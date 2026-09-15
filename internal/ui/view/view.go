// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package view // import "miniflux.app/v2/internal/ui/view"

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/http/request"
	"miniflux.app/v2/internal/reader/prefetch"
	"miniflux.app/v2/internal/template"
	"miniflux.app/v2/internal/ui/static"
)

// view wraps template argument building.
type view struct {
	tpl    *template.Engine
	r      *http.Request
	params map[string]any
}

// Set adds a new template argument.
func (v *view) Set(param string, value any) *view {
	v.params[param] = value
	return v
}

// Render executes the template with arguments.
func (v *view) Render(template string) []byte {
	return v.tpl.Render(template+".html", v.params)
}

// New returns a new view with default parameters.
func New(tpl *template.Engine, r *http.Request) *view {
	webSession := request.WebSession(r)
	theme := webSession.Theme()
	flashSuccessMessage, flashErrorMessage := webSession.ConsumeMessages()
	return &view{tpl, r, map[string]any{
		"menu":                "",
		"csrf":                webSession.CSRF(),
		"flashSuccessMessage": flashSuccessMessage,
		"flashErrorMessage":   flashErrorMessage,
		"theme":               theme,
		"language":            webSession.Language(),
		"theme_checksum":      static.StylesheetBundles[theme+".css"].Checksum,
		"app_js_checksum":     static.JavascriptBundles["app.js"].Checksum,
		"sw_js_checksum":      static.JavascriptBundles["service-worker.js"].Checksum,
		"webAuthnEnabled":     config.Opts.WebAuthn(),
		"suggestedFeeds":      SuggestedFeeds(),
		"fetchState":          prefetch.State,
		"forceCrawler":        config.Opts.ForceCrawler(),
	}}
}

// SuggestedFeed is one entry of panfleto's feeds.json (internal/ui/static/bin/feeds.json): the single
// list behind the subscribe page's suggestions, new-account starter feeds, and the landing page signup.
type SuggestedFeed struct {
	URL      string `json:"url"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Starter  bool   `json:"starter"`
}

var suggestedFeeds = sync.OnceValue(func() []SuggestedFeed {
	var feeds []SuggestedFeed
	if err := json.Unmarshal(static.BinaryBundles["feeds.json"].Data, &feeds); err != nil {
		slog.Error("Unable to parse feeds.json", slog.Any("error", err))
	}
	return feeds
})

// SuggestedFeeds returns the parsed feeds.json.
func SuggestedFeeds() []SuggestedFeed {
	return suggestedFeeds()
}
