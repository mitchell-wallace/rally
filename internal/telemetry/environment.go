package telemetry

import (
	"os"
	"runtime"

	"github.com/mitchell-wallace/rally/internal/buildinfo"
	"golang.org/x/term"
)

// EnvironmentContext returns a context block describing the process
// environment. Values are stable for the life of the process, so this is
// called once at init and reused. It deliberately excludes hostname,
// username, and any network identity.
func EnvironmentContext() map[string]interface{} {
	return map[string]interface{}{
		"version": buildinfo.EmbeddedVersion(),
		"go_os":   runtime.GOOS,
		"go_arch": runtime.GOARCH,
		"term":    termDescription(),
	}
}

// termDescription returns the $TERM value when stdout is a terminal, or
// "non-tty" when it is not. This captures whether the CLI is running in an
// interactive context without leaking host identity.
func termDescription() string {
	if term.IsTerminal(int(os.Stdout.Fd())) {
		if t := os.Getenv("TERM"); t != "" {
			return t
		}
		return "unknown"
	}
	return "non-tty"
}
