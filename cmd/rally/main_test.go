package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/store"
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
antigravity_model = ""
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

	expectedFields := []string{"schema_version", "laps_instructions", "run_hooks_on_autocommit", "data_dir", "[defaults]", "iterations", "mix", "claude_model", "codex_model", "gemini_model", "opencode_model", "antigravity_model"}
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
	if !strings.Contains(content, "antigravity_model") {
		t.Error("init config missing antigravity_model in [defaults]")
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

func TestRunInitRoles_InstallsRoutesAndRoleInstructions(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	if err := runInitRoles(cmd, []string{}); err != nil {
		t.Fatalf("runInitRoles failed: %v", err)
	}

	cfg, err := config.LoadV2(tmp)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	if cfg.OpenCodeModel != "opencode-go/kimi-k2.6" {
		t.Errorf("OpenCodeModel = %q, want opencode-go/kimi-k2.6", cfg.OpenCodeModel)
	}
	if cfg.ClaudeModel != "claude-opus-4-7" {
		t.Errorf("ClaudeModel = %q, want claude-opus-4-7", cfg.ClaudeModel)
	}
	if cfg.GeminiModel != "gemini-3.1-pro-preview" {
		t.Errorf("GeminiModel = %q, want gemini-3.1-pro-preview", cfg.GeminiModel)
	}
	if cfg.CodexModel != "gpt-5.5" {
		t.Errorf("CodexModel = %q, want gpt-5.5", cfg.CodexModel)
	}
	if cfg.AntigravityModel != "Gemini 3.5 Flash (High)" {
		t.Errorf("AntigravityModel = %q, want Gemini 3.5 Flash (High)", cfg.AntigravityModel)
	}

	wantRoutes := map[string]string{
		"default": "opencode",
		"junior":  "opencode",
		"senior":  "claude",
		"ui":      "gemini",
		"verify":  "codex",
	}
	for role, want := range wantRoutes {
		got := cfg.Routes[role]
		if len(got) != 1 || got[0] != want {
			t.Errorf("route %s = %#v, want [%s]", role, got, want)
		}
	}

	for _, role := range []string{"junior", "senior", "ui", "verify"} {
		path := filepath.Join(tmp, ".rally", "agents", role+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s instructions: %v", role, err)
		}
		if !strings.Contains(string(data), "# ") {
			t.Errorf("%s instructions missing heading", role)
		}
	}
}

func TestRunRelayNewResetsAgentStatus(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	if err := exec.Command("git", "init", workspaceDir).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	writeTestConfig(t, workspaceDir, "schema_version = 2\n")

	s, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}

	resilience := relay.NewResilience(s)
	key := relay.ResilienceKey{Harness: "gemini", Model: "default"}
	if err := resilience.FreezeAgent(key, 1, "test freeze"); err != nil {
		t.Fatalf("freeze agent: %v", err)
	}
	st, _ := resilience.GetState(key)
	if st != relay.StateFrozen {
		t.Fatalf("expected agent frozen, got %v", st)
	}

	origWd, _ := os.Getwd()
	os.Chdir(workspaceDir)
	defer os.Chdir(origWd)

	rootCmd.SetArgs([]string{"start", "--new", "--iterations", "0"})
	rootCmd.Execute()

	s2, _ := store.NewStore(rallyDir)
	resilience2 := relay.NewResilience(s2)
	st2, _ := resilience2.GetState(key)
	if st2 == relay.StateFrozen {
		t.Fatalf("expected agent to NOT be frozen after --new, got %v", st2)
	}
}


