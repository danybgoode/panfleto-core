// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package autofetch // import "miniflux.app/v2/internal/reader/autofetch"

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/reader/fetcher"
)

func setConfig(t *testing.T, env map[string]string) {
	t.Helper()
	for key, value := range env {
		t.Setenv(key, value)
	}
	opts, err := config.NewConfigParser().ParseEnvironmentVariables()
	if err != nil {
		t.Fatalf("parsing config: %v", err)
	}
	config.Opts = opts
}

func paragraph(chars int) string {
	return "<p>" + strings.Repeat("a", chars) + "</p>"
}

type fakeChain struct {
	direct     string
	directErr  error
	fallback   string
	directHits int
	stepHits   int
}

func (f *fakeChain) install(t *testing.T) {
	t.Helper()
	originalScrape, originalSteps := scrapeWebsite, fallbackSteps
	t.Cleanup(func() { scrapeWebsite, fallbackSteps = originalScrape, originalSteps })

	scrapeWebsite = func(_ *fetcher.RequestBuilder, pageURL, _ string) (string, string, error) {
		f.directHits++
		return pageURL, f.direct, f.directErr
	}
	fallbackSteps = map[string]step{"unwall": func(pageURL string) (string, string) {
		f.stepHits++
		return "https://unwall.example/base", f.fallback
	}}
}

func TestFetchKeepsAFullDirectScrapeAndSkipsTheFallback(t *testing.T) {
	setConfig(t, map[string]string{"FETCH_FALLBACK_CHAIN": "unwall", "FETCH_THIN_CONTENT_THRESHOLD": "100"})
	chain := &fakeChain{direct: paragraph(150), fallback: paragraph(900)}
	chain.install(t)

	_, content, err := Fetch(nil, &model.Feed{}, "https://example.org/a")
	if err != nil || content != paragraph(150) {
		t.Fatalf("expected the direct scrape, got %q, %v", content, err)
	}
	if chain.stepHits != 0 {
		t.Fatalf("a full direct scrape must not call the fallback, it was called %d times", chain.stepHits)
	}
}

func TestFetchUsesTheFallbackWhenDirectIsThin(t *testing.T) {
	setConfig(t, map[string]string{"FETCH_FALLBACK_CHAIN": "unwall", "FETCH_THIN_CONTENT_THRESHOLD": "100"})
	chain := &fakeChain{direct: paragraph(40), fallback: paragraph(500)}
	chain.install(t)

	baseURL, content, err := Fetch(nil, &model.Feed{}, "https://example.org/a")
	if err != nil || content != paragraph(500) || baseURL != "https://unwall.example/base" {
		t.Fatalf("expected the fallback's content and base URL, got %q, %q, %v", baseURL, content, err)
	}
}

func TestFetchUsesTheFallbackWhenDirectErrors(t *testing.T) {
	setConfig(t, map[string]string{"FETCH_FALLBACK_CHAIN": "unwall", "FETCH_THIN_CONTENT_THRESHOLD": "100"})
	chain := &fakeChain{directErr: errors.New("403"), fallback: paragraph(500)}
	chain.install(t)

	_, content, err := Fetch(nil, &model.Feed{}, "https://example.org/a")
	if err != nil || content != paragraph(500) {
		t.Fatalf("a fallback hit must clear step one's error, got %q, %v", content, err)
	}
}

func TestFetchKeepsTheLongerResultWhenEveryStepIsThin(t *testing.T) {
	setConfig(t, map[string]string{"FETCH_FALLBACK_CHAIN": "unwall", "FETCH_THIN_CONTENT_THRESHOLD": "1000"})

	chain := &fakeChain{direct: paragraph(300), fallback: paragraph(80)}
	chain.install(t)
	if _, content, _ := Fetch(nil, &model.Feed{}, "https://example.org/a"); content != paragraph(300) {
		t.Fatalf("a shorter fallback must not replace the direct scrape, got %q", content)
	}

	chain.direct, chain.fallback = paragraph(80), paragraph(300)
	if _, content, _ := Fetch(nil, &model.Feed{}, "https://example.org/a"); content != paragraph(300) {
		t.Fatalf("a longer thin fallback should win over a thinner direct scrape, got %q", content)
	}
}

func TestFetchReturnsStepOnesErrorWhenEveryStepIsEmpty(t *testing.T) {
	setConfig(t, map[string]string{"FETCH_FALLBACK_CHAIN": "unwall"})
	directErr := errors.New("404")
	chain := &fakeChain{directErr: directErr}
	chain.install(t)

	_, content, err := Fetch(nil, &model.Feed{}, "https://example.org/a")
	if content != "" || !errors.Is(err, directErr) {
		t.Fatalf("expected no content and step one's error so the entry keeps its feed content, got %q, %v", content, err)
	}
}

func TestFetchWithAnEmptyChainIsTodaysScraper(t *testing.T) {
	setConfig(t, map[string]string{"FETCH_THIN_CONTENT_THRESHOLD": "100"})
	chain := &fakeChain{direct: paragraph(10), fallback: paragraph(900)}
	chain.install(t)

	if _, content, _ := Fetch(nil, &model.Feed{}, "https://example.org/a"); content != paragraph(10) || chain.stepHits != 0 {
		t.Fatalf("the kill switch (empty FETCH_FALLBACK_CHAIN) must only scrape directly, got %q and %d fallback calls", content, chain.stepHits)
	}
}

func TestIsThinCountsTextNotMarkup(t *testing.T) {
	setConfig(t, map[string]string{"FETCH_THIN_CONTENT_THRESHOLD": "10"})
	if !IsThin(`<p><a href="https://example.org/a-very-long-url-that-is-not-text">Comments</a></p>`) {
		t.Fatal("an HN-style link-only entry is thin: markup is not text")
	}
	if IsThin("<p>ñandú ñandú ñandú</p>") {
		t.Fatal("seventeen characters of text is not thin at a threshold of ten")
	}
}

func TestUnwallRequestURL(t *testing.T) {
	cases := []struct {
		pageURL, want string
		ok            bool
	}{
		{"https://www.ft.com/content/e14542d9?syn-25a6b1a6=1", "https://api.unwall.app/fetch?url=https%3A%2F%2Fwww.ft.com%2Fcontent%2Fe14542d9%3Fsyn-25a6b1a6%3D1", true},
		{"https://elpais.com/sociedad/2026-09-15/a.html", "https://api.unwall.app/fetch?url=https%3A%2F%2Felpais.com%2Fsociedad%2F2026-09-15%2Fa.html", true},
		{"http://nytimes.com/a#top", "https://api.unwall.app/fetch?url=http%3A%2F%2Fnytimes.com%2Fa%23top", true},
		{"not-a-url", "", false},
		{"javascript:alert(1)", "", false},
		{"ftp://example.org/a", "", false},
		{"https:///no-host", "", false},
	}
	for _, c := range cases {
		got, ok := unwallRequestURL(c.pageURL)
		if got != c.want || ok != c.ok {
			t.Errorf("unwallRequestURL(%q) = %q, %v; want %q, %v", c.pageURL, got, ok, c.want, c.ok)
		}
	}
}

func unwallReply(status int, headers map[string]string, body string) func(string) (*http.Response, error) {
	return func(requestURL string) (*http.Response, error) {
		request, _ := http.NewRequest(http.MethodGet, requestURL, nil)
		response := &http.Response{StatusCode: status, Header: http.Header{}, ContentLength: int64(len(body)), Body: io.NopCloser(strings.NewReader(body)), Request: request}
		for key, value := range headers {
			response.Header.Set(key, value)
		}
		return response, nil
	}
}

func installUnwall(t *testing.T, execute func(string) (*http.Response, error)) *time.Time {
	t.Helper()
	originalExecute, originalNow, originalState, originalSleep := unwallExecute, unwallNow, unwallState, unwallSleep
	t.Cleanup(func() {
		unwallExecute, unwallNow, unwallState, unwallSleep = originalExecute, originalNow, originalState, originalSleep
	})
	unwallSleep = func(time.Duration) {}

	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	unwallExecute = execute
	unwallNow = func() time.Time { return now }
	unwallState = newUnwallGuard()
	return &now
}

const unwallArticle = `{"html":"<html><body><article><h1>Title</h1><p>` +
	`This is the first paragraph of a long article body, written to look like prose so readability keeps it.</p><p>` +
	`This is the second paragraph of that body, which also carries enough words to be counted as content.</p>` +
	`</article></body></html>","finalUrl":"https://www.nytimes.com/a","challenge":false,"browserRendered":false}`

func TestFetchFromUnwallExtractsTheArticle(t *testing.T) {
	setConfig(t, nil)
	installUnwall(t, unwallReply(200, map[string]string{"RateLimit-Remaining": "117"}, unwallArticle))

	baseURL, content := fetchFromUnwall("https://nytimes.com/a")
	if baseURL != "https://www.nytimes.com/a" {
		t.Errorf("expected finalUrl as the base URL, got %q", baseURL)
	}
	if !strings.Contains(content, "second paragraph") || strings.Contains(content, "<html") {
		t.Errorf("expected readability-extracted article content, got %q", content)
	}
}

func TestFetchFromUnwallMissesWithoutAnArticle(t *testing.T) {
	setConfig(t, nil)
	for name, reply := range map[string]func(string) (*http.Response, error){
		"challenge":   unwallReply(200, nil, `{"html":"<p>verify you are human</p>","challenge":true}`),
		"empty html":  unwallReply(200, nil, `{"html":"","finalUrl":null}`),
		"not json":    unwallReply(200, nil, `<!DOCTYPE html><title>UnWall</title>`),
		"bad request": unwallReply(400, nil, `{"error":"Invalid URL"}`),
		"forbidden":   unwallReply(403, nil, `blocked`),
		"transport":   func(string) (*http.Response, error) { return nil, errors.New("connection refused") },
	} {
		installUnwall(t, reply)
		if _, content := fetchFromUnwall("https://www.ft.com/content/a"); content != "" {
			t.Errorf("%s: expected a miss, got %q", name, content)
		}
	}
}

func TestUnwallCoolsDownOnRateLimitAndNeverCallsDuringIt(t *testing.T) {
	setConfig(t, nil)
	calls := 0
	now := installUnwall(t, func(requestURL string) (*http.Response, error) {
		calls++
		return unwallReply(429, map[string]string{"RateLimit-Reset": "42"}, "Too many requests")(requestURL)
	})

	fetchFromUnwall("https://www.nytimes.com/a")
	fetchFromUnwall("https://www.nytimes.com/b")
	if calls != 1 {
		t.Fatalf("expected the 429 to stop the next call, got %d calls", calls)
	}

	*now = now.Add(43 * time.Second)
	fetchFromUnwall("https://www.nytimes.com/c")
	if calls != 2 {
		t.Fatalf("expected a call once RateLimit-Reset has passed, got %d calls", calls)
	}
}

func TestUnwallCoolsDownBeforeTheLimitIsSpent(t *testing.T) {
	setConfig(t, nil)
	calls := 0
	now := installUnwall(t, func(requestURL string) (*http.Response, error) {
		calls++
		return unwallReply(200, map[string]string{"RateLimit-Remaining": "10", "RateLimit-Reset": "30"}, unwallArticle)(requestURL)
	})

	fetchFromUnwall("https://www.nytimes.com/a")
	fetchFromUnwall("https://www.nytimes.com/b")
	*now = now.Add(31 * time.Second)
	fetchFromUnwall("https://www.nytimes.com/c")
	if calls != 2 {
		t.Fatalf("expected remaining <= 10 to pause until reset, got %d calls", calls)
	}
}

func TestUnwallBacksOffExponentiallyOnFailures(t *testing.T) {
	setConfig(t, nil)
	calls := 0
	now := installUnwall(t, func(string) (*http.Response, error) {
		calls++
		return nil, errors.New("timeout")
	})

	expected := []time.Duration{0, 0, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 30 * time.Minute, 30 * time.Minute}
	for i, backoff := range expected {
		fetchFromUnwall("https://www.nytimes.com/a")
		if calls != i+1 {
			t.Fatalf("step %d: expected %d calls, got %d", i, i+1, calls)
		}
		if wait := unwallState.cooldown(*now); wait != backoff {
			t.Fatalf("failure %d: expected a %s back-off, got %s", i+1, backoff, wait)
		}
		*now = now.Add(backoff)
	}
}

func TestUnwallSuccessResetsTheFailureCount(t *testing.T) {
	setConfig(t, nil)
	fail := true
	now := installUnwall(t, func(requestURL string) (*http.Response, error) {
		if fail {
			return nil, errors.New("timeout")
		}
		return unwallReply(200, nil, unwallArticle)(requestURL)
	})

	fetchFromUnwall("https://www.nytimes.com/a")
	fetchFromUnwall("https://www.nytimes.com/b")
	fail = false
	fetchFromUnwall("https://www.nytimes.com/c")
	fail = true
	fetchFromUnwall("https://www.nytimes.com/d")
	fetchFromUnwall("https://www.nytimes.com/e")
	if wait := unwallState.cooldown(*now); wait != 0 {
		t.Fatalf("two failures after a success must not back off, got %s", wait)
	}
}

func TestUnwallCachesResultsForAnHour(t *testing.T) {
	setConfig(t, nil)
	calls := 0
	now := installUnwall(t, func(requestURL string) (*http.Response, error) {
		calls++
		return unwallReply(200, nil, unwallArticle)(requestURL)
	})

	first, _ := fetchFromUnwall("https://www.nytimes.com/a")
	*now = now.Add(59 * time.Minute)
	second, _ := fetchFromUnwall("https://www.nytimes.com/a")
	if calls != 1 || first != second {
		t.Fatalf("expected a cache hit within the hour, got %d calls", calls)
	}

	*now = now.Add(2 * time.Minute)
	fetchFromUnwall("https://www.nytimes.com/a")
	if calls != 2 {
		t.Fatalf("expected a stale entry to refetch, got %d calls", calls)
	}
}

func TestUnwallCacheIsBounded(t *testing.T) {
	setConfig(t, nil)
	installUnwall(t, unwallReply(200, nil, unwallArticle))
	for i := range unwallCacheSize + 50 {
		unwallState.store("https://example.org/"+strings.Repeat("x", i), "", "")
	}
	if size := len(unwallState.results); size > unwallCacheSize {
		t.Fatalf("expected at most %d cached results, got %d", unwallCacheSize, size)
	}
}

func TestFetchNeverSendsAPrivateFeedToTheFallback(t *testing.T) {
	setConfig(t, map[string]string{"FETCH_FALLBACK_CHAIN": "unwall", "FETCH_THIN_CONTENT_THRESHOLD": "100"})
	chain := &fakeChain{direct: paragraph(10), fallback: paragraph(900)}
	chain.install(t)

	for name, feed := range map[string]*model.Feed{
		"basic auth": {Username: "reader", Password: "secret"},
		"cookie":     {Cookie: "session=abc"},
	} {
		if _, content, _ := Fetch(nil, feed, "https://example.substack.com/p/a?token=abc"); content != paragraph(10) {
			t.Errorf("%s: a feed with credentials must scrape directly only, got %q", name, content)
		}
	}
	if chain.stepHits != 0 {
		t.Fatalf("a private feed's article URL was sent to the fallback %d times", chain.stepHits)
	}
}

func TestUnwallReserveSpacesCallsWithoutALock(t *testing.T) {
	guard := newUnwallGuard()
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	if wait := guard.reserve(now); wait != 0 {
		t.Fatalf("the first call waits for nothing, got %s", wait)
	}
	if wait := guard.reserve(now); wait != unwallMinSpacing {
		t.Fatalf("a concurrent second call waits one spacing, got %s", wait)
	}
	if wait := guard.reserve(now); wait != 2*unwallMinSpacing {
		t.Fatalf("a third call waits two spacings, got %s", wait)
	}
	if wait := guard.reserve(now.Add(time.Hour)); wait != 0 {
		t.Fatalf("a call long after the last one waits for nothing, got %s", wait)
	}
}
