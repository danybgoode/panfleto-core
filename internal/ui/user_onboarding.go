// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"miniflux.app/v2/internal/model"
	feedHandler "miniflux.app/v2/internal/reader/handler"
	"miniflux.app/v2/internal/storage"
)

type starterFeed struct {
	url      string
	category string
}

func provisionUserOnboarding(store *storage.Storage, userID int64, username string) {
	// 1. Starter Feeds
	feeds := []starterFeed{
		{url: "https://news.ycombinator.com/rss", category: "Tech"},
		{url: "https://www.theverge.com/rss/index.xml", category: "Tech"},
		{url: "https://wwwhatsnew.com/feed/", category: "Tech"},
		{url: "https://feeds.arstechnica.com/arstechnica/index", category: "Tech"},
		{url: "https://daringfireball.net/feeds/main", category: "Tech"},
		{url: "https://rss.nytimes.com/services/xml/rss/nyt/HomePage.xml", category: "News"},
		{url: "https://www.jornada.com.mx/rss/edicion.xml", category: "News"},
		{url: "https://feeds.elpais.com/mrss-s/pages/ep/site/elpais.com/section/mexico/portada", category: "News"},
		{url: "https://feeds.bbci.co.uk/news/rss.xml", category: "News"},
		{url: "https://www.ft.com/rss/home", category: "Business"},
		{url: "https://feeds.content.dowjones.io/public/rss/mw_topstories", category: "Business"},
		{url: "https://xkcd.com/rss.xml", category: "Comics"},
		{url: "https://www.newyorker.com/feed/everything", category: "Culture"},
	}

	categoryMap := make(map[string]int64)

	for _, f := range feeds {
		catID, ok := categoryMap[f.category]
		if !ok {
			// Check if it already exists
			cat, err := store.CategoryByTitle(userID, f.category)
			if err == nil && cat != nil {
				catID = cat.ID
			} else {
				newCat, err := store.CreateCategory(userID, &model.CategoryCreationRequest{Title: f.category})
				if err == nil && newCat != nil {
					catID = newCat.ID
				} else {
					slog.Error("Failed to create starter category", slog.String("category", f.category), slog.Any("error", err))
					continue
				}
			}
			categoryMap[f.category] = catID
		}

		// Add feed
		req := &model.FeedCreationRequest{
			FeedURL:    f.url,
			CategoryID: catID,
		}

		_, err := feedHandler.CreateFeed(store, userID, req)
		if err != nil {
			slog.Error("Failed to add starter feed", slog.String("url", f.url), slog.Any("error", err))
		}
	}

	// 2. Telegram Notification
	go sendTelegramNotification(username)

	// 3. Resend Welcome Email
	go sendWelcomeEmail(username)
}

func sendTelegramNotification(email string) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		slog.Warn("TELEGRAM_BOT_TOKEN not set, skipping notification")
		return
	}

	chatId := "1517743559"
	msg := fmt.Sprintf("🎉 YEEHAW! A new reader just joined Panfleto! 🎉\n\nEmail: %s\n\nKeep on pushing, Panflo! 🚀", email)

	payload := map[string]string{
		"chat_id": chatId,
		"text":    msg,
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		slog.Error("Failed to send Telegram notification", slog.Any("error", err))
		return
	}
	defer resp.Body.Close()
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
		"from":    "Panflo <hello@panfleto.win>",
		"to":      []string{email},
		"subject": "Welcome to Panfleto! 📰",
		"html":    htmlBody,
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("Failed to send Welcome email", slog.Any("error", err))
		return
	}
	defer resp.Body.Close()
}
