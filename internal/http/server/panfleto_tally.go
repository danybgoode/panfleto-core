// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server // import "miniflux.app/v2/internal/http/server"

// panfleto: GET /tally/{summary|members|log} - the owner's members view in tally (tally.goldenfrijoles.com).
// Registered only when TALLY_CONNECTOR_KEY is set; every request without that exact bearer key is a 404.
// Read-only. tally calls it server-side over the VM's edge network and stores nothing.

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/crypto"
	"miniflux.app/v2/internal/storage"
)

type tallyCol struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type,omitempty"`
	Sort  bool   `json:"sort,omitempty"`
}

func tallyAuthorized(r *http.Request) bool {
	key := config.Opts.TallyConnectorKey()
	given, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && len(key) >= 32 && crypto.ConstantTimeCmp(strings.TrimSpace(given), key)
}

func tallyInt(r *http.Request, name string, def, lo, hi int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		n = def
	}
	return max(lo, min(hi, n))
}

func tallyDevice(ua string) string {
	switch {
	case ua == "":
		return "—"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "Android"):
		return "phone"
	case strings.Contains(ua, "iPad"):
		return "tablet"
	default:
		return "desktop"
	}
}

func tallyHandler(store *storage.Storage) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !tallyAuthorized(r) {
			http.NotFound(w, r)
			return
		}
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		size := tallyInt(r, "size", 25, 5, 200)
		offset := (tallyInt(r, "page", 1, 1, 100000) - 1) * size
		var body any
		var err error
		switch r.PathValue("what") {
		case "members":
			var rows []storage.TallyMember
			var total int64
			rows, total, err = store.TallyMembers(q, r.URL.Query().Get("sort"), r.URL.Query().Get("dir"), size, offset)
			body = map[string]any{"total": total, "rows": rows, "columns": []tallyCol{
				{"id", "No.", "number", true}, {"username", "Reader", "", true}, {"role", "Role", "", true},
				{"language", "Language", "", true}, {"feeds", "Feeds", "number", true}, {"reads", "Articles read", "number", true},
				{"starred", "Starred", "number", true}, {"last_seen", "Last sign-in", "date", true},
			}}
		case "summary":
			var s *storage.TallySummary
			s, err = store.TallySummary(tallyInt(r, "days", 30, 1, 400))
			if err == nil {
				body = map[string]any{
					"tiles": []map[string]any{
						{"label": "Readers", "value": s.Users, "sub": strconv.FormatInt(s.Admins, 10) + " admins"},
						{"label": "Signed in, in range", "value": s.ActiveUsers, "sub": strconv.FormatInt(s.SignIns, 10) + " sign-ins"},
						{"label": "Articles read, in range", "value": s.ReadsInRange, "sub": "marked read"},
						{"label": "Subscriptions", "value": s.Feeds, "sub": strconv.FormatInt(s.DistinctFeeds, 10) + " distinct feeds"},
						{"label": "Failing feeds", "value": s.FailingFeeds, "sub": "with a parsing error"},
						{"label": "Starred", "value": s.StarredTotal, "sub": "all time"},
					},
					"sections": []map[string]any{
						{"title": "Readers", "kind": "kv", "rows": [][]any{{"Total", s.Users}, {"Admins", s.Admins}, {"Signed in, in range", s.ActiveUsers}, {"Sign-ins in range", s.SignIns}}},
						{"title": "Feeds", "kind": "kv", "rows": [][]any{{"Subscriptions", s.Feeds}, {"Distinct feeds", s.DistinctFeeds}, {"Failing", s.FailingFeeds}}},
					},
					"daily": s.ReadsDaily, "daily_label": "Articles read per day",
				}
			}
		case "log":
			var rows []storage.TallySignIn
			var total int64
			rows, total, err = store.TallySignIns(q, size, offset)
			out := make([]map[string]any, 0, len(rows))
			for _, x := range rows {
				out = append(out, map[string]any{"at": x.At, "name": "sign-in", "who": x.Username, "device": tallyDevice(x.UserAgent)})
			}
			body = map[string]any{"total": total, "rows": out, "columns": []tallyCol{
				{"at", "When", "date", false}, {"name", "Event", "", false}, {"who", "Reader", "", false}, {"device", "Device", "", false},
			}}
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			slog.Error("tally connector failed", slog.Any("error", err))
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, no-store")
		_ = json.NewEncoder(w).Encode(body)
	})
}
