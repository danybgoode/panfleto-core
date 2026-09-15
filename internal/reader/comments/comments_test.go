// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package comments // import "miniflux.app/v2/internal/reader/comments"

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"miniflux.app/v2/internal/config"
)

const threadURL = "https://news.ycombinator.com/item?id=49715934"

func setup(t *testing.T, reply func(string) (*http.Response, error)) (*int, *time.Time) {
	t.Helper()
	opts, err := config.NewConfigParser().ParseEnvironmentVariables()
	if err != nil {
		t.Fatalf("parsing config: %v", err)
	}
	config.Opts = opts

	originalExecute, originalNow, originalCache := execute, now, cache
	t.Cleanup(func() { execute, now, cache = originalExecute, originalNow, originalCache })

	calls := 0
	clock := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	execute = func(requestURL string) (*http.Response, error) {
		calls++
		return reply(requestURL)
	}
	now = func() time.Time { return clock }
	cache = newThreadCache()
	return &calls, &clock
}

func jsonReply(status int, body string) func(string) (*http.Response, error) {
	return func(requestURL string) (*http.Response, error) {
		request, _ := http.NewRequest(http.MethodGet, requestURL, nil)
		return &http.Response{StatusCode: status, Header: http.Header{}, ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}
}

func str(s string) *string { return &s }

func item(author, text string, children ...hackerNewsItem) hackerNewsItem {
	return hackerNewsItem{Author: str(author), Text: str(text), CreatedAt: 1757937600, Children: children}
}

func encode(t *testing.T, children ...hackerNewsItem) string {
	t.Helper()
	body, err := json.Marshal(hackerNewsItem{Author: str("op"), Children: children})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestHackerNewsItemID(t *testing.T) {
	cases := []struct {
		url string
		id  int64
		ok  bool
	}{
		{"https://news.ycombinator.com/item?id=49715934", 49715934, true},
		{"http://news.ycombinator.com/item?id=1", 1, true},
		{"https://NEWS.ycombinator.com/item?id=7", 7, true},
		{"https://news.ycombinator.com/item?id=abc", 0, false},
		{"https://news.ycombinator.com/item?id=-3", 0, false},
		{"https://news.ycombinator.com/user?id=pg", 0, false},
		{"https://news.ycombinator.com.evil.example/item?id=1", 0, false},
		{"https://evil.example/item?id=1&host=news.ycombinator.com", 0, false},
		{"javascript://news.ycombinator.com/item?id=1", 0, false},
		{"https://arstechnica.com/gadgets/2026/09/a/#comments", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		id, ok := HackerNewsItemID(c.url)
		if id != c.id || ok != c.ok || Supported(c.url) != c.ok {
			t.Errorf("HackerNewsItemID(%q) = %d, %v; want %d, %v", c.url, id, ok, c.id, c.ok)
		}
	}
}

func TestEveryCommentBodyIsSanitized(t *testing.T) {
	setup(t, nil)
	thread := buildThread([]hackerNewsItem{
		item("mallory", `<p>hi</p><script>alert(document.cookie)</script><img src=x onerror="alert(1)"><a href="javascript:alert(2)">x</a>`,
			item("eve", `<iframe src="https://evil.example"></iframe><p onclick="steal()">reply</p>`)),
	}, threadURL)

	html, err := Render(thread, "en_US", "UTC", threadURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"<script", "alert(document.cookie)", "onerror", "javascript:", "onclick", "evil.example"} {
		if strings.Contains(string(html), forbidden) {
			t.Errorf("%q survived the sanitizer:\n%s", forbidden, html)
		}
	}
	if !strings.Contains(string(html), "<p>hi</p>") || !strings.Contains(string(html), "reply") {
		t.Errorf("the harmless parts of the comments should remain:\n%s", html)
	}
}

func TestAuthorsAreEscaped(t *testing.T) {
	setup(t, nil)
	thread := buildThread([]hackerNewsItem{item(`<b onmouseover="x()">bob</b>`, "<p>ok</p>")}, threadURL)
	html, _ := Render(thread, "en_US", "UTC", threadURL)
	if strings.Contains(string(html), "<b onmouseover") {
		t.Fatalf("an author name must be text, not markup:\n%s", html)
	}
}

func TestDeletedAndEmptyCommentsAreSkipped(t *testing.T) {
	setup(t, nil)
	thread := buildThread([]hackerNewsItem{
		{Author: nil, Text: nil},
		item("ghost", "   "),
		item("alice", "<p>kept</p>"),
	}, threadURL)
	if thread.Count != 1 || len(thread.Comments) != 1 || thread.Comments[0].Author != "alice" {
		t.Fatalf("expected only alice's comment, got %+v", thread)
	}
}

func TestThreadsNestToTheDepthCap(t *testing.T) {
	setup(t, nil)
	deepest := item("d6", "<p>6</p>")
	chain := deepest
	for i := maxDepth - 1; i >= 0; i-- {
		chain = item("d", "<p>level</p>", chain)
	}
	thread := buildThread([]hackerNewsItem{chain}, threadURL)

	depth := 0
	for comments := thread.Comments; len(comments) > 0; comments = comments[0].Children {
		depth++
	}
	if depth != maxDepth || !thread.Truncated {
		t.Fatalf("expected %d rendered levels and a truncated thread, got %d levels, truncated=%v", maxDepth, depth, thread.Truncated)
	}

	html, _ := Render(thread, "en_US", "UTC", threadURL)
	if !strings.Contains(string(html), `href="https://news.ycombinator.com/item?id=49715934"`) {
		t.Fatalf("a truncated thread must link to the full discussion:\n%s", html)
	}
}

func TestThreadsStopAtTheCommentCap(t *testing.T) {
	setup(t, nil)
	var items []hackerNewsItem
	for range maxComments + 20 {
		items = append(items, item("a", "<p>x</p>"))
	}
	thread := buildThread(items, threadURL)
	if thread.Count != maxComments || len(thread.Comments) != maxComments || !thread.Truncated {
		t.Fatalf("expected %d comments and a truncated thread, got %d, truncated=%v", maxComments, thread.Count, thread.Truncated)
	}
}

func TestLoadFetchesTheAlgoliaItemAndCachesIt(t *testing.T) {
	var requested string
	body := ""
	calls, clock := setup(t, func(requestURL string) (*http.Response, error) {
		requested = requestURL
		return jsonReply(200, body)(requestURL)
	})
	body = encode(t, item("alice", "<p>first</p>", item("bob", "<p>reply</p>")))

	thread, err := Load(threadURL)
	if err != nil || thread.Count != 2 || thread.Comments[0].Children[0].Author != "bob" {
		t.Fatalf("expected a nested two-comment thread, got %+v, %v", thread, err)
	}
	if requested != "https://hn.algolia.com/api/v1/items/49715934" {
		t.Fatalf("expected the Algolia item URL, got %q", requested)
	}

	*clock = clock.Add(9 * time.Minute)
	if _, err := Load(threadURL); err != nil || *calls != 1 {
		t.Fatalf("a second open within the TTL must make no outbound request, got %d calls", *calls)
	}

	*clock = clock.Add(2 * time.Minute)
	if _, err := Load(threadURL); err != nil || *calls != 2 {
		t.Fatalf("a stale thread must refetch, got %d calls", *calls)
	}
}

func TestLoadReturnsAnErrorWhenTheAPIIsDown(t *testing.T) {
	for name, reply := range map[string]func(string) (*http.Response, error){
		"transport": func(string) (*http.Response, error) { return nil, errors.New("timeout") },
		"503":       jsonReply(503, "unavailable"),
		"not json":  jsonReply(200, "<html>"),
	} {
		setup(t, reply)
		if thread, err := Load(threadURL); err == nil || thread != nil {
			t.Errorf("%s: expected an error so the panel falls back to the link, got %+v", name, thread)
		}
	}
	setup(t, jsonReply(200, "{}"))
	if _, err := Load("https://arstechnica.com/a#comments"); err == nil {
		t.Error("an unsupported URL must not be fetched")
	}
}

func TestCacheIsBounded(t *testing.T) {
	setup(t, nil)
	for i := range cacheSize + 30 {
		cache.put(strings.Repeat("k", i+1), &Thread{})
	}
	if len(cache.threads) > cacheSize {
		t.Fatalf("expected at most %d cached threads, got %d", cacheSize, len(cache.threads))
	}
}

func TestRenderUnavailableLinksToTheThread(t *testing.T) {
	html, err := RenderUnavailable("en_US", threadURL)
	if err != nil || !strings.Contains(string(html), `href="https://news.ycombinator.com/item?id=49715934"`) || !strings.Contains(string(html), "View Comments") {
		t.Fatalf("expected the translated outbound link, got %s, %v", html, err)
	}
}

func TestADeletedCommentWithLiveRepliesMarksTheThreadTruncated(t *testing.T) {
	setup(t, nil)
	thread := buildThread([]hackerNewsItem{
		{Author: nil, Text: nil, Children: []hackerNewsItem{item("alice", "<p>still here</p>")}},
		item("bob", "<p>top</p>"),
	}, threadURL)
	if thread.Count != 1 || !thread.Truncated {
		t.Fatalf("hidden live replies must mark the thread truncated so it links out, got count=%d truncated=%v", thread.Count, thread.Truncated)
	}
}

func TestDeletedRepliesAtTheDepthCapDoNotTruncate(t *testing.T) {
	setup(t, nil)
	chain := item("last", "<p>deepest shown</p>", hackerNewsItem{Author: nil, Text: nil})
	for i := maxDepth - 2; i >= 0; i-- {
		chain = item("d", "<p>level</p>", chain)
	}
	if thread := buildThread([]hackerNewsItem{chain}, threadURL); thread.Truncated {
		t.Fatal("only deleted comments below the cap is not a truncated thread")
	}
}
