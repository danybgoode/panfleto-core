// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package comments // import "miniflux.app/v2/internal/reader/comments"

import (
	"bytes"
	"html/template"
	"math"
	"time"

	"miniflux.app/v2/internal/locale"
	"miniflux.app/v2/internal/reader/sanitizer"
	"miniflux.app/v2/internal/timezone"
)

// sanitizeBody is the non-negotiable: comment bodies are strangers' HTML rendered inside a signed-in
// session, so every one goes through the reader's sanitizer before it is cached or rendered.
func sanitizeBody(commentsURL, body string) string {
	return sanitizer.SanitizeHTML(commentsURL, body, &sanitizer.SanitizerOptions{OpenLinksInNewTab: true})
}

// The fragment uses only classes the reader's stylesheets already have, because the CSP blocks inline
// styles (D6): replies nest as blockquotes inside .entry-content, which the themes already indent.
// Added collapse/expand functionality (ported from editorial-panfleto) with keyboard shortcuts.
var fragment = template.Must(template.New("comments").Parse(`
{{- define "comment" -}}
<blockquote class="entry-comment" id="comment-{{ .Index }}">
<div class="entry-comment-header">
<button class="collapse-toggle" onclick="toggleComment(this)" aria-expanded="true" aria-controls="comment-body-{{ .Index }}">[-]</button>
<p class="entry-comment-meta">
<strong>{{ .Comment.Author }}</strong> · 
<time datetime="{{ .Comment.Created.UTC.Format "2006-01-02T15:04:05Z" }}">{{ call .Elapsed .Comment.Created }}</time>
{{- if .Children }}<span class="entry-comment-reply-count">(+{{ len .Children }} replies)</span>{{ end }}
</p>
</div>
<div class="entry-comment-body" id="comment-body-{{ .Index }}">
{{ .Body }}
</div>
{{- if .Children }}
<div class="entry-comment-children">
{{- range .Children }}{{ template "comment" . }}{{ end }}
</div>
{{- end }}
</blockquote>
{{- end -}}
<div class="entry-content entry-comments-thread">
<div class="comments-controls">
<button class="page-button" onclick="expandAllComments()" title="Expand all comments">[Expand All]</button>
<button class="page-button" onclick="collapseAllComments()" title="Collapse all comments">[Collapse All]</button>
</div>
{{- range .Comments }}{{ template "comment" . }}{{ end }}
{{- if .Truncated }}
<p class="entry-comments-view-all"><a href="{{ .CommentsURL }}" target="_blank" rel="noopener noreferrer">{{ .ViewAll }}</a></p>
{{- end }}
</div>`))

var unavailable = template.Must(template.New("unavailable").Parse(
	`<p class="entry-comments-unavailable"><a href="{{ .CommentsURL }}" target="_blank" rel="noopener noreferrer">{{ .ViewAll }}</a></p>`))

type commentView struct {
	Comment  *Comment
	Body     template.HTML
	Children []commentView
	Elapsed  func(time.Time) string
	Index    int
}

// Render returns the thread as an HTML fragment in the reader's language and timezone.
func Render(thread *Thread, language, tz, commentsURL string) ([]byte, error) {
	printer := locale.NewPrinter(language)
	elapsed := func(t time.Time) string { return elapsedTime(printer, tz, t) }

	var commentIndex int
	var views func(comments []*Comment) []commentView
	views = func(comments []*Comment) []commentView {
		out := make([]commentView, 0, len(comments))
		for _, comment := range comments {
			commentIndex++
			out = append(out, commentView{
				Comment: comment,
				// Safe: Body was sanitized in buildThread, before the thread was cached.
				Body:     template.HTML(comment.Body),
				Children: views(comment.Children),
				Elapsed:  elapsed,
				Index:    commentIndex,
			})
		}
		return out
	}

	var buffer bytes.Buffer
	err := fragment.Execute(&buffer, map[string]any{
		"Comments":    views(thread.Comments),
		"Truncated":   thread.Truncated,
		"CommentsURL": commentsURL,
		"ViewAll":     printer.Print("entry.comments.title"),
	})
	return buffer.Bytes(), err
}

// RenderUnavailable is D3: when the source can't be reached, the panel offers the thread on its site.
func RenderUnavailable(language, commentsURL string) ([]byte, error) {
	var buffer bytes.Buffer
	err := unavailable.Execute(&buffer, map[string]any{
		"CommentsURL": commentsURL,
		"ViewAll":     locale.NewPrinter(language).Print("entry.comments.title"),
	})
	return buffer.Bytes(), err
}

// elapsedTime mirrors internal/template's unexported helper with the same translated keys; importing the
// template engine for one function would pull the whole view layer into a reader package.
func elapsedTime(printer *locale.Printer, tz string, t time.Time) string {
	current := timezone.Now(tz)
	t = timezone.Convert(tz, t)
	if t.IsZero() || current.Before(t) {
		return printer.Print("time_elapsed.not_yet")
	}

	seconds := current.Sub(t).Seconds()
	days := int(seconds / 86400)
	switch {
	case seconds < 60:
		return printer.Print("time_elapsed.now")
	case seconds < 3600:
		minutes := int(seconds / 60)
		return printer.Plural("time_elapsed.minutes", minutes, minutes)
	case seconds < 86400:
		hours := int(seconds / 3600)
		return printer.Plural("time_elapsed.hours", hours, hours)
	case days == 1:
		return printer.Print("time_elapsed.yesterday")
	case days < 21:
		return printer.Plural("time_elapsed.days", days, days)
	case days < 31:
		weeks := int(math.Round(float64(days) / 7))
		return printer.Plural("time_elapsed.weeks", weeks, weeks)
	case days < 365:
		months := int(math.Round(float64(days) / 30))
		return printer.Plural("time_elapsed.months", months, months)
	default:
		years := int(math.Round(float64(days) / 365))
		return printer.Plural("time_elapsed.years", years, years)
	}
}
