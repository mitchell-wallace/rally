package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/config"
)

func TestRunRelayLoadsInstructions(t *testing.T) {
	tmp := t.TempDir()

	rallyDir := filepath.Join(tmp, ".rally")
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(rallyDir, "config.toml")
	configContent := `claude_model = "sonnet"
codex_model = ""
gemini_model = ""
opencode_model = ""
beads = "auto"
run_hooks_on_autocommit = false
data_dir = ""
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	instructionsPath := filepath.Join(rallyDir, "instructions.md")
	instructionsContent := "Always use TDD for new features.\nWrite clear error messages."
	if err := os.WriteFile(instructionsPath, []byte(instructionsContent), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(instructionsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != instructionsContent {
		t.Fatalf("instructions content mismatch: got %q, want %q", string(data), instructionsContent)
	}

	cfg, err := config.LoadV2(tmp)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if cfg.DataDir != "" {
		t.Errorf("expected empty data_dir, got %q", cfg.DataDir)
	}
	if cfg.ClaudeModel != "sonnet" {
		t.Errorf("expected sonnet model, got %q", cfg.ClaudeModel)
	}

	expectedFields := []string{"claude_model", "codex_model", "gemini_model", "opencode_model", "beads", "run_hooks_on_autocommit", "data_dir"}
	for _, f := range expectedFields {
		if !strings.Contains(configContent, f) {
			t.Errorf("init template missing field %q", f)
		}
	}
}
