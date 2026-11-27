// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server // import "miniflux.app/v2/internal/http/server"

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"miniflux.app/v2/internal/config"
)

// panfleto: the tally route answers only the exact bearer key, and never when the key is unset or short.
func TestTallyAuthorized(t *testing.T) {
	previous := config.Opts
	t.Cleanup(func() { config.Opts = previous })
	key := "0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		env, header string
		want        bool
	}{
		{"", "Bearer ", false},
		{"", "Bearer " + key, false},
		{"short", "Bearer short", false},
		{key, "", false},
		{key, "Bearer wrong", false},
		{key, "Basic " + key, false},
		{key, "Bearer " + key, true},
	} {
		os.Clearenv()
		if tc.env != "" {
			os.Setenv("TALLY_CONNECTOR_KEY", tc.env)
		}
		opts, err := config.NewConfigParser().ParseEnvironmentVariables()
		if err != nil {
			t.Fatal(err)
		}
		config.Opts = opts
		r := httptest.NewRequest(http.MethodGet, "/tally/members", nil)
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}
		if got := tallyAuthorized(r); got != tc.want {
			t.Errorf("env=%q header=%q: got %v, want %v", tc.env, tc.header, got, tc.want)
		}
	}
}

func TestTallyUnauthorizedIs404(t *testing.T) {
	previous := config.Opts
	t.Cleanup(func() { config.Opts = previous })
	os.Clearenv()
	os.Setenv("TALLY_CONNECTOR_KEY", "0123456789abcdef0123456789abcdef")
	opts, err := config.NewConfigParser().ParseEnvironmentVariables()
	if err != nil {
		t.Fatal(err)
	}
	config.Opts = opts
	w := httptest.NewRecorder()
	tallyHandler(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tally/members", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestTallyDevice(t *testing.T) {
	if tallyDevice("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0)") != "phone" || tallyDevice("Mozilla/5.0 (Macintosh)") != "desktop" || tallyDevice("") != "—" {
		t.Fatal("device classification")
	}
}
