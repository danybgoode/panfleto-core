// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
)

// The summary line is the entire point of Roadmap/02-onboarding-and-signup/onboarding-provisioning-reliability:
// before it, a partial provision was invisible. A count that is quietly wrong is the same bug wearing
// a different hat, so the arithmetic is pinned here.

func TestFormatOnboardingSummaryCountsAndNamesFailures(t *testing.T) {
	results := []feedResult{
		{URL: "https://a.example/feed"},
		{URL: "https://b.example/feed", Reason: "unable to fetch feed"},
		{URL: "https://c.example/feed"},
		{URL: "https://d.example/feed", Reason: "onboarding deadline reached"},
	}

	got := formatOnboardingSummary("reader@example.com", results)

	for _, want := range []string{"reader@example.com", "2/4", "2 failed", "https://b.example/feed", "https://d.example/feed"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "https://a.example/feed") || strings.Contains(got, "https://c.example/feed") {
		t.Errorf("summary names a feed that succeeded:\n%s", got)
	}
}

func TestFormatOnboardingSummaryAllAdded(t *testing.T) {
	results := []feedResult{{URL: "https://a.example/feed"}, {URL: "https://b.example/feed"}}

	got := formatOnboardingSummary("reader@example.com", results)

	if !strings.Contains(got, "all 2 starter feeds") {
		t.Errorf("a clean run should say so plainly:\n%s", got)
	}
	if strings.Contains(got, "failed") {
		t.Errorf("a clean run must not mention failures:\n%s", got)
	}
}

// The case nobody would notice by hand: feeds.json unreadable or empty means zero feeds were even
// attempted, which reads as "0/0 added" - technically true, and exactly the silence this epic removes.
func TestFormatOnboardingSummaryNoFeedsAttempted(t *testing.T) {
	got := formatOnboardingSummary("reader@example.com", nil)

	if !strings.Contains(got, "NO starter feeds were attempted") {
		t.Errorf("an empty starter list must be reported as an anomaly, not as success:\n%s", got)
	}
}

func TestEnvOrTrimsAndFallsBack(t *testing.T) {
	t.Setenv("PANFLETO_TEST_VALUE", "  configured  ")
	if got := envOr("PANFLETO_TEST_VALUE", "fallback"); got != "configured" {
		t.Errorf("want trimmed %q, got %q", "configured", got)
	}
	t.Setenv("PANFLETO_TEST_VALUE", "   ")
	if got := envOr("PANFLETO_TEST_VALUE", "fallback"); got != "fallback" {
		t.Errorf("a whitespace-only value must fall back, got %q", got)
	}
}

// AGENTS.md rule 4: the Telegram API URL carries the bot token in its path, and Go's *url.Error
// embeds the request URL in its message. Nothing may put that in a log line.
func TestScrubTokenRemovesTheCredential(t *testing.T) {
	token := "123456:AAHwSuperSecretBotToken"
	msg := `Post "https://api.telegram.org/bot` + token + `/sendMessage": dial tcp: i/o timeout`

	got := scrubToken(msg, token)

	if strings.Contains(got, token) {
		t.Fatalf("the bot token survived scrubbing: %s", got)
	}
	if !strings.Contains(got, "<redacted>") || !strings.Contains(got, "i/o timeout") {
		t.Errorf("scrubbing should redact the token and keep the diagnosis: %s", got)
	}
}
