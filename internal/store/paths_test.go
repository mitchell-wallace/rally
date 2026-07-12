package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRallyDirFallsBackToLegacyDirectory(t *testing.T) {
	workspaceDir := t.TempDir()

	want := filepath.Join(workspaceDir, ".rally")
	if got := RallyDir(workspaceDir); got != want {
		t.Fatalf("RallyDir() = %q, want legacy fallback %q", got, want)
	}
	if got := ConfigPath(workspaceDir); got != filepath.Join(want, "config.toml") {
		t.Fatalf("ConfigPath() = %q, want path below legacy fallback", got)
	}
}

func TestRallyDirPrefersCircuitDirectoryWhenPresent(t *testing.T) {
	workspaceDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspaceDir, ".rally"), 0o755); err != nil {
		t.Fatal(err)
	}
	circuitDir := filepath.Join(workspaceDir, ".circuit", "rally")
	if err := os.MkdirAll(circuitDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := RallyDir(workspaceDir); got != circuitDir {
		t.Fatalf("RallyDir() = %q, want preferred Circuit path %q", got, circuitDir)
	}
	if got := StateDir(workspaceDir); got != filepath.Join(circuitDir, "state") {
		t.Fatalf("StateDir() = %q, want path below preferred Circuit directory", got)
	}
}
