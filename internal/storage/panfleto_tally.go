// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package storage // import "miniflux.app/v2/internal/storage"

// panfleto: read-only queries behind tally's members view (internal/http/server/panfleto_tally.go).

import (
	"fmt"
	"strings"
	"time"
)

// TallyMember is one reader in tally's members list.
type TallyMember struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Role        string     `json:"role"`
	Language    string     `json:"language"`
	LastLoginAt *time.Time `json:"last_seen"`
	Feeds       int64      `json:"feeds"`
	Reads       int64      `json:"reads"`
	Starred     int64      `json:"starred"`
}

var tallyMemberSort = map[string]string{
	"id": "id", "username": "username", "role": "role", "language": "language",
	"last_seen": "last_login_at", "feeds": "feeds", "reads": "reads", "starred": "starred",
}

// TallyMembers returns one page of readers, sorted by a whitelisted column, and the total matching q.
func (s *Storage) TallyMembers(q, sort, dir string, limit, offset int) ([]TallyMember, int64, error) {
	col, ok := tallyMemberSort[sort]
	if !ok {
		col = "last_login_at"
	}
	order := "DESC"
	if strings.EqualFold(dir, "asc") {
		order = "ASC"
	}
	var total int64
	if err := s.db.QueryRow(`SELECT count(*) FROM users WHERE $1 = '' OR username ILIKE '%' || $1 || '%'`, q).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf(`store: unable to count tally members: %v`, err)
	}
	rows, err := s.db.Query(fmt.Sprintf(`
		SELECT * FROM (
			SELECT u.id, u.username, CASE WHEN u.is_admin THEN 'admin' ELSE 'reader' END AS role,
			       coalesce(u.language, '') AS language, u.last_login_at,
			       (SELECT count(*) FROM feeds f WHERE f.user_id = u.id) AS feeds,
			       (SELECT count(*) FROM entries e WHERE e.user_id = u.id AND e.status = 'read') AS reads,
			       (SELECT count(*) FROM entries e WHERE e.user_id = u.id AND e.starred) AS starred
			FROM users u
			WHERE $1 = '' OR u.username ILIKE '%%' || $1 || '%%'
		) m
		ORDER BY %s %s NULLS LAST, id
		LIMIT $2 OFFSET $3`, col, order), q, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf(`store: unable to list tally members: %v`, err)
	}
	defer rows.Close()
	members := []TallyMember{}
	for rows.Next() {
		var m TallyMember
		if err := rows.Scan(&m.ID, &m.Username, &m.Role, &m.Language, &m.LastLoginAt, &m.Feeds, &m.Reads, &m.Starred); err != nil {
			return nil, 0, fmt.Errorf(`store: unable to read tally member: %v`, err)
		}
		members = append(members, m)
	}
	return members, total, rows.Err()
}

// TallySummary is the headline numbers for tally's Product tab.
type TallySummary struct {
	Users, Admins, ActiveUsers, Feeds, DistinctFeeds, FailingFeeds, ReadsInRange, StarredTotal, SignIns int64
	ReadsDaily                                                                                          []TallyDay
}

// TallyDay is one day of a daily series.
type TallyDay struct {
	Day   string `json:"d"`
	Count int64  `json:"n"`
}

// TallySummary computes the summary for the last `days` days.
func (s *Storage) TallySummary(days int) (*TallySummary, error) {
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	r := &TallySummary{}
	err := s.db.QueryRow(`
		SELECT (SELECT count(*) FROM users),
		       (SELECT count(*) FROM users WHERE is_admin),
		       (SELECT count(*) FROM users WHERE last_login_at >= $1),
		       (SELECT count(*) FROM feeds),
		       (SELECT count(DISTINCT feed_url) FROM feeds),
		       (SELECT count(*) FROM feeds WHERE parsing_error_count > 0),
		       (SELECT count(*) FROM entries WHERE status = 'read' AND changed_at >= $1),
		       (SELECT count(*) FROM entries WHERE starred),
		       (SELECT count(*) FROM web_sessions WHERE user_id IS NOT NULL AND created_at >= $1)`, since).
		Scan(&r.Users, &r.Admins, &r.ActiveUsers, &r.Feeds, &r.DistinctFeeds, &r.FailingFeeds, &r.ReadsInRange, &r.StarredTotal, &r.SignIns)
	if err != nil {
		return nil, fmt.Errorf(`store: unable to compute tally summary: %v`, err)
	}
	rows, err := s.db.Query(`
		SELECT to_char(date_trunc('day', changed_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD'), count(*)
		FROM entries WHERE status = 'read' AND changed_at >= $1 GROUP BY 1 ORDER BY 1`, since)
	if err != nil {
		return nil, fmt.Errorf(`store: unable to compute tally daily reads: %v`, err)
	}
	defer rows.Close()
	r.ReadsDaily = []TallyDay{}
	for rows.Next() {
		var d TallyDay
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			return nil, err
		}
		r.ReadsDaily = append(r.ReadsDaily, d)
	}
	return r, rows.Err()
}

// TallySignIn is one web session (a sign-in) for tally's log. No IP address leaves the database.
type TallySignIn struct {
	At        time.Time `json:"at"`
	Username  string    `json:"who"`
	UserAgent string    `json:"-"`
}

// TallySignIns returns one page of sign-ins, newest first, optionally filtered by username.
func (s *Storage) TallySignIns(q string, limit, offset int) ([]TallySignIn, int64, error) {
	var total int64
	if err := s.db.QueryRow(`
		SELECT count(*) FROM web_sessions w JOIN users u ON u.id = w.user_id
		WHERE $1 = '' OR u.username ILIKE '%' || $1 || '%'`, q).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf(`store: unable to count tally sign-ins: %v`, err)
	}
	rows, err := s.db.Query(`
		SELECT w.created_at, u.username, w.user_agent FROM web_sessions w JOIN users u ON u.id = w.user_id
		WHERE $1 = '' OR u.username ILIKE '%' || $1 || '%'
		ORDER BY w.created_at DESC LIMIT $2 OFFSET $3`, q, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf(`store: unable to list tally sign-ins: %v`, err)
	}
	defer rows.Close()
	out := []TallySignIn{}
	for rows.Next() {
		var x TallySignIn
		if err := rows.Scan(&x.At, &x.Username, &x.UserAgent); err != nil {
			return nil, 0, err
		}
		out = append(out, x)
	}
	return out, total, rows.Err()
}
