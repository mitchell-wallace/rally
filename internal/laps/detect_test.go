package laps

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDetect(t *testing.T) {
	// We assume "laps" is on PATH from the install step.
	// If not, skip tests that need it.
	haveLapsBin := true
	if _, err := execLookPath("laps"); err != nil {
		haveLapsBin = false
	}

	tmp := t.TempDir()

	// Scenario: no .laps/ at all
	if Detect(tmp) {
		t.Error("expected false when .laps/ missing")
	}

	// Scenario: bare .laps/ without laps.json
	_ = os.MkdirAll(filepath.Join(tmp, ".laps"), 0o755)
	if Detect(tmp) {
		t.Error("expected false when .laps/laps.json missing")
	}

	// Scenario: .laps/laps.json exists but no laps binary
	lapsJSON := filepath.Join(tmp, ".laps", "laps.json")
	_ = os.WriteFile(lapsJSON, []byte("{}"), 0o644)
	if haveLapsBin {
		// Temporarily break PATH so laps binary is not found.
		oldPath := os.Getenv("PATH")
		os.Setenv("PATH", "/nonexistent")
		defer os.Setenv("PATH", oldPath)
		if Detect(tmp) {
			t.Error("expected false when laps binary not on PATH")
		}
		os.Setenv("PATH", oldPath)
	}

	// Scenario: both present
	if haveLapsBin {
		if !Detect(tmp) {
			t.Error("expected true when both .laps/laps.json and laps binary present")
		}
	}
}

func execLookPath(name string) (string, error) {
	// duplicated to avoid importing os/exec directly in test
	// (we just need a way to know if laps is available)
	return exec.LookPath(name)
}
