package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/spf13/cobra"
)

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

	configPath := store.ConfigPath(tmp)
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
		path := filepath.Join(store.AgentsDir(tmp), role+".md")
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

func writeFileMain(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func initGitTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := gitx.GitOutput(dir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := gitx.GitOutput(dir, "config", "user.name", "Test"); err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if _, err := gitx.GitOutput(dir, "config", "user.email", "test@localhost"); err != nil {
		t.Fatalf("git config user.email: %v", err)
	}
	return dir
}

func makeInitialCommit(t *testing.T, dir string) {
	t.Helper()
	writeFileMain(t, dir, "README.md", "# test\n")
	if _, err := gitx.GitOutput(dir, "add", "README.md"); err != nil {
		t.Fatalf("git add README.md: %v", err)
	}
	if _, err := gitx.GitOutput(dir, "commit", "-m", "initial commit"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
}

func countCommits(t *testing.T, dir string) int {
	t.Helper()
	out, err := gitx.GitOutput(dir, "rev-list", "--count", "HEAD")
	if err != nil {
		t.Fatalf("git rev-list --count HEAD: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse commit count %q: %v", strings.TrimSpace(string(out)), err)
	}
	return n
}

func lastCommitMessage(t *testing.T, dir string) string {
	t.Helper()
	out, err := gitx.GitOutput(dir, "log", "-1", "--format=%s")
	if err != nil {
		t.Fatalf("git log -1 --format=%%s: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func commitMessages(t *testing.T, dir string) []string {
	t.Helper()
	out, err := gitx.GitOutput(dir, "log", "--format=%s")
	if err != nil {
		t.Fatalf("git log --format=%%s: %v", err)
	}
	var msgs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			msgs = append(msgs, line)
		}
	}
	return msgs
}

func createInitSetupFiles(t *testing.T, dir string) {
	t.Helper()
	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")
	writeFileMain(t, dir, ".rally/README.md", "# Rally\n")
	if _, err := gitx.GitOutput(dir, "add", ".rally/.gitignore", ".rally/config.toml", ".rally/README.md"); err != nil {
		t.Fatalf("git add setup files: %v", err)
	}
	if _, err := gitx.GitOutput(dir, "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit setup files: %v", err)
	}
}

func TestCommitSetupFiles_InitCreatesExactlyOneCommit(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)

	before := countCommits(t, dir)

	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")
	writeFileMain(t, dir, ".rally/README.md", "# Rally\n")

	committed, err := commitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
		".rally/README.md",
	}, "rally: initialize workspace")
	if err != nil {
		t.Fatalf("commitSetupFiles: %v", err)
	}
	if !committed {
		t.Fatal("expected committed=true on first init")
	}

	after := countCommits(t, dir)
	if after != before+1 {
		t.Fatalf("expected %d commits, got %d", before+1, after)
	}

	msg := lastCommitMessage(t, dir)
	if msg != "rally: initialize workspace" {
		t.Fatalf("commit message = %q, want %q", msg, "rally: initialize workspace")
	}
}

func TestCommitSetupFiles_InitRerunIsNoOp(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)
	createInitSetupFiles(t, dir)

	before := countCommits(t, dir)

	committed, err := commitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
		".rally/README.md",
	}, "rally: initialize workspace")
	if err != nil {
		t.Fatalf("commitSetupFiles: %v", err)
	}
	if committed {
		t.Fatal("expected committed=false on rerun with nothing changed")
	}

	after := countCommits(t, dir)
	if after != before {
		t.Fatalf("expected %d commits (no new commit), got %d", before, after)
	}
}

func TestCommitSetupFiles_DirtyWorkingTreeNotSwept(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)

	userFile := filepath.Join(dir, "user-code.go")
	if err := os.WriteFile(userFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write user file: %v", err)
	}

	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")
	writeFileMain(t, dir, ".rally/README.md", "# Rally\n")

	committed, err := commitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
		".rally/README.md",
	}, "rally: initialize workspace")
	if err != nil {
		t.Fatalf("commitSetupFiles: %v", err)
	}
	if !committed {
		t.Fatal("expected committed=true even with dirty tree")
	}

	out, err := gitx.GitOutput(dir, "status", "--porcelain", "user-code.go")
	if err != nil {
		t.Fatalf("git status user-code.go: %v", err)
	}
	status := strings.TrimSpace(string(out))
	if status == "" {
		t.Fatal("user-code.go should still appear in status (unstaged)")
	}
	if strings.Contains(status, "A ") || strings.Contains(status, "M ") {
		t.Fatalf("user-code.go should NOT be staged, got status %q", status)
	}

	msg := lastCommitMessage(t, dir)
	if msg != "rally: initialize workspace" {
		t.Fatalf("commit message = %q, want %q", msg, "rally: initialize workspace")
	}
}

func TestCommitSetupFiles_HookInstallCreatesCommit(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)
	createInitSetupFiles(t, dir)

	before := countCommits(t, dir)

	lapsDir := filepath.Join(dir, ".laps")
	hooksDir := filepath.Join(lapsDir, "hooks", "rally")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	writeFileMain(t, dir, ".laps/hooks.json", `{"version":1,"hooks":[]}`)
	writeFileMain(t, dir, ".laps/hooks/rally/laps-done-hook.sh", "#!/bin/sh\nexit 0\n")
	writeFileMain(t, dir, ".laps/hooks/rally/laps-handoff-hook.sh", "#!/bin/sh\nexit 0\n")
	writeFileMain(t, dir, ".laps/hooks/rally/laps-wrapup-hook.sh", "#!/bin/sh\nexit 0\n")

	hookPaths := []string{
		".laps/hooks.json",
		".laps/hooks/rally/laps-done-hook.sh",
		".laps/hooks/rally/laps-handoff-hook.sh",
		".laps/hooks/rally/laps-wrapup-hook.sh",
	}
	committed, err := commitSetupFiles(dir, hookPaths, "rally: install laps hooks")
	if err != nil {
		t.Fatalf("commitSetupFiles: %v", err)
	}
	if !committed {
		t.Fatal("expected committed=true on first hook install")
	}

	after := countCommits(t, dir)
	if after != before+1 {
		t.Fatalf("expected %d commits, got %d", before+1, after)
	}

	msg := lastCommitMessage(t, dir)
	if msg != "rally: install laps hooks" {
		t.Fatalf("commit message = %q, want %q", msg, "rally: install laps hooks")
	}
}

func TestCommitSetupFiles_HookReinstallIsNoOp(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)
	createInitSetupFiles(t, dir)

	hookPaths := []string{
		".laps/hooks.json",
		".laps/hooks/rally/laps-done-hook.sh",
		".laps/hooks/rally/laps-handoff-hook.sh",
		".laps/hooks/rally/laps-wrapup-hook.sh",
	}

	lapsDir := filepath.Join(dir, ".laps")
	hooksDir := filepath.Join(lapsDir, "hooks", "rally")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	writeFileMain(t, dir, ".laps/hooks.json", `{"version":1,"hooks":[]}`)
	writeFileMain(t, dir, ".laps/hooks/rally/laps-done-hook.sh", "#!/bin/sh\nexit 0\n")
	writeFileMain(t, dir, ".laps/hooks/rally/laps-handoff-hook.sh", "#!/bin/sh\nexit 0\n")
	writeFileMain(t, dir, ".laps/hooks/rally/laps-wrapup-hook.sh", "#!/bin/sh\nexit 0\n")

	_, err := commitSetupFiles(dir, hookPaths, "rally: install laps hooks")
	if err != nil {
		t.Fatalf("first commitSetupFiles: %v", err)
	}

	before := countCommits(t, dir)

	committed, err := commitSetupFiles(dir, hookPaths, "rally: install laps hooks")
	if err != nil {
		t.Fatalf("reinstall commitSetupFiles: %v", err)
	}
	if committed {
		t.Fatal("expected committed=false on hook reinstall")
	}

	after := countCommits(t, dir)
	if after != before {
		t.Fatalf("expected %d commits (no new commit), got %d", before, after)
	}

	msgs := commitMessages(t, dir)
	initCount := 0
	for _, m := range msgs {
		if m == "rally: install laps hooks" {
			initCount++
		}
	}
	if initCount != 1 {
		t.Fatalf("expected exactly 1 'rally: install laps hooks' commit, got %d", initCount)
	}
}

// --- Edge-case hardening: special characters in commit messages ---

// commitSetupFiles must pass messages with double quotes and dollar signs
// through to git literally — no shell expansion because exec.Command is used.
func TestCommitSetupFiles_SpecialCharMessage_DoubleQuotesAndDollars(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)

	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")

	msg := "rally: init \"special\" with $HOME and `backtick`"
	committed, err := commitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
	}, msg)
	if err != nil {
		t.Fatalf("commitSetupFiles: %v", err)
	}
	if !committed {
		t.Fatal("expected committed=true")
	}

	got := lastCommitMessage(t, dir)
	if got != msg {
		t.Errorf("commit message = %q, want %q", got, msg)
	}
}

// commitSetupFiles must handle single quotes and newlines in the message.
// The subject line (first line) is verified via git log.
func TestCommitSetupFiles_SpecialCharMessage_SingleQuotesAndNewlines(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)

	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")

	msg := "rally: init user's workspace\n\nBody with special 'chars'."
	committed, err := commitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
	}, msg)
	if err != nil {
		t.Fatalf("commitSetupFiles: %v", err)
	}
	if !committed {
		t.Fatal("expected committed=true")
	}

	got := lastCommitMessage(t, dir)
	if got != `rally: init user's workspace` {
		t.Errorf("commit subject = %q, want %q", got, `rally: init user's workspace`)
	}
}

// --- Edge-case hardening: git unavailable ---

// commitSetupFiles on a non-git directory returns (false, nil) — no crash.
func TestCommitSetupFiles_NonGitDirIsNoOp(t *testing.T) {
	dir := t.TempDir()

	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")

	committed, err := commitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
	}, "rally: initialize workspace")
	if err != nil {
		t.Fatalf("commitSetupFiles non-git dir: %v", err)
	}
	if committed {
		t.Error("expected committed=false for non-git dir")
	}
}

// commitSetupFiles in a directory with a corrupted/empty .git returns
// (false, nil) — graceful skip, no panic.
func TestCommitSetupFiles_CorruptedGitDirIsNoOp(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")

	committed, err := commitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
	}, "rally: initialize workspace")
	if err != nil {
		t.Fatalf("commitSetupFiles corrupted git dir: %v", err)
	}
	if committed {
		t.Error("expected committed=false for corrupted git dir")
	}
}
