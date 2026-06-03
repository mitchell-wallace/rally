package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a fresh git repository rooted at a temp dir and returns its
// path. It configures a local identity so commits succeed deterministically.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := GitOutput(dir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := GitOutput(dir, "config", "user.name", "Test"); err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if _, err := GitOutput(dir, "config", "user.email", "test@localhost"); err != nil {
		t.Fatalf("git config user.email: %v", err)
	}
	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// trackedFiles returns the set of paths git tracks at HEAD.
func trackedFiles(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out, err := GitOutput(dir, "ls-files")
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	set := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			set[line] = true
		}
	}
	return set
}

// TestCommitRallyStateSkipsGitignoredPath proves that when an operator
// gitignores one tracked .rally operational path, CommitRallyState skips it
// without error while still committing the other tracked .rally paths.
func TestCommitRallyStateSkipsGitignoredPath(t *testing.T) {
	dir := initRepo(t)

	// Operator has gitignored .rally/config.toml (a tracked operational path)
	// but not .rally/summary.jsonl.
	writeFile(t, dir, ".gitignore", ".rally/config.toml\n")
	writeFile(t, dir, ".rally/config.toml", "model = \"sonnet\"\n")
	writeFile(t, dir, ".rally/summary.jsonl", "{\"event\":\"done\"}\n")

	if err := CommitRallyState(dir); err != nil {
		t.Fatalf("CommitRallyState returned error for gitignored .rally path: %v", err)
	}

	tracked := trackedFiles(t, dir)
	if tracked[".rally/config.toml"] {
		t.Errorf(".rally/config.toml was gitignored but got committed")
	}
	if !tracked[".rally/summary.jsonl"] {
		t.Errorf(".rally/summary.jsonl should have been committed, tracked=%v", tracked)
	}
}

// TestCommitRallyStateCommitsConfigWhenNotIgnored confirms the default tracked
// paths include .rally/config.toml and .rally/summary.jsonl when no gitignore
// rule excludes them.
func TestCommitRallyStateCommitsConfigWhenNotIgnored(t *testing.T) {
	dir := initRepo(t)

	writeFile(t, dir, ".rally/config.toml", "model = \"sonnet\"\n")
	writeFile(t, dir, ".rally/summary.jsonl", "{\"event\":\"done\"}\n")

	if err := CommitRallyState(dir); err != nil {
		t.Fatalf("CommitRallyState: %v", err)
	}

	tracked := trackedFiles(t, dir)
	for _, p := range []string{".rally/config.toml", ".rally/summary.jsonl"} {
		if !tracked[p] {
			t.Errorf("%s should have been committed, tracked=%v", p, tracked)
		}
	}
}
