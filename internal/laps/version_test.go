package laps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/release"
)

// writeFakeLaps installs a fake `laps` executable on PATH that prints the given
// version for `laps version`. Pass an empty version to simulate a binary whose
// version cannot be parsed.
func writeFakeLaps(t *testing.T, version string) {
	t.Helper()
	binDir := t.TempDir()
	out := "laps (unknown)\n"
	if version != "" {
		out = "laps v" + version + "\n"
	}
	script := "#!/bin/sh\nprintf '%s'\n"
	script = strings.Replace(script, "%s", out, 1)
	path := filepath.Join(binDir, "laps")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake laps: %v", err)
	}
	t.Setenv("PATH", binDir)
}

func withLapsWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	lapsDir := filepath.Join(ws, ".laps")
	if err := os.MkdirAll(lapsDir, 0o755); err != nil {
		t.Fatalf("mkdir .laps: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lapsDir, "laps.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write laps.json: %v", err)
	}
	return ws
}

func TestVersionWarningNoLapsWorkspace(t *testing.T) {
	// No .laps/laps.json -> laps not in use here -> silent.
	if w := VersionWarning(t.TempDir()); w != "" {
		t.Fatalf("expected no warning, got %q", w)
	}
}

func TestVersionWarningMissingBinary(t *testing.T) {
	ws := withLapsWorkspace(t)
	t.Setenv("PATH", t.TempDir()) // empty dir: no laps on PATH
	w := VersionWarning(ws)
	if !strings.Contains(w, "not installed") {
		t.Fatalf("expected not-installed warning, got %q", w)
	}
}

func TestVersionWarningOldLaps(t *testing.T) {
	ws := withLapsWorkspace(t)
	// One patch below the minimum the hooks contract requires.
	old := release.CompareVersions(release.MinLapsVersion, "0.0.1")
	if old <= 0 {
		t.Skipf("MinLapsVersion %s too low to construct an older version", release.MinLapsVersion)
	}
	writeFakeLaps(t, "0.0.1")
	w := VersionWarning(ws)
	if !strings.Contains(w, "older than") {
		t.Fatalf("expected outdated warning, got %q", w)
	}
}

func TestVersionWarningCompatibleLaps(t *testing.T) {
	ws := withLapsWorkspace(t)
	writeFakeLaps(t, release.MinLapsVersion)
	if w := VersionWarning(ws); w != "" {
		t.Fatalf("expected no warning for compatible laps, got %q", w)
	}
}
