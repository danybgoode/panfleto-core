// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package ui // import "miniflux.app/v2/internal/ui"

import (
	"net/http"

	"miniflux.app/v2/internal/http/request"
	"miniflux.app/v2/internal/http/response"
	"miniflux.app/v2/internal/reader/comments"
)

// panfleto: inline comments. The entry page lazy-loads this fragment on first expand
// (Roadmap/01-reading-experience/inline-comments).
func (h *handler) showEntryComments(w http.ResponseWriter, r *http.Request) {
	loggedUserID := request.UserID(r)
	entryID := request.RouteInt64Param(r, "entryID")

	entry, err := h.store.NewEntryQueryBuilder(loggedUserID).WithEntryIDs(entryID).WithoutContent().GetEntry()
	if err != nil {
		response.HTMLServerError(w, r, err)
		return
	}
	if entry == nil || !comments.Supported(entry.CommentsURL) {
		response.HTMLNotFound(w, r)
		return
	}

	user, err := h.store.UserByID(loggedUserID)
	if err != nil {
		response.HTMLServerError(w, r, err)
		return
	}

	thread, loadErr := comments.Load(entry.CommentsURL)
	if loadErr != nil {
		fragment, err := comments.RenderUnavailable(user.Language, entry.CommentsURL)
		if err != nil {
			response.HTMLServerError(w, r, err)
			return
		}
		setFragmentHeaders(w)
		w.WriteHeader(http.StatusBadGateway)
		w.Write(fragment)
		return
	}

	fragment, err := comments.Render(thread, user.Language, user.Timezone, entry.CommentsURL)
	if err != nil {
		response.HTMLServerError(w, r, err)
		return
	}
	setFragmentHeaders(w)
	w.WriteHeader(http.StatusOK)
	w.Write(fragment)
}

// setFragmentHeaders: the fragment carries strangers' markup and has no layout, so no page CSP. Opened
// directly, it gets the sandboxing policy Miniflux uses for untrusted content.
func setFragmentHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", response.ContentSecurityPolicyForUntrustedContent)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate, no-store")
}
