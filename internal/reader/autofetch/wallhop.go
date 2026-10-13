// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package autofetch // import "miniflux.app/v2/internal/reader/autofetch"

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/reader/fetcher"
	"miniflux.app/v2/internal/reader/readability"
)

const wallhopAPIURL = "https://wallhop.io/api/"

type wallhopResult struct {
	baseURL string
	content string
	expires time.Time
}

type wallhopGuard struct {
	mu       sync.Mutex
	nextSlot time.Time
	until    time.Time
	failures int
	results  map[string]wallhopResult
}

var (
	wallhopNow     = time.Now
	wallhopState   = &wallhopGuard{results: make(map[string]wallhopResult)}
	wallhopExecute = func(requestURL string) (*http.Response, error) {
		return fetcher.NewRequestBuilder().
			WithUserAgent("", config.Opts.HTTPClientUserAgent()).
			WithTimeout(config.Opts.HTTPClientTimeout()).
			ExecuteRequest(requestURL)
	}
	wallhopSleep = time.Sleep
)

func wallhopRequestURL(pageURL string) (string, bool) {
	parsed, err := url.Parse(pageURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "", false
	}
	if address, err := netip.ParseAddr(host); err == nil &&
		(address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified()) {
		return "", false
	}
	for key := range parsed.Query() {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
			strings.Contains(lower, "password") || strings.Contains(lower, "auth") ||
			strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") {
			return "", false
		}
	}
	parsed.Fragment = ""
	// Wallhop's published path format is /api/https://host/path, including the target query string.
	return wallhopAPIURL + parsed.String(), true
}

func (g *wallhopGuard) begin(pageURL string, now time.Time) (wallhopResult, bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if cached, found := g.results[pageURL]; found && now.Before(cached.expires) {
		return cached, true, 0
	}
	if now.Before(g.until) {
		return wallhopResult{}, false, -1
	}
	slot := now
	if g.nextSlot.After(slot) {
		slot = g.nextSlot
	}
	g.nextSlot = slot.Add(500 * time.Millisecond)
	return wallhopResult{}, false, slot.Sub(now)
}

func (g *wallhopGuard) observe(response *http.Response, clientErr error, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if clientErr != nil || response == nil || response.StatusCode >= 500 {
		g.failures++
		if g.failures >= 3 {
			g.until = now.Add(min(time.Minute<<min(g.failures-3, 5), 30*time.Minute))
		}
		return
	}
	g.failures = 0
	if response.StatusCode == http.StatusTooManyRequests {
		g.until = now.Add(time.Minute)
		if delay := fetcher.NewResponseHandler(response, nil).ParseRetryDelay(); delay > 0 && delay < time.Hour {
			g.until = now.Add(delay)
		}
	}
}

func (g *wallhopGuard) store(pageURL, baseURL, content string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.results) >= 256 {
		for key, result := range g.results {
			if !now.Before(result.expires) {
				delete(g.results, key)
			}
		}
		for key := range g.results {
			if len(g.results) < 256 {
				break
			}
			delete(g.results, key)
		}
	}
	g.results[pageURL] = wallhopResult{baseURL, content, now.Add(time.Hour)}
}

func fetchFromWallhop(pageURL string) (baseURL, content string) {
	requestURL, ok := wallhopRequestURL(pageURL)
	if !ok {
		return "", ""
	}
	if cached, found, wait := wallhopState.begin(pageURL, wallhopNow()); found {
		return cached.baseURL, cached.content
	} else if wait < 0 {
		return "", ""
	} else if wait > 3*time.Second {
		return "", ""
	} else if wait > 0 {
		wallhopSleep(wait)
	}

	response, clientErr := wallhopExecute(requestURL)
	handler := fetcher.NewResponseHandler(response, clientErr)
	defer handler.Close()
	wallhopState.observe(response, clientErr, wallhopNow())
	if err := handler.LocalizedError(); err != nil {
		slog.Warn("Fetch fallback wallhop missed", slog.String("entry_url", pageURL), slog.Any("error", err.Error()))
		return "", ""
	}
	if !strings.HasPrefix(strings.ToLower(handler.ContentType()), "application/json") {
		return "", ""
	}
	body, err := handler.ReadBody(config.Opts.HTTPClientMaxBodySize())
	if err != nil {
		return "", ""
	}
	baseURL, content = extractWallhopBody(pageURL, body)
	if content != "" {
		wallhopState.store(pageURL, baseURL, content, wallhopNow())
	}
	return baseURL, content
}

// The observed API response contains a full HTML document in `body`, not extracted prose.
// Treat it as an untrusted page and pass it through the reader's normal extraction/sanitization path.
func extractWallhopBody(pageURL string, body []byte) (baseURL, content string) {
	var payload struct {
		Body string `json:"body"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Body == "" {
		return "", ""
	}
	baseURL, content, err := readability.ExtractContent(strings.NewReader(payload.Body))
	if err != nil {
		return "", ""
	}
	if baseURL == "" {
		baseURL = pageURL
	}
	text := strings.ToLower(content)
	if strings.Contains(text, "verify you are human") || strings.Contains(text, "checking your browser") || strings.Contains(text, "captcha") {
		return "", ""
	}
	return baseURL, content
}
