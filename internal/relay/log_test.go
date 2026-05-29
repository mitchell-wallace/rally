package relay

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/store"
)

func TestRepoKey_Format(t *testing.T) {
	keyRe := regexp.MustCompile(`^[a-z0-9-]{1,8}-[0-9a-f]{4}$`)

	tests := []struct {
		name string
		path string
	}{
		{"short folder", "/tmp/foo"},
		{"long folder", "/tmp/this-is-a-very-long-repo-folder-name"},
		{"underscored", "/var/lib/some_repo"},
		{"trailing dashes", "/tmp/--weird--"},
		{"deep path", "/home/user/code/projects/checkout-a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := repoKey(tt.path)
			if !keyRe.MatchString(got) {
				t.Errorf("repoKey(%q) = %q, want format <basename[:8]>-<4 hex chars>", tt.path, got)
			}
			parts := strings.Split(got, "-")
			hash := parts[len(parts)-1]
			if len(hash) != 4 {
				t.Errorf("hash segment of %q is %d chars, want 4", got, len(hash))
			}
		})
	}
}

func TestRepoKey_Deterministic(t *testing.T) {
	path := "/tmp/some-repo"
	a := repoKey(path)
	b := repoKey(path)
	if a != b {
		t.Errorf("repoKey not deterministic: %q vs %q", a, b)
	}
}

func TestRepoKey_DistinctPathsDistinctKeys(t *testing.T) {
	// Two repos with the same basename under different paths must produce
	// different keys so logs written to a shared data dir never collide.
	keyA := repoKey("/tmp/a/myrepo")
	keyB := repoKey("/tmp/b/myrepo")
	if keyA == keyB {
		t.Errorf("expected distinct keys for distinct paths, both got %q", keyA)
	}
}

func TestRepoKey_LogPathScoping(t *testing.T) {
	// rally tail --try N reads each try's LogPath from its workspace's
	// tries.jsonl, so the path used by openRelayLog is what scoping depends
	// on. Verify two repos under one data dir get distinct relay log paths.
	dataDir := t.TempDir()
	pathA := repoKey("/tmp/repos/alpha")
	pathB := repoKey("/tmp/repos/beta")
	logA := relayLogPath(dataDir, "/tmp/repos/alpha", 1)
	logB := relayLogPath(dataDir, "/tmp/repos/beta", 1)
	if logA == logB {
		t.Errorf("relay log paths collided: %q", logA)
	}
	if !strings.Contains(logA, pathA) || !strings.Contains(logB, pathB) {
		t.Errorf("relay paths missing repo key: %q (want %s), %q (want %s)", logA, pathA, logB, pathB)
	}
}

func TestPruneRepoRelayLogs(t *testing.T) {
	tmp := t.TempDir()
	relaysDir := store.RelaysDir(tmp)
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
