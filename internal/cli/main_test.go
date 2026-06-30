package cli

import (
	"io"
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

// TestMain isolates the user-level rally config from the developer's real home
// for the whole package, so tests that run `rally init` never read or write the
// machine's ~/.config/rally. Individual tests that assert user-config behaviour
// further isolate per-test via isolateUserConfig.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rally-xdg-*")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// isolateUserConfig points the user-level config at a fresh per-test directory so
// init/roles tests neither read a prior test's user config nor pollute it.
func isolateUserConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg"))
}

func writeTestConfig(t *testing.T, dir, content string) {
	t.Helper()
	rallyDir := store.RallyDir(dir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunRelayLoadsInstructions(t *testing.T) {
	tmp := t.TempDir()

	rallyDir := store.RallyDir(tmp)
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

	expectedFields := []string{"schema_version", "laps_instructions", "run_hooks_on_autocommit", "data_dir", "[defaults]", "iterations", "mix", "claude_model", "codex_model", "opencode_model", "antigravity_model"}
	for _, f := range expectedFields {
		if !strings.Contains(configContent, f) {
			t.Errorf("init template missing field %q", f)
		}
	}
}

func assertRootCommandDoesNotInitializeTelemetryWithBakedNewRelicLicense(t *testing.T, args ...string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RALLY_TELEMETRY", "")
	t.Setenv("NEW_RELIC_LICENSE_KEY", "")

	rootCmd := NewRootCommand(RootOptions{
		Version: "dev",
		NewRelic: NewRelicOptions{
			LicenseKey: "baked-license",
		},
	})
	rootCmd.SetArgs(args)
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("%v command failed: %v", args, err)
	}

	machineIDPath := filepath.Join(home, ".local", "share", "rally", "machine-id")
	if _, err := os.Stat(machineIDPath); !os.IsNotExist(err) {
		t.Fatalf("%v command must not create machine-id file, stat err=%v", args, err)
	}
}

func TestVersionCommandDoesNotInitializeTelemetryWithBakedNewRelicLicense(t *testing.T) {
	assertRootCommandDoesNotInitializeTelemetryWithBakedNewRelicLicense(t, "--version")
}

func TestHelpCommandDoesNotInitializeTelemetryWithBakedNewRelicLicense(t *testing.T) {
	assertRootCommandDoesNotInitializeTelemetryWithBakedNewRelicLicense(t, "--help")
}

func TestStartCommandSilencesUsageForRuntimeErrors(t *testing.T) {
	startCmd, _, err := NewRootCommand(RootOptions{Version: "dev"}).Find([]string{"start"})
	if err != nil {
		t.Fatal(err)
	}
	if !startCmd.SilenceUsage {
		t.Fatal("start command must not print usage for runtime relay errors")
	}
}

func TestRunInit_WritesNewShapeConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	isolateUserConfig(t)

	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	cmd.SetArgs([]string{})
	if err := runInit(cmd, []string{}); err != nil {
		t.Fatalf("runInit failed: %v", err)
	}

	// The repo-level config is comments-only and points at the user file.
	configPath := store.ConfigPath(tmp)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read repo config: %v", err)
	}
	repoContent := string(data)
	if !strings.Contains(repoContent, "OVERRIDES ONLY") {
		t.Error("repo config missing the overrides-only header")
	}
	if !strings.Contains(repoContent, "~/.config/rally/config.toml") {
		t.Error("repo config missing pointer to the user-level config")
	}
	for _, line := range strings.Split(repoContent, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			t.Errorf("repo config should be comments-only, found active line: %q", line)
		}
	}

	// The active base config lives in the user-level file.
	userData, err := os.ReadFile(store.UserConfigPath())
	if err != nil {
		t.Fatalf("failed to read user config: %v", err)
	}
	userContent := string(userData)
	for _, want := range []string{"schema_version = 2", "[defaults]", "iterations", "mix", "claude_model", "antigravity_model"} {
		if !strings.Contains(userContent, want) {
			t.Errorf("user config missing %q", want)
		}
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

	gitignore, err := os.ReadFile(filepath.Join(store.RallyDir(tmp), ".gitignore"))
	if err != nil {
		t.Fatalf("failed to read .rally/.gitignore: %v", err)
	}
	if string(gitignore) != "state/\n" {
		t.Fatalf(".rally/.gitignore = %q, want %q", string(gitignore), "state/\n")
	}

	readme, err := os.ReadFile(filepath.Join(store.RallyDir(tmp), "README.md"))
	if err != nil {
		t.Fatalf("failed to read .rally/README.md: %v", err)
	}
	readmeText := string(readme)
	if !strings.Contains(readmeText, ".rally/state/") {
		t.Fatal("README missing .rally/state/ layout")
	}
	if strings.Contains(readmeText, "git-tracked") {
		t.Fatal("README still claims JSONL state is git-tracked")
	}

	s, err := store.NewStore(store.RallyDir(tmp))
	if err != nil {
		t.Fatalf("NewStore after init failed: %v", err)
	}
	if err := s.AppendTry(store.TryRecord{ID: 1, AgentType: "codex"}); err != nil {
		t.Fatalf("AppendTry after init failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.StateDir(tmp), "tries.jsonl")); err != nil {
		t.Fatalf("expected try record under .rally/state/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.RallyDir(tmp), "tries.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("top-level tries.jsonl should not exist, stat err=%v", err)
	}
}

func TestRunInit_DoesNotOverwriteExistingConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	isolateUserConfig(t)

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

func TestRunInit_UpdatesExistingGitignore(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	rallyDir := store.RallyDir(tmp)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write an existing .gitignore without state/
	existingGitignore := "current_task.md\nrelays/\nrun-state.json\n"
	gitignorePath := filepath.Join(rallyDir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(existingGitignore), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	if err := runInit(cmd, []string{}); err != nil {
		t.Fatalf("runInit failed: %v", err)
	}

	// Verify .gitignore got updated with state/
	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("failed to read .gitignore: %v", err)
	}
	expected := "current_task.md\nrelays/\nrun-state.json\nstate/\n"
	if string(data) != expected {
		t.Errorf(".gitignore = %q, want %q", string(data), expected)
	}
}

func TestRunInit_UpdatesExistingGitignoreNoTrailingNewline(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	rallyDir := store.RallyDir(tmp)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write an existing .gitignore without state/ and no trailing newline
	existingGitignore := "current_task.md\nrelays/\nrun-state.json"
	gitignorePath := filepath.Join(rallyDir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(existingGitignore), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	if err := runInit(cmd, []string{}); err != nil {
		t.Fatalf("runInit failed: %v", err)
	}

	// Verify .gitignore got updated with state/ and a preceding newline
	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("failed to read .gitignore: %v", err)
	}
	expected := "current_task.md\nrelays/\nrun-state.json\nstate/\n"
	if string(data) != expected {
		t.Errorf(".gitignore = %q, want %q", string(data), expected)
	}
}

func TestRunInitRoles_InstallsRoutesAndRoleInstructions(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	isolateUserConfig(t)

	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	if err := runInitAll(cmd, []string{}); err != nil {
		t.Fatalf("runInitAll failed: %v", err)
	}

	cfg, err := config.LoadV2(tmp)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	if cfg.Defaults.OpenCodeModel != "opencode-go/kimi-k2.6" {
		t.Errorf("OpenCodeModel = %q, want opencode-go/kimi-k2.6", cfg.Defaults.OpenCodeModel)
	}
	if cfg.Defaults.ClaudeModel != "claude-opus-4-7" {
		t.Errorf("ClaudeModel = %q, want claude-opus-4-7", cfg.Defaults.ClaudeModel)
	}
	if cfg.Defaults.CodexModel != "gpt-5.5" {
		t.Errorf("CodexModel = %q, want gpt-5.5", cfg.Defaults.CodexModel)
	}
	if cfg.Defaults.AntigravityModel != "Gemini 3.5 Flash (High)" {
		t.Errorf("AntigravityModel = %q, want Gemini 3.5 Flash (High)", cfg.Defaults.AntigravityModel)
	}

	wantRoutes := map[string]string{
		"default": "opencode",
		"junior":  "opencode",
		"senior":  "claude",
		"ui":      "ag",
		"verify":  "codex",
	}
	for role, want := range wantRoutes {
		got := cfg.Routes[role]
		if len(got) != 1 || got[0] != want {
			t.Errorf("route %s = %#v, want [%s]", role, got, want)
		}
	}

	for _, role := range []string{"junior", "senior", "ui", "verify"} {
		path := filepath.Join(store.AgentsBuiltinDir(tmp), role+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s instructions: %v", role, err)
		}
		if !strings.Contains(string(data), "# ") {
			t.Errorf("%s instructions missing heading", role)
		}
	}
}

func TestRunInitAll_RunsBothInSequence(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	isolateUserConfig(t)

	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	if err := runInitAll(cmd, []string{}); err != nil {
		t.Fatalf("runInitAll failed: %v", err)
	}

	configPath := store.ConfigPath(tmp)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("workspace config not created: %v", err)
	}
	gitignorePath := filepath.Join(store.RallyDir(tmp), ".gitignore")
	if _, err := os.Stat(gitignorePath); err != nil {
		t.Fatalf("workspace .gitignore not created: %v", err)
	}
	readmePath := filepath.Join(store.RallyDir(tmp), "README.md")
	if _, err := os.Stat(readmePath); err != nil {
		t.Fatalf("workspace README.md not created: %v", err)
	}

	cfg, err := config.LoadV2(tmp)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if cfg.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", cfg.SchemaVersion)
	}
	if _, ok := cfg.Routes["junior"]; !ok {
		t.Error("missing junior route (role setup not run)")
	}
	if cfg.Defaults.OpenCodeModel != "opencode-go/kimi-k2.6" {
		t.Errorf("OpenCodeModel = %q, want opencode-go/kimi-k2.6", cfg.Defaults.OpenCodeModel)
	}

	for _, role := range []string{"junior", "senior", "ui", "verify"} {
		path := filepath.Join(store.AgentsBuiltinDir(tmp), role+".md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("role instructions for %s not created: %v", role, err)
		}
	}
}

func TestRunInitRoles_OnlyTouchesRoleConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	isolateUserConfig(t)

	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	if err := runInit(cmd, []string{}); err != nil {
		t.Fatalf("runInit failed: %v", err)
	}

	readmePath := filepath.Join(store.RallyDir(tmp), "README.md")
	readmeBefore, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}

	gitignorePath := filepath.Join(store.RallyDir(tmp), ".gitignore")
	gitignoreBefore, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	if err := runInitRoles(cmd, []string{}); err != nil {
		t.Fatalf("runInitRoles failed: %v", err)
	}

	readmeAfter, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README.md after roles: %v", err)
	}
	if string(readmeAfter) != string(readmeBefore) {
		t.Error("init roles should not modify README.md")
	}

	gitignoreAfter, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore after roles: %v", err)
	}
	if string(gitignoreAfter) != string(gitignoreBefore) {
		t.Error("init roles should not modify .gitignore")
	}

	cfg, err := config.LoadV2(tmp)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if _, ok := cfg.Routes["junior"]; !ok {
		t.Error("missing junior route")
	}
	if got := cfg.Routes["recovery"]; len(got) != 1 || got[0] != "claude" {
		t.Errorf("recovery route = %v, want [claude]", got)
	}
	if cfg.Defaults.ClaudeModel != "claude-opus-4-7" {
		t.Errorf("ClaudeModel = %q, want claude-opus-4-7", cfg.Defaults.ClaudeModel)
	}

	for _, role := range []string{"junior", "senior", "ui", "verify", "recovery"} {
		path := filepath.Join(store.AgentsBuiltinDir(tmp), role+".md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("role instructions for %s not created: %v", role, err)
		}
	}
}

func TestRunInitAll_IdempotentRerun(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	isolateUserConfig(t)

	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	if err := runInitAll(cmd, []string{}); err != nil {
		t.Fatalf("first runInitAll failed: %v", err)
	}

	cfg, err := config.LoadV2(tmp)
	if err != nil {
		t.Fatalf("LoadV2 after first run: %v", err)
	}
	cfg.Defaults.ClaudeModel = "my-custom-claude"
	if err := config.SaveV2(tmp, cfg); err != nil {
		t.Fatalf("SaveV2 custom config: %v", err)
	}

	if err := runInitAll(cmd, []string{}); err != nil {
		t.Fatalf("second runInitAll failed: %v", err)
	}

	cfg2, err := config.LoadV2(tmp)
	if err != nil {
		t.Fatalf("LoadV2 after second run: %v", err)
	}
	if cfg2.Defaults.ClaudeModel != "my-custom-claude" {
		t.Errorf("ClaudeModel = %q, want my-custom-claude (idempotent rerun should not overwrite)", cfg2.Defaults.ClaudeModel)
	}
}

func TestRunInitRoles_IdempotentRerun(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	isolateUserConfig(t)

	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	if err := runInitAll(cmd, []string{}); err != nil {
		t.Fatalf("runInitAll setup failed: %v", err)
	}

	cfg, _ := config.LoadV2(tmp)
	cfg.Defaults.AntigravityModel = "my-antigravity"
	config.SaveV2(tmp, cfg)

	if err := runInitRoles(cmd, []string{}); err != nil {
		t.Fatalf("runInitRoles rerun failed: %v", err)
	}

	cfg2, err := config.LoadV2(tmp)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if cfg2.Defaults.AntigravityModel != "my-antigravity" {
		t.Errorf("AntigravityModel = %q, want my-antigravity (idempotent rerun should not overwrite)", cfg2.Defaults.AntigravityModel)
	}
}

func TestRunRelayNewResetsAgentStatus(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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
	key := relay.ResilienceKey{Harness: "antigravity", Model: "default"}
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

	rootCmd := NewRootCommand(RootOptions{Version: "dev"})
	rootCmd.SetArgs([]string{"start", "--new", "--iterations", "0"})
	_ = rootCmd.Execute()

	s2, _ := store.NewStore(rallyDir)
	resilience2 := relay.NewResilience(s2)
	st2, _ := resilience2.GetState(key)
	if st2 == relay.StateFrozen {
		t.Fatalf("expected agent to NOT be frozen after --new, got %v", st2)
	}
}
