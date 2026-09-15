// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package comments brings a discussion thread into the reader: an adapter per comment source, an
// in-process cache, and the HTML fragment the entry page lazy-loads. One adapter, Hacker News, by the
// product owner's decision. Roadmap/01-reading-experience/inline-comments (D1–D6).
package comments // import "miniflux.app/v2/internal/reader/comments"

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/reader/fetcher"
)

// D1 and D2.
const (
	cacheTTL        = 10 * time.Minute
	cacheSize       = 128
	maxDepth        = 5
	maxComments     = 300
	hackerNewsItems = "https://hn.algolia.com/api/v1/items/"
)

// Comment is one sanitized comment and its replies.
type Comment struct {
	Author   string
	Created  time.Time
	Body     string
	Children []*Comment
}

// Thread is what the panel renders.
type Thread struct {
	Comments  []*Comment
	Count     int
	Truncated bool
}

// Swapped out by tests.
var (
	execute = func(requestURL string) (*http.Response, error) {
		return fetcher.NewRequestBuilder().
			WithUserAgent("", config.Opts.HTTPClientUserAgent()).
			WithTimeout(config.Opts.HTTPClientTimeout()).
			ExecuteRequest(requestURL)
	}
	now   = time.Now
	cache = newThreadCache()
)

// HackerNewsItemID extracts the item id from a news.ycombinator.com/item?id=N comments URL.
func HackerNewsItemID(commentsURL string) (int64, bool) {
	parsed, err := url.Parse(commentsURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return 0, false
	}
	if !strings.EqualFold(parsed.Hostname(), "news.ycombinator.com") || parsed.Path != "/item" {
		return 0, false
	}
	id, err := strconv.ParseInt(parsed.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// Supported reports whether an adapter can load this comments URL (D5).
func Supported(commentsURL string) bool {
	_, ok := HackerNewsItemID(commentsURL)
	return ok
}

// Load returns the thread behind a supported comments URL, from the cache when it is fresh.
func Load(commentsURL string) (*Thread, error) {
	id, ok := HackerNewsItemID(commentsURL)
	if !ok {
		return nil, errors.New("comments: unsupported comments URL")
	}

	key := "hn:" + strconv.FormatInt(id, 10)
	if thread, found := cache.get(key); found {
		slog.Debug("Comments served from cache", slog.String("source", "hackernews"), slog.Int64("item_id", id))
		return thread, nil
	}

	thread, err := fetchHackerNews(id, commentsURL)
	if err != nil {
		slog.Warn("Comments unavailable", slog.String("source", "hackernews"), slog.Int64("item_id", id), slog.Any("error", err))
		return nil, err
	}

	cache.put(key, thread)
	slog.Info("Comments fetched", slog.String("source", "hackernews"), slog.Int64("item_id", id), slog.Int("comments", thread.Count), slog.Bool("truncated", thread.Truncated))
	return thread, nil
}

type hackerNewsItem struct {
	Author    *string          `json:"author"`
	Text      *string          `json:"text"`
	CreatedAt int64            `json:"created_at_i"`
	Children  []hackerNewsItem `json:"children"`
}

func fetchHackerNews(id int64, commentsURL string) (*Thread, error) {
	response, clientErr := execute(hackerNewsItems + strconv.FormatInt(id, 10))
	responseHandler := fetcher.NewResponseHandler(response, clientErr)
	defer responseHandler.Close()

	if localizedError := responseHandler.LocalizedError(); localizedError != nil {
		return nil, localizedError.Error()
	}

	body, localizedError := responseHandler.ReadBody(config.Opts.HTTPClientMaxBodySize())
	if localizedError != nil {
		return nil, localizedError.Error()
	}

	var item hackerNewsItem
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, fmt.Errorf("comments: unable to parse the Hacker News item: %w", err)
	}

	return buildThread(item.Children, commentsURL), nil
}

// buildThread walks the tree depth-first, skipping deleted and empty comments (their replies too - HN
// shows them under a "[deleted]" stub we don't render), and stops at maxDepth and maxComments.
func buildThread(items []hackerNewsItem, commentsURL string) *Thread {
	thread := &Thread{}

	var walk func(items []hackerNewsItem, depth int) []*Comment
	walk = func(items []hackerNewsItem, depth int) []*Comment {
		var out []*Comment
		for _, item := range items {
			if !isLive(item) {
				// A deleted comment's live replies aren't rendered; say the thread is incomplete.
				if hasLiveReply(item.Children) {
					thread.Truncated = true
				}
				continue
			}
			if thread.Count >= maxComments {
				thread.Truncated = true
				return out
			}
			thread.Count++

			comment := &Comment{
				Author:  *item.Author,
				Created: time.Unix(item.CreatedAt, 0),
				Body:    sanitizeBody(commentsURL, *item.Text),
			}
			if depth+1 < maxDepth {
				comment.Children = walk(item.Children, depth+1)
			} else if hasLiveReply(item.Children) {
				thread.Truncated = true
			}
			out = append(out, comment)
		}
		return out
	}

	thread.Comments = walk(items, 0)
	return thread
}

func isLive(item hackerNewsItem) bool {
	return item.Author != nil && item.Text != nil && strings.TrimSpace(*item.Text) != ""
}

func hasLiveReply(items []hackerNewsItem) bool {
	for _, item := range items {
		if isLive(item) || hasLiveReply(item.Children) {
			return true
		}
	}
	return false
}

type cachedThread struct {
	thread  *Thread
	expires time.Time
}

type threadCache struct {
	mu      sync.Mutex
	threads map[string]cachedThread
}

func newThreadCache() *threadCache {
	return &threadCache{threads: make(map[string]cachedThread)}
}

func (c *threadCache) get(key string) (*Thread, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cached, found := c.threads[key]
	if !found || now().After(cached.expires) {
		return nil, false
	}
	return cached.thread, true
}

func (c *threadCache) put(key string, thread *Thread) {
	c.mu.Lock()
	defer c.mu.Unlock()

	current := now()
	if len(c.threads) >= cacheSize {
		for k, cached := range c.threads {
			if current.After(cached.expires) {
				delete(c.threads, k)
			}
		}
		for k := range c.threads {
			if len(c.threads) < cacheSize {
				break
			}
			delete(c.threads, k)
		}
	}
	c.threads[key] = cachedThread{thread: thread, expires: current.Add(cacheTTL)}
}
