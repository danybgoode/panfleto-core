package config // import "miniflux.app/v2/internal/config"

import (
	"os"
	"testing"
)

// panfleto: the analytics options are off by default and only accept an absolute https URL.
func TestAnalyticsDefaultsOff(t *testing.T) {
	os.Clearenv()
	opts, err := NewConfigParser().ParseEnvironmentVariables()
	if err != nil {
		t.Fatal(err)
	}
	if opts.AnalyticsScriptURL() != "" || opts.AnalyticsSite() != "" || opts.AnalyticsOrigin() != "" {
		t.Fatalf("analytics should be off by default")
	}
}

func TestAnalyticsOrigin(t *testing.T) {
	os.Clearenv()
	os.Setenv("ANALYTICS_SCRIPT_URL", "https://tally.goldenfrijoles.com/t.js")
	os.Setenv("ANALYTICS_SITE", "panfleto")
	opts, err := NewConfigParser().ParseEnvironmentVariables()
	if err != nil {
		t.Fatal(err)
	}
	if got := opts.AnalyticsOrigin(); got != "https://tally.goldenfrijoles.com" {
		t.Fatalf("origin = %q", got)
	}
	if opts.AnalyticsSite() != "panfleto" {
		t.Fatalf("site = %q", opts.AnalyticsSite())
	}
}

func TestAnalyticsRejectsInsecureURL(t *testing.T) {
	for _, bad := range []string{"http://tally.example/t.js", "/t.js", "javascript:alert(1)"} {
		os.Clearenv()
		os.Setenv("ANALYTICS_SCRIPT_URL", bad)
		if _, err := NewConfigParser().ParseEnvironmentVariables(); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
