package relay

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPruneRepoRelayLogs(t *testing.T) {
	tmp := t.TempDir()
	relaysDir := filepath.Join(tmp, ".rally", "relays")
	if err := os.MkdirAll(relaysDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create 15 relay log files (relay-1.log through relay-15.log)
	for i := 1; i <= 15; i++ {
		path := filepath.Join(relaysDir, fmt.Sprintf("relay-%d.log", i))
		if err := os.WriteFile(path, []byte("log entry\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := PruneRepoRelayLogs(tmp, 10); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(relaysDir)
	if err != nil {
		t.Fatal(err)
	}

	// Should keep 10 most recent files (relay-6.log through relay-15.log)
	if len(entries) != 10 {
		t.Fatalf("expected 10 files, got %d", len(entries))
	}

	// Verify relay-1.log through relay-5.log are removed
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("relay-%d.log", i)
		if _, err := os.Stat(filepath.Join(relaysDir, name)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed", name)
		}
	}

	// Verify relay-6.log through relay-15.log are kept
	for i := 6; i <= 15; i++ {
		name := fmt.Sprintf("relay-%d.log", i)
		if _, err := os.Stat(filepath.Join(relaysDir, name)); os.IsNotExist(err) {
			t.Errorf("expected %s to be kept", name)
		}
	}
}
