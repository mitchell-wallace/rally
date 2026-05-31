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

// installSiblingLaps writes a fake laps executable next to a fake rally
// executable and points executablePath at it, so the sibling lookup resolves to
// the script. Pass an empty version to omit the sibling entirely (simulating a
// missing companion while still anchoring the executable directory).
func installSiblingLaps(t *testing.T, version string) {
	t.Helper()
	dir := t.TempDir()
	old := executablePath
	executablePath = func() (string, error) { return filepath.Join(dir, "rally"), nil }
	t.Cleanup(func() { executablePath = old })
	if version == "missing" {
		return
	}
	out := "laps (unknown)\n"
	if version != "" {
		out = "laps v" + version + "\n"
	}
	script := "#!/bin/sh\nprintf '" + out + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "laps"), []byte(script), 0o755); err != nil {
		t.Fatalf("write sibling laps: %v", err)
	}
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

// A stale bundled companion must surface a warning even when an unrelated,
// up-to-date laps copy exists on PATH; the PATH copy must not mask it.
func TestVersionWarningStaleSiblingNotMaskedByPath(t *testing.T) {
	ws := withLapsWorkspace(t)
	if release.CompareVersions(release.MinLapsVersion, "0.0.1") <= 0 {
		t.Skipf("MinLapsVersion %s too low to construct an older companion", release.MinLapsVersion)
	}
	writeFakeLaps(t, release.MinLapsVersion) // up-to-date copy on PATH
	installSiblingLaps(t, "0.0.1")           // stale bundled companion
	w := VersionWarning(ws)
	if !strings.Contains(w, "older than") {
		t.Fatalf("expected stale-companion warning despite fresh PATH copy, got %q", w)
	}
}

// When no bundled companion exists, the warning may fall back to a usable PATH
// copy and stay silent.
func TestVersionWarningMissingSiblingFallsBackToPath(t *testing.T) {
	ws := withLapsWorkspace(t)
	writeFakeLaps(t, release.MinLapsVersion) // up-to-date copy on PATH
	installSiblingLaps(t, "missing")         // no bundled companion
	if w := VersionWarning(ws); w != "" {
		t.Fatalf("expected no warning when companion missing but PATH copy is fresh, got %q", w)
	}
}

// CompanionVersion drives `rally update`'s current-version detection. It must
// report the bundled sibling's version, never an unrelated PATH copy, so a
// stale companion is updated rather than skipped.
func TestCompanionVersionIgnoresPathCopy(t *testing.T) {
	writeFakeLaps(t, release.MinLapsVersion) // fresh PATH copy must be ignored
	installSiblingLaps(t, "0.0.1")           // stale companion
	v, ok := CompanionVersion()
	if !ok || v != "0.0.1" {
		t.Fatalf("expected companion version 0.0.1, got %q ok=%v", v, ok)
	}
}

// A missing companion must report no version even when a fresh PATH copy
// exists, so `rally update` installs the sibling instead of skipping it.
func TestCompanionVersionMissingDespitePathCopy(t *testing.T) {
	writeFakeLaps(t, release.MinLapsVersion) // fresh PATH copy must be ignored
	installSiblingLaps(t, "missing")
	if v, ok := CompanionVersion(); ok {
		t.Fatalf("expected no companion version when sibling missing, got %q ok=true", v)
	}
}
