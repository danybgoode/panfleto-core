// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"miniflux.app/v2/internal/model"
	feedHandler "miniflux.app/v2/internal/reader/handler"
	"miniflux.app/v2/internal/storage"
	"miniflux.app/v2/internal/ui/view"
)

// Onboarding runs in a goroutine off the OAuth callback, so nothing here may block the user's sign-in
// and nothing here may run forever. See Roadmap/02-onboarding-and-signup/onboarding-provisioning-reliability.
const (
	// The whole starter-feed loop. 16 feeds; a feed that takes longer than this to subscribe is a
	// feed the new user is better off without. D1: this bounds the loop BETWEEN feeds -
	// feedHandler.CreateFeed takes no context, so one feed can still block for its own HTTP timeout.
	onboardingTimeout = 4 * time.Minute
	// Each notification, independently. They are reports, not the product.
	notificationTimeout = 20 * time.Second
)

// feedResult is one starter feed's outcome. Collected rather than discarded (the bug this epic
// exists to fix): the failures are what the product owner needs to hear about.
type feedResult struct {
	URL    string
	Reason string // empty means it was added
}

func (r feedResult) ok() bool { return r.Reason == "" }

func provisionUserOnboarding(store *storage.Storage, userID int64, username string) {
	ctx, cancel := context.WithTimeout(context.Background(), onboardingTimeout)
	defer cancel()

	started := time.Now()
	results := createStarterFeeds(ctx, store, userID)
	summary := formatOnboardingSummary(username, results)

	added := 0
	for _, r := range results {
		if r.ok() {
			added++
		}
	}
	slog.Info("Starter-feed onboarding finished",
		slog.String("username", username),
		slog.Int("added", added),
		slog.Int("attempted", len(results)),
		slog.Duration("took", time.Since(started)),
	)

	// Three independent failures, not one chain. The feeds are already committed to the database
	// before either of these runs; each notification carries its own deadline derived from
	// context.Background(), not from ctx, so a timed-out provisioning run still gets reported and
	// neither call can cancel the other. They are sequential rather than concurrent - nobody is
	// waiting on this goroutine, and a hung Telegram delays the email by at most notificationTimeout.
	sendTelegramNotification(summary)
	sendWelcomeEmail(username)
}

// createStarterFeeds subscribes the user to every starter feed in feeds.json, collecting one result
// per feed. It never returns early on a feed error - a partial list beats an empty one (D2).
func createStarterFeeds(ctx context.Context, store *storage.Storage, userID int64) []feedResult {
	categoryMap := make(map[string]int64)
	results := make([]feedResult, 0, 16)

	for _, f := range view.SuggestedFeeds() {
		if !f.Starter {
			continue
		}

		if err := ctx.Err(); err != nil {
			results = append(results, feedResult{URL: f.URL, Reason: "onboarding deadline reached"})
			continue
		}

		catID, ok := categoryMap[f.Category]
		if !ok {
			// Check if it already exists
			cat, err := store.CategoryByTitle(userID, f.Category)
			if err == nil && cat != nil {
				catID = cat.ID
			} else {
				newCat, err := store.CreateCategory(userID, &model.CategoryCreationRequest{Title: f.Category})
				if err == nil && newCat != nil {
					catID = newCat.ID
				} else {
					slog.Error("Failed to create starter category", slog.String("category", f.Category), slog.Any("error", err))
					results = append(results, feedResult{URL: f.URL, Reason: "category " + f.Category + " could not be created"})
					continue
				}
			}
			categoryMap[f.Category] = catID
		}

		// Add feed
		req := &model.FeedCreationRequest{
			FeedURL:    f.URL,
			CategoryID: catID,
		}

		// CreateFeed returns a *locale.LocalizedErrorWrapper, whose Error() yields the underlying error.
		if _, localizedErr := feedHandler.CreateFeed(store, userID, req); localizedErr != nil {
			reason := "unknown error"
			if err := localizedErr.Error(); err != nil {
				reason = err.Error()
			}
			slog.Error("Failed to add starter feed", slog.String("url", f.URL), slog.String("error", reason))
			results = append(results, feedResult{URL: f.URL, Reason: reason})
			continue
		}
		results = append(results, feedResult{URL: f.URL})
	}

	return results
}

// formatOnboardingSummary turns the per-feed results into the one line the product owner reads. It is
// pure so it can be tested, because "11/13 feeds added" being quietly wrong is the failure mode this
// whole epic is about. Failing feed URLs are listed - they are public RSS addresses, not credentials.
func formatOnboardingSummary(username string, results []feedResult) string {
	failed := make([]string, 0, len(results))
	for _, r := range results {
		if !r.ok() {
			failed = append(failed, r.URL)
		}
	}
	added := len(results) - len(failed)

	switch {
	case len(results) == 0:
		return fmt.Sprintf("⚠️ New reader %s joined Panfleto, but NO starter feeds were attempted — feeds.json looks empty.", username)
	case len(failed) == 0:
		return fmt.Sprintf("🎉 New reader %s joined Panfleto — all %d starter feeds added.", username, added)
	default:
		return fmt.Sprintf("⚠️ New reader %s joined Panfleto — %d/%d starter feeds added, %d failed:\n%s",
			username, added, len(results), len(failed), strings.Join(failed, "\n"))
	}
}

func sendTelegramNotification(message string) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		slog.Warn("TELEGRAM_BOT_TOKEN not set, skipping notification")
		return
	}

	// Was hardcoded. Defaults to the chat the product owner already receives signup pings on.
	chatID := envOr("PANFLETO_TELEGRAM_CHAT_ID", "1517743559")

	payload := map[string]string{
		"chat_id": chatID,
		"text":    message,
	}

	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), notificationTimeout)
	defer cancel()

	// The URL carries the bot token, so it must never reach a log line (AGENTS.md rule 4).
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		slog.Error("Failed to build Telegram notification request")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("Failed to send Telegram notification", slog.String("error", scrubToken(err.Error(), token)))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		slog.Error("Telegram rejected the notification", slog.Int("status", resp.StatusCode))
	}
}

func sendWelcomeEmail(email string) {
	apiKey := os.Getenv("RESEND_API_KEY")
	if apiKey == "" {
		slog.Warn("RESEND_API_KEY not set, skipping welcome email")
		return
	}

	htmlBody := `
        <div style="font-family: sans-serif; max-w-xl; margin: 0 auto; color: #333;">
        <img src="https://panfleto.win/panflo.png" alt="Panflo Mascot" style="width: 120px; height: auto; margin-bottom: 20px; display: block;" />
        <h1 style="color: #111;">Welcome to Panfleto! 🎉</h1>
        <p>Hello there,</p>
        <p>Thank you for signing up! Your account is ready, and we've pre-loaded some starter feeds to get you going.</p>
        <p>You can check out your new feeds right away at: <a href="https://app.panfleto.win/feeds" style="color: #3b82f6; text-decoration: none; font-weight: bold;">app.panfleto.win/feeds</a></p>
        <br/>
        <p>Panfleto is built differently. Here, you get to enjoy your reading <strong>100% free of ads, tracking, and manipulative algorithms</strong>. Just pure, chronological feeds.</p>
        
        <div style="background-color: #f9f9f9; padding: 15px; border-radius: 8px; margin: 20px 0; border: 1px solid #eaeaea;">
          <h3 style="margin-top: 0;">⚡ Quick Cheat Sheet</h3>
          <ul style="margin-bottom: 0; padding-left: 20px; line-height: 1.6;">
            <li><strong>Spacebar:</strong> Scroll down (and to next article)</li>
            <li><strong>Enter / o:</strong> Open focused article</li>
            <li><strong>m:</strong> Toggle read/unread</li>
            <li><strong>v:</strong> Open original site</li>
            <li><strong>? :</strong> View all shortcuts!</li>
          </ul>
        </div>
        
        <p><strong>A quick favor:</strong></p>
        <p>Panfleto is run entirely out of pocket by a single developer. If you enjoy the distraction-free experience, please consider chipping in to keep the servers running and the project ad-free.</p>
        <p>You can find the "Save Panflo" options at the bottom of any article or simply via our <a href="https://buymeacoffee.com/savepanflo" style="color: #BD5FFF; font-weight: bold; text-decoration: none;">Buy Me A Coffee</a>.</p>
        <br/>
        <p>Happy reading!</p>
        <p><em>— Panflo</em></p>
      </div>
    `

	payload := map[string]interface{}{
		"from":    envOr("PANFLETO_EMAIL_FROM", "Panflo <hello@panfleto.win>"), // was hardcoded
		"to":      []string{email},
		"subject": "Welcome to Panfleto! 📰",
		"html":    htmlBody,
	}

	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), notificationTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		slog.Error("Failed to build welcome email request")
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("Failed to send Welcome email", slog.String("error", scrubToken(err.Error(), apiKey)))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		slog.Error("Resend rejected the welcome email", slog.Int("status", resp.StatusCode))
	}
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

// scrubToken keeps a credential out of a log line. Go's *url.Error embeds the request URL, and the
// Telegram URL carries the bot token in its path (AGENTS.md rule 4).
func scrubToken(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "<redacted>")
}
