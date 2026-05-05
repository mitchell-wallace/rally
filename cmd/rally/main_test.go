package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/spf13/cobra"
)

func writeTestConfig(t *testing.T, dir, content string) {
	t.Helper()
	rallyDir := filepath.Join(dir, ".rally")
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunRelayLoadsInstructions(t *testing.T) {
	tmp := t.TempDir()

	rallyDir := filepath.Join(tmp, ".rally")
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(rallyDir, "config.toml")
	configContent := `schema_version = 2
laps_instructions = ""
run_hooks_on_autocommit = false
data_dir = ""

[defaults]
iterations = 5
mix = "cc cx"
claude_model = "sonnet"
codex_model = ""
gemini_model = ""
opencode_model = ""
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

	expectedFields := []string{"schema_version", "laps_instructions", "run_hooks_on_autocommit", "data_dir", "[defaults]", "iterations", "mix", "claude_model", "codex_model", "gemini_model", "opencode_model"}
	for _, f := range expectedFields {
		if !strings.Contains(configContent, f) {
			t.Errorf("init template missing field %q", f)
		}
	}
}

func TestRunInit_WritesNewShapeConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	cmd.SetArgs([]string{})
	if err := runInit(cmd, []string{}); err != nil {
		t.Fatalf("runInit failed: %v", err)
	}

	configPath := filepath.Join(tmp, ".rally", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "schema_version = 2") {
		t.Error("init config missing schema_version = 2")
	}
	if !strings.Contains(content, "[defaults]") {
		t.Error("init config missing [defaults] section")
	}
	if !strings.Contains(content, "iterations") {
		t.Error("init config missing iterations in [defaults]")
	}
	if !strings.Contains(content, "mix") {
		t.Error("init config missing mix in [defaults]")
	}
	if !strings.Contains(content, "claude_model") {
		t.Error("init config missing claude_model in [defaults]")
	}

	cfg, err := config.LoadV2(tmp)
	if err != nil {
		t.Fatalf("LoadV2 of init config failed: %v", err)
	}
	if cfg.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", cfg.SchemaVersion)
	}
	if cfg.Defaults.Iterations != 5 {
		t.Errorf("Defaults.Iterations = %d, want 5", cfg.Defaults.Iterations)
	}
}

func TestRunInit_DoesNotOverwriteExistingConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeTestConfig(t, tmp, `schema_version = 2
[defaults]
claude_model = "my-custom-model"
`)

	cmd := &cobra.Command{}
	if err := runInit(cmd, []string{}); err != nil {
		t.Fatalf("runInit failed: %v", err)
	}

	cfg, err := config.LoadV2(tmp)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if cfg.ClaudeModel != "my-custom-model" {
		t.Errorf("ClaudeModel = %q, want 'my-custom-model' (existing config should be preserved)", cfg.ClaudeModel)
	}
}
