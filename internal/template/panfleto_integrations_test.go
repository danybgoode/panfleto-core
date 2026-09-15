// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package template // import "miniflux.app/v2/internal/template"

import (
	"strings"
	"testing"
	"time"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/ui/form"
)

// panfleto's MCP panel on /integrations, in its three states.
//
// Two things here are worth a test rather than a screenshot. First, the panel used to carry four CSP
// violations — inline `style=` attributes and an `onclick` — under a policy of
// `style-src 'nonce-…'; script-src 'nonce-…'`, where neither can ever be allowed by a nonce. They
// were rebuilt away; this asserts they stay away, including through an upstream rebase that touches
// the layout (Roadmap/03-agent-surface/mcp-token-handling, story 1.1).
//
// Second, the panel is behind a session, so an anonymous smoke test cannot see it: a template that
// panics here is a 500 on a settings page that nobody notices until a signed-in user hits it.
func TestPanfletoMCPPanelRendersWithoutInlineStyleOrScript(t *testing.T) {
	config.Opts = config.NewConfigOptions()
	engine := NewEngine("")
	engine.ParseTemplates()

	createdAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	lastUsedAt := time.Date(2026, 9, 15, 9, 30, 0, 0, time.UTC)
	const token = "TOKENVALUEFORTHISTESTONLY"

	cases := map[string]struct {
		extra      map[string]any
		mustHave   []string
		mustNotHav []string
	}{
		"a user with no token is offered one, and shown none": {
			extra:      map[string]any{},
			mustHave:   []string{"Generate MCP token", "/integration/mcp/generate"},
			mustNotHav: []string{"/api/mcp?token=", "Rotate now"},
		},
		"an ordinary visit shows the token's age and use, but NOT the token": {
			// The credential must not be in the page's HTML just because Settings was opened - an
			// extension, a screenshot or a shared screen would pick it up.
			extra:      map[string]any{"mcpKeyExists": true, "mcpCreatedAt": createdAt, "mcpLastUsedAt": &lastUsedAt},
			mustHave:   []string{"Last used", "Rotate now", "/integration/mcp/rotate", "revokes the current one immediately", "/integration/mcp/reveal"},
			mustNotHav: []string{token, "Generate MCP token", "Never used"},
		},
		"revealing it, and only then, puts the URL in the page": {
			extra:      map[string]any{"mcpKeyExists": true, "mcpToken": token, "mcpCreatedAt": createdAt, "mcpLastUsedAt": &lastUsedAt},
			mustHave:   []string{token, "Authorization: Bearer", "hidden again"},
			mustNotHav: []string{"Generate MCP token"},
		},
		"a token that has never been used says so, rather than showing a blank": {
			extra:      map[string]any{"mcpKeyExists": true, "mcpCreatedAt": createdAt, "mcpLastUsedAt": (*time.Time)(nil)},
			mustHave:   []string{"Never used"},
			mustNotHav: []string{"Generate MCP token"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			data := map[string]any{
				"user":            &model.User{ID: 1, Username: "reader", Language: "en_US", Timezone: "UTC", Theme: "system_serif"},
				"language":        "en_US",
				"theme":           "system_serif",
				"menu":            "settings",
				"csrf":            "csrf-token",
				"cspNonce":        "nonce",
				"countUnread":     0,
				"countErrorFeeds": 0,
				"form":            form.IntegrationForm{},
			}
			for k, v := range tc.extra {
				data[k] = v
			}

			out := string(engine.Render("integrations.html", data))

			// The CSP assertion. `style=` and `onclick` cannot be nonce-allowed, so either one is a
			// console error on every visit to this page.
			if strings.Contains(out, "style=") {
				t.Error("an inline style attribute is back; it cannot be allowed by a nonce")
			}
			if strings.Contains(out, "onclick") {
				t.Error("an inline event handler is back; it cannot be allowed by a nonce")
			}
			// Rotation destroys a credential, so it must never be reachable by a prefetchable GET.
			if strings.Contains(out, `href="`+"/integration/mcp") {
				t.Error("an MCP action is exposed as a link; it must be a POST form")
			}

			for _, want := range tc.mustHave {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, unwanted := range tc.mustNotHav {
				if strings.Contains(out, unwanted) {
					t.Errorf("unexpectedly present: %q", unwanted)
				}
			}
		})
	}
}
