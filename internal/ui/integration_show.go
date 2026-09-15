// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package ui // import "miniflux.app/v2/internal/ui"

import (
	"log/slog"
	"net/http"

	"miniflux.app/v2/internal/http/request"
	"miniflux.app/v2/internal/http/response"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/ui/form"
	"miniflux.app/v2/internal/ui/view"
)

func (h *handler) showIntegrationPage(w http.ResponseWriter, r *http.Request) {
	h.renderIntegrationPage(w, r, false)
}

// revealMCPToken renders the same page with the credential in it. It is a POST because that is the
// only way to ask for the token without putting anything in a URL, and because it means the token is
// in the page's HTML only when somebody asked for it - not on every visit to Settings, where an
// extension, a screenshot or a shared screen would pick it up. (Story 1.1: "masked until revealed".)
func (h *handler) revealMCPToken(w http.ResponseWriter, r *http.Request) {
	h.renderIntegrationPage(w, r, true)
}

func (h *handler) renderIntegrationPage(w http.ResponseWriter, r *http.Request, revealToken bool) {
	user, err := h.store.UserByID(request.UserID(r))
	if err != nil {
		response.HTMLServerError(w, r, err)
		return
	}

	integration, err := h.store.Integration(user.ID)
	if err != nil {
		response.HTMLServerError(w, r, err)
		return
	}

	integrationForm := form.IntegrationForm{
		PinboardEnabled:                  integration.PinboardEnabled,
		PinboardToken:                    integration.PinboardToken,
		PinboardTags:                     integration.PinboardTags,
		PinboardMarkAsUnread:             integration.PinboardMarkAsUnread,
		InstapaperEnabled:                integration.InstapaperEnabled,
		InstapaperUsername:               integration.InstapaperUsername,
		InstapaperPassword:               integration.InstapaperPassword,
		FeverEnabled:                     integration.FeverEnabled,
		FeverUsername:                    integration.FeverUsername,
		GoogleReaderEnabled:              integration.GoogleReaderEnabled,
		GoogleReaderUsername:             integration.GoogleReaderUsername,
		WallabagEnabled:                  integration.WallabagEnabled,
		WallabagOnlyURL:                  integration.WallabagOnlyURL,
		WallabagURL:                      integration.WallabagURL,
		WallabagClientID:                 integration.WallabagClientID,
		WallabagClientSecret:             integration.WallabagClientSecret,
		WallabagUsername:                 integration.WallabagUsername,
		WallabagPassword:                 integration.WallabagPassword,
		WallabagTags:                     integration.WallabagTags,
		NotionEnabled:                    integration.NotionEnabled,
		NotionPageID:                     integration.NotionPageID,
		NotionToken:                      integration.NotionToken,
		NunuxKeeperEnabled:               integration.NunuxKeeperEnabled,
		NunuxKeeperURL:                   integration.NunuxKeeperURL,
		NunuxKeeperAPIKey:                integration.NunuxKeeperAPIKey,
		EspialEnabled:                    integration.EspialEnabled,
		EspialURL:                        integration.EspialURL,
		EspialAPIKey:                     integration.EspialAPIKey,
		EspialTags:                       integration.EspialTags,
		ReadwiseEnabled:                  integration.ReadwiseEnabled,
		ReadwiseAPIKey:                   integration.ReadwiseAPIKey,
		TelegramBotEnabled:               integration.TelegramBotEnabled,
		TelegramBotToken:                 integration.TelegramBotToken,
		TelegramBotChatID:                integration.TelegramBotChatID,
		TelegramBotTopicID:               integration.TelegramBotTopicID,
		TelegramBotDisableWebPagePreview: integration.TelegramBotDisableWebPagePreview,
		TelegramBotDisableNotification:   integration.TelegramBotDisableNotification,
		TelegramBotDisableButtons:        integration.TelegramBotDisableButtons,
		LinkAceEnabled:                   integration.LinkAceEnabled,
		LinkAceURL:                       integration.LinkAceURL,
		LinkAceAPIKey:                    integration.LinkAceAPIKey,
		LinkAceTags:                      integration.LinkAceTags,
		LinkAcePrivate:                   integration.LinkAcePrivate,
		LinkAceCheckDisabled:             integration.LinkAceCheckDisabled,
		LinkdingEnabled:                  integration.LinkdingEnabled,
		LinkdingURL:                      integration.LinkdingURL,
		LinkdingAPIKey:                   integration.LinkdingAPIKey,
		LinkdingTags:                     integration.LinkdingTags,
		LinkdingMarkAsUnread:             integration.LinkdingMarkAsUnread,
		LinktacoEnabled:                  integration.LinktacoEnabled,
		LinktacoAPIToken:                 integration.LinktacoAPIToken,
		LinktacoOrgSlug:                  integration.LinktacoOrgSlug,
		LinktacoTags:                     integration.LinktacoTags,
		LinktacoVisibility:               integration.LinktacoVisibility,
		LinkwardenEnabled:                integration.LinkwardenEnabled,
		LinkwardenURL:                    integration.LinkwardenURL,
		LinkwardenAPIKey:                 integration.LinkwardenAPIKey,
		LinkwardenCollectionID:           integration.LinkwardenCollectionID,
		MatrixBotEnabled:                 integration.MatrixBotEnabled,
		MatrixBotUser:                    integration.MatrixBotUser,
		MatrixBotPassword:                integration.MatrixBotPassword,
		MatrixBotURL:                     integration.MatrixBotURL,
		MatrixBotChatID:                  integration.MatrixBotChatID,
		AppriseEnabled:                   integration.AppriseEnabled,
		AppriseURL:                       integration.AppriseURL,
		AppriseServicesURL:               integration.AppriseServicesURL,
		ReadeckEnabled:                   integration.ReadeckEnabled,
		ReadeckPushEnabled:               integration.ReadeckPushEnabled,
		ReadeckURL:                       integration.ReadeckURL,
		ReadeckAPIKey:                    integration.ReadeckAPIKey,
		ReadeckLabels:                    integration.ReadeckLabels,
		ReadeckOnlyURL:                   integration.ReadeckOnlyURL,
		ShioriEnabled:                    integration.ShioriEnabled,
		ShioriURL:                        integration.ShioriURL,
		ShioriUsername:                   integration.ShioriUsername,
		ShioriPassword:                   integration.ShioriPassword,
		ShaarliEnabled:                   integration.ShaarliEnabled,
		ShaarliURL:                       integration.ShaarliURL,
		ShaarliAPISecret:                 integration.ShaarliAPISecret,
		WebhookEnabled:                   integration.WebhookEnabled,
		WebhookURL:                       integration.WebhookURL,
		WebhookSecret:                    integration.WebhookSecret,
		RSSBridgeEnabled:                 integration.RSSBridgeEnabled,
		RSSBridgeURL:                     integration.RSSBridgeURL,
		RSSBridgeToken:                   integration.RSSBridgeToken,
		OmnivoreEnabled:                  integration.OmnivoreEnabled,
		OmnivoreAPIKey:                   integration.OmnivoreAPIKey,
		OmnivoreURL:                      integration.OmnivoreURL,
		KarakeepEnabled:                  integration.KarakeepEnabled,
		KarakeepAPIKey:                   integration.KarakeepAPIKey,
		KarakeepURL:                      integration.KarakeepURL,
		KarakeepTags:                     integration.KarakeepTags,
		RaindropEnabled:                  integration.RaindropEnabled,
		RaindropToken:                    integration.RaindropToken,
		RaindropCollectionID:             integration.RaindropCollectionID,
		RaindropTags:                     integration.RaindropTags,
		BetulaEnabled:                    integration.BetulaEnabled,
		BetulaURL:                        integration.BetulaURL,
		BetulaToken:                      integration.BetulaToken,
		NtfyEnabled:                      integration.NtfyEnabled,
		NtfyTopic:                        integration.NtfyTopic,
		NtfyURL:                          integration.NtfyURL,
		NtfyAPIToken:                     integration.NtfyAPIToken,
		NtfyUsername:                     integration.NtfyUsername,
		NtfyPassword:                     integration.NtfyPassword,
		NtfyIconURL:                      integration.NtfyIconURL,
		NtfyInternalLinks:                integration.NtfyInternalLinks,
		CuboxEnabled:                     integration.CuboxEnabled,
		CuboxAPILink:                     integration.CuboxAPILink,
		DiscordEnabled:                   integration.DiscordEnabled,
		DiscordWebhookLink:               integration.DiscordWebhookLink,
		SlackEnabled:                     integration.SlackEnabled,
		SlackWebhookLink:                 integration.SlackWebhookLink,
		PushoverEnabled:                  integration.PushoverEnabled,
		PushoverUser:                     integration.PushoverUser,
		PushoverToken:                    integration.PushoverToken,
		PushoverDevice:                   integration.PushoverDevice,
		PushoverPrefix:                   integration.PushoverPrefix,
		ArchiveorgEnabled:                integration.ArchiveorgEnabled,
	}

	// --- PANFLETO MCP TOKEN ---
	// It used to be MINTED HERE, as a side effect of opening this page: a reader who once looked at
	// Settings had a live, long-lived credential on their account that they never asked for, could
	// not date, and could not replace. Now the page only reports what exists; creating a token is an
	// explicit act (see generateMCPToken below).
	// Roadmap/03-agent-surface/mcp-token-handling, D1.
	mcpKey := h.mcpAPIKey(user.ID)
	// ---------------------------

	view := view.New(h.tpl, r)
	view.Set("form", integrationForm)
	view.Set("menu", "settings")
	view.Set("user", user)
	if mcpKey != nil {
		// Its age and its last use always; the credential itself only when it was asked for. Miniflux's
		// own api_keys model carries all three (last_used_at is stamped on every authenticated API
		// call), so nothing new is stored.
		view.Set("mcpKeyExists", true)
		view.Set("mcpCreatedAt", mcpKey.CreatedAt)
		view.Set("mcpLastUsedAt", mcpKey.LastUsedAt)
		if revealToken {
			view.Set("mcpToken", mcpKey.Token)
		}
	}
	navMetadata, _ := h.store.GetNavMetadata(user.ID)
	view.Set("countUnread", navMetadata.CountUnread)
	view.Set("countErrorFeeds", navMetadata.CountErrorFeeds)

	response.HTML(w, r, view.Render("integrations"))
}

// mcpAPIKeyDescription is how the reader's own API-key list labels the MCP credential. It is the
// lookup key for every handler below, so it exists once.
const mcpAPIKeyDescription = "Panfleto MCP"

// mcpAPIKey returns the user's MCP key, or nil when they have none. A read error is reported as "no
// key" rather than as an error page: this drives a settings panel, not an authorization decision.
func (h *handler) mcpAPIKey(userID int64) *model.APIKey {
	apiKeys, err := h.store.APIKeys(userID)
	if err != nil {
		slog.Error("Unable to read API keys for the MCP panel", slog.Int64("user_id", userID), slog.Any("error", err))
		return nil
	}
	for i := range apiKeys {
		if apiKeys[i].Description == mcpAPIKeyDescription {
			return &apiKeys[i]
		}
	}
	return nil
}

// generateMCPToken mints the MCP credential on an explicit request. POST-only and CSRF-checked by the
// middleware, and idempotent: pressing it twice must not leave the user with two keys, one of which
// they can neither see nor revoke.
func (h *handler) generateMCPToken(w http.ResponseWriter, r *http.Request) {
	userID := request.UserID(r)

	if h.mcpAPIKey(userID) == nil {
		if _, err := h.store.CreateAPIKey(userID, mcpAPIKeyDescription); err != nil {
			// api_keys has `unique (user_id, description)`, so a double-click or a second tab loses
			// this race with a unique violation. The user asked for a token and now has exactly one,
			// which is what they wanted - a 500 would be us reporting our own race to them.
			if h.mcpAPIKey(userID) == nil {
				response.HTMLServerError(w, r, err)
				return
			}
		} else {
			// The token value itself is never logged (AGENTS.md rule 4) - only that one now exists.
			slog.Info("MCP token generated", slog.Int64("user_id", userID))
		}
	}

	response.HTMLRedirect(w, r, h.routePath("/integrations"))
}

// rotateMCPToken replaces the credential. D4: the old token dies IMMEDIATELY - deleted before the new
// one is created - because a token you rotate is a token you believe has leaked. The panel says so in
// plain words before the button, since a user whose assistant silently stops working will not connect
// the two events.
func (h *handler) rotateMCPToken(w http.ResponseWriter, r *http.Request) {
	userID := request.UserID(r)

	if existing := h.mcpAPIKey(userID); existing != nil {
		if err := h.store.DeleteAPIKey(userID, existing.ID); err != nil {
			response.HTMLServerError(w, r, err)
			return
		}
	}

	// Delete-then-create is forced by `unique (user_id, description)` - two rows with this description
	// cannot coexist - so there is a window where the user has no token at all. Retry once rather than
	// leaving them there; if it still fails they land back on the panel with its Generate button, which
	// is a recoverable state and a visible one.
	if _, err := h.store.CreateAPIKey(userID, mcpAPIKeyDescription); err != nil {
		slog.Warn("MCP token rotation could not create the replacement, retrying", slog.Int64("user_id", userID), slog.Any("error", err))
		if _, retryErr := h.store.CreateAPIKey(userID, mcpAPIKeyDescription); retryErr != nil {
			response.HTMLServerError(w, r, retryErr)
			return
		}
	}
	slog.Info("MCP token rotated", slog.Int64("user_id", userID))

	response.HTMLRedirect(w, r, h.routePath("/integrations"))
}
