// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package autofetch // import "miniflux.app/v2/internal/reader/autofetch"

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/reader/fetcher"
	"miniflux.app/v2/internal/reader/readability"
)

// Everything here follows Roadmap/01-reading-experience/spike-unwall-app/sprint-1.md's Decision.
const (
	unwallAPIURL                = "https://api.unwall.app/fetch"
	unwallRemainingSlack        = 10
	unwallDefaultReset          = time.Minute
	unwallFailuresBeforeBackoff = 3
	unwallBackoffMin            = time.Minute
	unwallBackoffMax            = 30 * time.Minute
	unwallCacheTTL              = time.Hour
	unwallCacheSize             = 256
	unwallMinSpacing            = 500 * time.Millisecond
)

// Swapped out by tests.
var (
	unwallExecute = func(requestURL string) (*http.Response, error) {
		return fetcher.NewRequestBuilder().
			WithUserAgent("", config.Opts.HTTPClientUserAgent()).
			WithTimeout(config.Opts.HTTPClientTimeout()).
			ExecuteRequest(requestURL)
	}
	unwallNow   = time.Now
	unwallState = newUnwallGuard()
	unwallSleep = time.Sleep
)

// unwallRequestURL passes the entry URL whole, as one encoded query parameter (decision 5). Only
// absolute http(s) URLs are sent: the API answers 400 to anything without a scheme.
func unwallRequestURL(pageURL string) (string, bool) {
	parsed, err := url.Parse(pageURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", false
	}
	return unwallAPIURL + "?" + url.Values{"url": {pageURL}}.Encode(), true
}

type unwallResponse struct {
	HTML      string `json:"html"`
	FinalURL  string `json:"finalUrl"`
	Challenge bool   `json:"challenge"`
}

func fetchFromUnwall(pageURL string) (baseURL, content string) {
	requestURL, ok := unwallRequestURL(pageURL)
	if !ok {
		return "", ""
	}

	if cached, found := unwallState.cached(pageURL); found {
		return cached.baseURL, cached.content
	}

	if wait := unwallState.cooldown(unwallNow()); wait > 0 {
		slog.Debug("Fetch fallback unwall is cooling down", slog.String("entry_url", pageURL), slog.Duration("remaining", wait))
		return "", ""
	}

	// Space calls to the fallback host without holding a lock across the request, so a Download press
	// never queues behind a worker's slow call.
	if wait := unwallState.reserve(unwallNow()); wait > 0 {
		unwallSleep(wait)
	}

	response, clientErr := unwallExecute(requestURL)
	responseHandler := fetcher.NewResponseHandler(response, clientErr)
	defer responseHandler.Close()

	unwallState.observe(response, clientErr, unwallNow())

	if localizedError := responseHandler.LocalizedError(); localizedError != nil {
		slog.Warn("Fetch fallback unwall missed", slog.String("entry_url", pageURL), slog.Any("error", localizedError.Error()))
		return "", ""
	}

	body, localizedError := responseHandler.ReadBody(config.Opts.HTTPClientMaxBodySize())
	if localizedError != nil {
		slog.Warn("Fetch fallback unwall missed", slog.String("entry_url", pageURL), slog.Any("error", localizedError.Error()))
		return "", ""
	}

	baseURL, content = extractUnwallBody(pageURL, body)
	unwallState.store(pageURL, baseURL, content)
	return baseURL, content
}

// extractUnwallBody turns the API's JSON into article content. html is the publisher's whole page,
// so it goes through readability like any scrape (decision 2).
func extractUnwallBody(pageURL string, body []byte) (baseURL, content string) {
	var payload unwallResponse
	if err := json.Unmarshal(body, &payload); err != nil || payload.HTML == "" || payload.Challenge {
		slog.Warn("Fetch fallback unwall returned no article", slog.String("entry_url", pageURL), slog.Bool("challenge", payload.Challenge))
		return "", ""
	}

	documentBaseURL, extracted, err := readability.ExtractContent(strings.NewReader(payload.HTML))
	if err != nil {
		return "", ""
	}

	switch {
	case documentBaseURL != "":
		baseURL = documentBaseURL
	case payload.FinalURL != "":
		baseURL = payload.FinalURL
	default:
		baseURL = pageURL
	}
	return baseURL, extracted
}

type unwallCached struct {
	baseURL, content string
	expires          time.Time
}

// unwallGuard holds the step's rate-limit cooldown and its one-hour result cache (decisions 3 and 4).
type unwallGuard struct {
	mu       sync.Mutex
	until    time.Time
	failures int
	nextSlot time.Time
	results  map[string]unwallCached
}

// reserve books the next call slot, at least unwallMinSpacing after the previous one, and returns how
// long the caller must wait for it.
func (g *unwallGuard) reserve(now time.Time) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	slot := now
	if g.nextSlot.After(slot) {
		slot = g.nextSlot
	}
	g.nextSlot = slot.Add(unwallMinSpacing)
	return slot.Sub(now)
}

func newUnwallGuard() *unwallGuard {
	return &unwallGuard{results: make(map[string]unwallCached)}
}

func (g *unwallGuard) cooldown(now time.Time) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Before(g.until) {
		return g.until.Sub(now)
	}
	return 0
}

// observe applies decision 3: a 429 or a nearly spent RateLimit-Remaining waits out RateLimit-Reset;
// transport errors and 5xx back off exponentially from one minute to thirty. The back-off starts at the
// third failure in a row, so one slow article (a timeout) doesn't switch the step off for everyone.
func (g *unwallGuard) observe(response *http.Response, clientErr error, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if clientErr != nil || response == nil || response.StatusCode >= 500 {
		g.failures++
		if g.failures >= unwallFailuresBeforeBackoff {
			backoff := min(unwallBackoffMin<<min(g.failures-unwallFailuresBeforeBackoff, 5), unwallBackoffMax)
			g.until = now.Add(backoff)
		}
		return
	}
	g.failures = 0

	remaining, remainingErr := strconv.Atoi(response.Header.Get("RateLimit-Remaining"))
	if response.StatusCode == http.StatusTooManyRequests || (remainingErr == nil && remaining <= unwallRemainingSlack) {
		reset := unwallDefaultReset
		if seconds, err := strconv.Atoi(response.Header.Get("RateLimit-Reset")); err == nil && seconds > 0 {
			reset = time.Duration(seconds) * time.Second
		}
		g.until = now.Add(reset)
	}
}

func (g *unwallGuard) cached(pageURL string) (unwallCached, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	result, found := g.results[pageURL]
	if !found || unwallNow().After(result.expires) {
		return unwallCached{}, false
	}
	return result, true
}

func (g *unwallGuard) store(pageURL, baseURL, content string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := unwallNow()
	if len(g.results) >= unwallCacheSize {
		for key, result := range g.results {
			if now.After(result.expires) {
				delete(g.results, key)
			}
		}
		for key := range g.results {
			if len(g.results) < unwallCacheSize {
				break
			}
			delete(g.results, key)
		}
	}
	g.results[pageURL] = unwallCached{baseURL: baseURL, content: content, expires: now.Add(unwallCacheTTL)}
}
