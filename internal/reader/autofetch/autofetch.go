// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package autofetch is panfleto's fetch fallback chain: the one function both the poll path and the
// Download button use to get an article's content. Step one is Miniflux's own scraper, unchanged;
// the steps after it come from FETCH_FALLBACK_CHAIN and only run when step one came back thin.
// Roadmap/01-reading-experience/article-autofetch (D1, D3).
package autofetch // import "miniflux.app/v2/internal/reader/autofetch"

import (
	"context"
	"log/slog"
	"unicode/utf8"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/reader/fetcher"
	"miniflux.app/v2/internal/reader/sanitizer"
	"miniflux.app/v2/internal/reader/scraper"
)

// SourceDirect names step one in the log.
const SourceDirect = "direct"

// step fetches pageURL through one fallback service. It returns empty content, never an error, on a
// miss: a fallback that fails the fetch is worse than no fallback.
type step func(pageURL string) (baseURL, content string)

// Swapped out by tests.
var (
	scrapeWebsite = scraper.ScrapeWebsite
	fallbackSteps = map[string]step{"unwall": fetchFromUnwall}
)

// TextLength counts the characters of text in an HTML fragment.
func TextLength(content string) int {
	return utf8.RuneCountInString(sanitizer.StripTags(content))
}

// IsThin reports whether content is too short to be the article (A2: a teaser, not a story).
func IsThin(content string) bool {
	return TextLength(content) < config.Opts.FetchThinContentThreshold()
}

// Fetch returns an article's content: the direct scrape if it isn't thin, otherwise the first
// fallback that isn't, otherwise the longest non-empty result. The error is step one's, and it is
// only returned when no step produced anything.
//
// The fallbacks only run for a feed that sends no credentials: a private feed's article links can carry
// per-subscriber tokens, and those must not be handed to a third party.
func Fetch(requestBuilder *fetcher.RequestBuilder, feed *model.Feed, pageURL string) (baseURL, content string, err error) {
	scraperRules := feed.ScraperRules
	baseURL, content, err = scrapeWebsite(requestBuilder, pageURL, scraperRules)
	source := SourceDirect
	if err != nil || content == "" {
		source = ""
	}

	if err == nil && !IsThin(content) {
		logSource(pageURL, source, content)
		return baseURL, content, nil
	}

	chain := config.Opts.FetchFallbackChain()
	if feed.Username != "" || feed.Password != "" || feed.Cookie != "" {
		chain = nil
	}

	for _, name := range chain {
		fetchStep, ok := fallbackSteps[name]
		if !ok {
			continue
		}

		stepBaseURL, stepContent := fetchStep(pageURL)
		if stepContent == "" || TextLength(stepContent) <= TextLength(content) {
			continue
		}

		baseURL, content, err, source = stepBaseURL, stepContent, nil, name
		if !IsThin(content) {
			break
		}
	}

	logSource(pageURL, source, content)
	return baseURL, content, err
}

// logSource records which step produced the content. With no fallback configured this is upstream's
// scraper and stays at its log level.
func logSource(pageURL, source, content string) {
	if source == "" {
		source = "none"
	}
	level := slog.LevelInfo
	if len(config.Opts.FetchFallbackChain()) == 0 {
		level = slog.LevelDebug
	}
	slog.Log(context.Background(), level, "Fetch fallback chain",
		slog.String("entry_url", pageURL),
		slog.String("source", source),
		slog.Int("text_length", TextLength(content)),
	)
}
