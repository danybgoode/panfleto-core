// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package api // import "miniflux.app/v2/internal/api"

import (
	"net/http"
	"time"

	"miniflux.app/v2/internal/http/request"
	"miniflux.app/v2/internal/http/response"
	"miniflux.app/v2/internal/reader/comments"
)

// CommentJSON represents a comment in JSON format for the API.
type CommentJSON struct {
	ID        int64      `json:"id"`
	Author    string     `json:"author"`
	CreatedAt time.Time  `json:"created_at"`
	Body      string     `json:"text"`
	Children  []CommentJSON `json:"children,omitempty"`
}

// CommentsThreadJSON represents a comments thread in JSON format for the API.
type CommentsThreadJSON struct {
	CommentsURL string    `json:"comments_url"`
	TotalCount  int       `json:"total_comments"`
	Truncated   bool      `json:"truncated"`
	Comments    []CommentJSON `json:"comments"`
}

// convertCommentToJSON converts a comments.Comment to CommentJSON.
func convertCommentToJSON(c *comments.Comment, index *int) CommentJSON {
	(*index)++
	return CommentJSON{
		ID:        int64(*index),
		Author:    c.Author,
		CreatedAt: c.Created,
		Body:      c.Body,
		Children:  convertCommentsToJSON(c.Children, index),
	}
}

// convertCommentsToJSON converts a slice of comments.Comment to []CommentJSON.
func convertCommentsToJSON(comments []*comments.Comment, index *int) []CommentJSON {
	result := make([]CommentJSON, 0, len(comments))
	for _, c := range comments {
		result = append(result, convertCommentToJSON(c, index))
	}
	return result
}

// getCommentsByEntryID returns comments for a specific entry as JSON.
func (h *handler) getCommentsByEntryID(w http.ResponseWriter, r *http.Request) {
	loggedUserID := request.UserID(r)
	entryID := request.RouteInt64Param(r, "entryID")

	// Get the entry to find its CommentsURL
	entry, err := h.store.NewEntryQueryBuilder(loggedUserID).WithEntryIDs(entryID).WithoutContent().GetEntry()
	if err != nil {
		response.JSONServerError(w, r, err)
		return
	}
	if entry == nil || !comments.Supported(entry.CommentsURL) {
		response.JSONNotFound(w, r)
		return
	}

	thread, loadErr := comments.Load(entry.CommentsURL)
	if loadErr != nil {
		http.Error(w, loadErr.Error(), http.StatusBadGateway)
		return
	}

	var index int
	commentIndex := &index
	commentsJSON := convertCommentsToJSON(thread.Comments, commentIndex)

	threadJSON := CommentsThreadJSON{
		CommentsURL: entry.CommentsURL,
		TotalCount:  thread.Count,
		Truncated:   thread.Truncated,
		Comments:    commentsJSON,
	}

	w.Header().Set("Cache-Control", "public, max-age=600")
	response.JSON(w, r, threadJSON)
}

// getCommentsByURL returns comments for a given comments URL as JSON.
// This allows external clients (like editorial-panfleto) to fetch comments directly.
// Note: This endpoint is public and does not require authentication for public comment sources.
func (h *handler) getCommentsByURL(w http.ResponseWriter, r *http.Request) {
	commentsURL := r.URL.Query().Get("url")
	if commentsURL == "" {
		http.Error(w, "missing url parameter", http.StatusBadRequest)
		return
	}

	if !comments.Supported(commentsURL) {
		http.Error(w, "unsupported comments URL", http.StatusBadRequest)
		return
	}

	thread, loadErr := comments.Load(commentsURL)
	if loadErr != nil {
		http.Error(w, loadErr.Error(), http.StatusBadGateway)
		return
	}

	var index int
	commentIndex := &index
	commentsJSON := convertCommentsToJSON(thread.Comments, commentIndex)

	threadJSON := CommentsThreadJSON{
		CommentsURL: commentsURL,
		TotalCount:  thread.Count,
		Truncated:   thread.Truncated,
		Comments:    commentsJSON,
	}

	w.Header().Set("Cache-Control", "public, max-age=600") // 10 minutes cache to match comments cache
	response.JSON(w, r, threadJSON)
}
