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
