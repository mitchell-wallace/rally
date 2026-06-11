package telemetry

import (
	"os"
	"runtime"
	"testing"

	"github.com/mitchell-wallace/rally/internal/buildinfo"
)

func TestEnvironmentContext_Fields(t *testing.T) {
	ctx := EnvironmentContext()

	// Must contain version from buildinfo.
	version, ok := ctx["version"].(string)
	if !ok || version == "" {
		t.Errorf("version missing or empty: %v", ctx["version"])
	}
	if version != buildinfo.EmbeddedVersion() {
		t.Errorf("version = %q, want %q", version, buildinfo.EmbeddedVersion())
	}

	// Must contain go_os matching runtime.GOOS.
	goOS, ok := ctx["go_os"].(string)
	if !ok || goOS == "" {
		t.Errorf("go_os missing or empty: %v", ctx["go_os"])
	}
	if goOS != runtime.GOOS {
		t.Errorf("go_os = %q, want %q", goOS, runtime.GOOS)
	}

	// Must contain go_arch matching runtime.GOARCH.
	goArch, ok := ctx["go_arch"].(string)
	if !ok || goArch == "" {
		t.Errorf("go_arch missing or empty: %v", ctx["go_arch"])
	}
	if goArch != runtime.GOARCH {
		t.Errorf("go_arch = %q, want %q", goArch, runtime.GOARCH)
	}

	// Must contain term field (non-empty).
	termVal, ok := ctx["term"].(string)
	if !ok || termVal == "" {
		t.Errorf("term missing or empty: %v", ctx["term"])
	}
}

func TestEnvironmentContext_NoHostnameOrUsername(t *testing.T) {
	ctx := EnvironmentContext()

	// Ensure no hostname, username, or IP-like fields exist.
	forbidden := []string{"hostname", "host", "username", "user", "ip", "server_name"}
	for _, key := range forbidden {
		if _, found := ctx[key]; found {
			t.Errorf("environment context must not contain %q, but it was present", key)
		}
	}

	// Also check that the values don't contain the hostname or username.
	hostname, _ := os.Hostname()
	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME")
	}

	for key, val := range ctx {
		str, ok := val.(string)
		if !ok {
			continue
		}
		if hostname != "" && str == hostname {
			t.Errorf("context field %q = %q matches hostname", key, str)
		}
		if username != "" && str == username {
			t.Errorf("context field %q = %q matches username", key, str)
		}
	}
}

func TestEnvironmentContext_TermNonTTY(t *testing.T) {
	// In test processes stdout is typically not a TTY, so term should
	// be "non-tty" unless the test runner has a TTY attached.
	ctx := EnvironmentContext()
	termVal := ctx["term"].(string)

	// We accept both "non-tty" and a valid $TERM value — the test just
	// proves the field is populated and non-empty.
	if termVal == "" {
		t.Error("term should not be empty")
	}
}
