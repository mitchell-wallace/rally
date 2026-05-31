package relay

import (
	"regexp"
	"strings"
	"testing"
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
