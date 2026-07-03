package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mitchell-wallace/rally/internal/store"
)

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	rallyDir := store.RallyDir(dir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
