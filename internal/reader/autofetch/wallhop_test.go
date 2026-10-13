// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package autofetch

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWallhopRequestURL(t *testing.T) {
	got, ok := wallhopRequestURL("https://example.com/article?part=1&lang=es")
	if !ok || got != "https://wallhop.io/api/https://example.com/article?part=1&lang=es" {
		t.Fatalf("request URL = %q, %v", got, ok)
	}
	for _, invalid := range []string{"", "javascript:alert(1)", "https://user:secret@example.com/a", "//example.com/a", "http://localhost/a", "http://127.0.0.1/a", "http://192.168.1.1/a", "https://example.com/a?access_token=secret"} {
		if _, ok := wallhopRequestURL(invalid); ok {
			t.Errorf("accepted %q", invalid)
		}
	}
}

func TestWallhopExtractsItsJSONBodyAsAnArticle(t *testing.T) {
	page := `{"body":"<html><head><title>Example</title></head><body><nav>Menu</nav><article><h1>Story</h1><p>` + strings.Repeat("Reported facts and context. ", 35) + `</p></article></body></html>"}`
	base, content := extractWallhopBody("https://example.com/story", []byte(page))
	if base == "" || !strings.Contains(content, "Reported facts and context") || strings.Contains(content, "Menu") {
		t.Fatalf("bad extracted article: %q, %q", base, content)
	}
	if _, content := extractWallhopBody("https://example.com/story", []byte(`{"body":"<p>verify you are human</p>"}`)); content != "" {
		t.Fatalf("challenge accepted as article: %q", content)
	}
}

func TestWallhopCachesAHitAndCoolsDownOnRateLimit(t *testing.T) {
	setConfig(t, map[string]string{"FETCH_FALLBACK_CHAIN": "wallhop"})
	oldExecute, oldState, oldNow, oldSleep := wallhopExecute, wallhopState, wallhopNow, wallhopSleep
	t.Cleanup(func() {
		wallhopExecute, wallhopState, wallhopNow, wallhopSleep = oldExecute, oldState, oldNow, oldSleep
	})
	now := time.Now()
	wallhopNow = func() time.Time { return now }
	wallhopSleep = func(time.Duration) {}
	wallhopState = &wallhopGuard{results: make(map[string]wallhopResult)}
	hits := 0
	wallhopExecute = func(string) (*http.Response, error) {
		hits++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"60"}}, Body: io.NopCloser(strings.NewReader("slow down"))}, nil
	}
	fetchFromWallhop("https://example.com/one")
	fetchFromWallhop("https://example.com/two")
	if hits != 1 {
		t.Fatalf("called during rate-limit cooldown %d times", hits)
	}
}
