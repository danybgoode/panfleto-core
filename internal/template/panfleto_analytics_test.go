package template // import "miniflux.app/v2/internal/template"

import (
	"os"
	"strings"
	"testing"

	"miniflux.app/v2/internal/config"
)

// panfleto: connect-src gains the analytics origin only when ANALYTICS_SCRIPT_URL is set.
func TestCSPAnalyticsOrigin(t *testing.T) {
	saved := config.Opts
	defer func() { config.Opts = saved }()

	os.Clearenv()
	opts, err := config.NewConfigParser().ParseEnvironmentVariables()
	if err != nil {
		t.Fatal(err)
	}
	config.Opts = opts
	if strings.Contains(csp(nil, "n"), "tally") {
		t.Fatal("CSP changed with analytics off")
	}

	os.Setenv("ANALYTICS_SCRIPT_URL", "https://tally.goldenfrijoles.com/t.js")
	if config.Opts, err = config.NewConfigParser().ParseEnvironmentVariables(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csp(nil, "n"), "connect-src 'self' https://tally.goldenfrijoles.com;") {
		t.Fatalf("connect-src missing the analytics origin: %s", csp(nil, "n"))
	}
}
