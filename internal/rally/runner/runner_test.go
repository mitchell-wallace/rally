package runner

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/rally/messages"
	"github.com/mitchell-wallace/rally/internal/rally/state"
)

func TestAgentForSessionUsesDeterministicCycle(t *testing.T) {
	t.Parallel()

	mix, err := ParseAgentMix([]string{"cx:2", "cc:1"})
	if err != nil {
		t.Fatalf("ParseAgentMix returned error: %v", err)
	}
	got := []string{
		AgentForSession(1, mix),
		AgentForSession(2, mix),
		AgentForSession(3, mix),
		AgentForSession(4, mix),
	}
	want := []string{"codex", "codex", "claude", "codex"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected agent at index %d: got %s want %s", i, got[i], want[i])
		}
	}
}

func TestRunnerAppliesBatchMessageAcrossRemainingSessions(t *testing.T) {
	workspaceDir := t.TempDir()
	binDir := filepath.Join(workspaceDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	claudeScript := "#!/usr/bin/env bash\nprintf '%s\n' '{\"type\":\"result\",\"result\":\"batch-wide instruction\"}'\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(claudeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	codexScript := "#!/usr/bin/env bash\nprintf '%s\n' \"${@: -1}\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte(codexScript), 0o755); err != nil {
		t.Fatal(err)
	}

	dataDir := filepath.Join(workspaceDir, "data")
	repoPath := filepath.Join(workspaceDir, "docs", "orchestration", "rally-progress.yaml")
	r := New(Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		RepoProgressPath: repoPath,
		AgentSpecs:       []string{"cc:1", "cx:1"},
		Iterations:       2,
		Stdout:           ioDiscard{},
		Stderr:           ioDiscard{},
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	st := state.Default()
	st.NextEventID = 2
	if err := state.NewStore(dataDir).Save(st); err != nil {
		t.Fatal(err)
	}
	targetBatchID := 1
	if err := messages.NewStore(dataDir).Append(messages.Event{
		EventID:       1,
		MessageID:     1,
		Scope:         messages.ScopeBatch,
		EventType:     messages.EventMessageCreated,
		CreatedAt:     messages.Timestamp(),
		Body:          "batch-wide instruction",
		TargetBatchID: &targetBatchID,
	}); err != nil {
		t.Fatal(err)
	}

	results, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("unexpected results length: %d", len(results))
	}

	for _, sessionID := range []int{1, 2} {
		data, err := os.ReadFile(filepath.Join(dataDir, "sessions", "session-"+strconv.Itoa(sessionID), "terminal.log"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "batch-wide instruction") {
			t.Fatalf("session %d transcript missing batch message: %s", sessionID, string(data))
		}
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func TestBuildAgentCommandUsesConfiguredModels(t *testing.T) {
	t.Parallel()

	cfg := Config{
		ClaudeModel:   "sonnet",
		CodexModel:    "o3",
		GeminiModel:   "gemini-2.5-pro",
		OpenCodeModel: "anthropic/claude-sonnet-4",
	}

	tests := []struct {
		agent  string
		want   []string
		stderr bool
	}{
		{
			agent:  "claude",
			want:   []string{"claude", "-p", "--dangerously-skip-permissions", "--model", "sonnet", "--output-format", "stream-json", "--verbose", "prompt"},
			stderr: true,
		},
		{
			agent:  "codex",
			want:   []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "--model", "o3", "prompt"},
			stderr: true,
		},
		{
			agent:  "gemini",
			want:   []string{"gemini", "--model", "gemini-2.5-pro", "--prompt", "prompt", "--yolo", "--output-format", "json"},
			stderr: true,
		},
		{
			agent:  "opencode",
			want:   []string{"opencode", "run", "--model", "anthropic/claude-sonnet-4", "--format", "json", "prompt"},
			stderr: true,
		},
	}

	for _, tt := range tests {
		got, suppressStderr, err := BuildAgentCommand(cfg, tt.agent, "prompt")
		if err != nil {
			t.Fatalf("%s returned error: %v", tt.agent, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("%s command mismatch:\n got %#v\nwant %#v", tt.agent, got, tt.want)
		}
		if suppressStderr != tt.stderr {
			t.Fatalf("%s suppressStderr mismatch: got %v want %v", tt.agent, suppressStderr, tt.stderr)
		}
	}
}

func TestBuildAgentCommandOmitsEmptyModelFlags(t *testing.T) {
	t.Parallel()

	got, _, err := BuildAgentCommand(Config{}, "opencode", "prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"opencode", "run", "--format", "json", "prompt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestAgentEnvOverrides(t *testing.T) {
	t.Parallel()

	if got := AgentEnvOverrides("codex"); got != nil {
		t.Fatalf("expected no env overrides for codex, got %#v", got)
	}

	got := AgentEnvOverrides("opencode")
	want := []string{`OPENCODE_PERMISSION={"*":"allow"}`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env override mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestAutoCommitWorkspaceCommitsDirtyRepo(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Rally Test")
	runGit(t, repo, "config", "user.email", "rally@example.com")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "file.txt")
	runGit(t, repo, "commit", "-m", "initial")

	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	hash, err := autoCommitWorkspace(repo, 3, 2, "codex", false)
	if err != nil {
		t.Fatalf("autoCommitWorkspace returned error: %v", err)
	}
	if hash == "" {
		t.Fatal("expected commit hash")
	}

	logOutput := runGit(t, repo, "log", "-1", "--pretty=%s")
	if strings.TrimSpace(logOutput) != "rally: session 3 iteration 2 (codex)" {
		t.Fatalf("unexpected commit message: %q", logOutput)
	}
}

func TestAutoCommitWorkspaceUsesFallbackIdentity(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, "xdg"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(homeDir, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	repo := t.TempDir()
	runGit(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	hash, err := autoCommitWorkspace(repo, 4, 1, "gemini", false)
	if err != nil {
		t.Fatalf("autoCommitWorkspace returned error: %v", err)
	}
	if hash == "" {
		t.Fatal("expected commit hash")
	}

	author := strings.TrimSpace(runGit(t, repo, "log", "-1", "--pretty=%an <%ae>"))
	if author != "Rally <rally@localhost>" {
		t.Fatalf("unexpected fallback author: %q", author)
	}
}

func TestAutoCommitWorkspaceBypassesCommitHooks(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Rally Test")
	runGit(t, repo, "config", "user.email", "rally@example.com")
	hooksDir := filepath.Join(repo, ".git", "hooks")
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte("#!/usr/bin/env bash\nprintf 'hook says no\\n' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	hash, err := autoCommitWorkspace(repo, 5, 1, "codex", false)
	if err != nil {
		t.Fatalf("autoCommitWorkspace returned error: %v", err)
	}
	if hash == "" {
		t.Fatal("expected commit hash")
	}
	if status := strings.TrimSpace(runGit(t, repo, "status", "--porcelain")); status != "" {
		t.Fatalf("expected clean repo after hook-bypassing commit, got:\n%s", status)
	}
}

func TestAutoCommitWorkspaceRunsCommitHooksWhenEnabled(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Rally Test")
	runGit(t, repo, "config", "user.email", "rally@example.com")
	hooksDir := filepath.Join(repo, ".git", "hooks")
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte("#!/usr/bin/env bash\nprintf 'hook says no\\n' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := autoCommitWorkspace(repo, 6, 1, "codex", true)
	if err == nil {
		t.Fatal("expected autoCommitWorkspace to return hook error")
	}
	if !strings.Contains(err.Error(), "hook says no") {
		t.Fatalf("expected hook output in error, got: %v", err)
	}
}

func TestAutoCommitWorkspaceIncludesGitFailureOutput(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Rally Test")
	runGit(t, repo, "config", "user.email", "rally@example.com")
	if err := os.WriteFile(filepath.Join(repo, ".git", "index.lock"), []byte("locked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := autoCommitWorkspace(repo, 7, 1, "codex", false)
	if err == nil {
		t.Fatal("expected autoCommitWorkspace to return git error")
	}
	if !strings.Contains(err.Error(), "index.lock") {
		t.Fatalf("expected git output in error, got: %v", err)
	}
}

func TestAutoCommitWorkspaceSkipsCleanRepo(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Rally Test")
	runGit(t, repo, "config", "user.email", "rally@example.com")

	hash, err := autoCommitWorkspace(repo, 1, 1, "claude", false)
	if err != nil {
		t.Fatalf("autoCommitWorkspace returned error: %v", err)
	}
	if hash != "" {
		t.Fatalf("expected empty hash for clean repo, got %q", hash)
	}
}

func TestRunnerAutoCommitLeavesRepoCleanWhenBatchLogsAreUnignored(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Rally Test")
	runGit(t, repo, "config", "user.email", "rally@example.com")

	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	codexScript := "#!/usr/bin/env bash\nprintf 'changed\\n' > file.txt\nprintf 'done\\n'\n"
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte(codexScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dataDir := filepath.Join(t.TempDir(), "data")
	r := New(Config{
		WorkspaceDir:     repo,
		DataDir:          dataDir,
		RepoProgressPath: filepath.Join(repo, "docs", "orchestration", "rally-progress.yaml"),
		AgentSpecs:       []string{"cx:1"},
		Iterations:       1,
		Stdout:           ioDiscard{},
		Stderr:           ioDiscard{},
	})

	results, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("unexpected results length: %d", len(results))
	}

	if status := strings.TrimSpace(runGit(t, repo, "status", "--porcelain")); status != "" {
		t.Fatalf("expected clean repo after run, got:\n%s", status)
	}
	if trackedLogs := strings.TrimSpace(runGit(t, repo, "ls-files", ".rally/batches")); trackedLogs != "" {
		t.Fatalf("batch logs should remain untracked, got:\n%s", trackedLogs)
	}
	excludeData, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(excludeData), ".rally/batches/") {
		t.Fatalf("expected git info exclude to ignore batch logs, got:\n%s", string(excludeData))
	}
}

func TestFormatGeminiHeadlessResponse(t *testing.T) {
	t.Parallel()

	got, err := formatGeminiHeadlessResponse([]byte(`{"response":"OK","stats":{}}`))
	if err != nil {
		t.Fatalf("formatGeminiHeadlessResponse returned error: %v", err)
	}
	if string(got) != "OK\n" {
		t.Fatalf("unexpected formatted output: %q", got)
	}
}

func TestFormatClaudeStreamJSONResponse(t *testing.T) {
	t.Parallel()

	raw := []byte(strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hidden stream"}]}}`,
		`{"type":"result","result":"FINAL"}`,
	}, "\n"))

	got, err := formatClaudeStreamJSONResponse(raw)
	if err != nil {
		t.Fatalf("formatClaudeStreamJSONResponse returned error: %v", err)
	}
	if string(got) != "FINAL\n" {
		t.Fatalf("unexpected formatted output: %q", got)
	}
}

func TestFormatOpenCodeJSONResponse(t *testing.T) {
	t.Parallel()

	raw := []byte(strings.Join([]string{
		`{"type":"text","part":{"type":"text","messageID":"msg_1","text":"pre-tool note"}}`,
		`{"type":"tool_use","part":{"type":"tool","tool":"grep"}}`,
		`{"type":"text","part":{"type":"text","messageID":"msg_2","text":"Final "}}`,
		`{"type":"text","part":{"type":"text","messageID":"msg_2","text":"answer"}}`,
	}, "\n"))

	got, err := formatOpenCodeJSONResponse(raw)
	if err != nil {
		t.Fatalf("formatOpenCodeJSONResponse returned error: %v", err)
	}
	if string(got) != "Final answer\n" {
		t.Fatalf("unexpected formatted output: %q", got)
	}
}

func TestRunnerGeminiWritesOnlyFinalResponseToStdout(t *testing.T) {
	workspaceDir := t.TempDir()
	binDir := filepath.Join(workspaceDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	geminiScript := "#!/usr/bin/env bash\nprintf '%s\\n' 'Gemini noise' >&2\nprintf '%s\\n' '{\"response\":\"OK\",\"stats\":{}}'\n"
	geminiPath := filepath.Join(binDir, "gemini")
	if err := os.WriteFile(geminiPath, []byte(geminiScript), 0o755); err != nil {
		t.Fatal(err)
	}

	dataDir := filepath.Join(workspaceDir, "data")
	repoPath := filepath.Join(workspaceDir, "docs", "orchestration", "rally-progress.yaml")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := New(Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		RepoProgressPath: repoPath,
		AgentSpecs:       []string{"ge:1"},
		Iterations:       1,
		Stdout:           &stdout,
		Stderr:           &stderr,
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	results, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("unexpected results length: %d", len(results))
	}
	if got := stdout.String(); got != "OK\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "sessions", "session-1", "terminal.log"))
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if !strings.Contains(log, `{"response":"OK","stats":{}}`) {
		t.Fatalf("session transcript missing raw Gemini JSON: %s", log)
	}
	if !strings.Contains(log, "Gemini noise") {
		t.Fatalf("session transcript missing Gemini stderr: %s", log)
	}

	for _, path := range []string{BatchLogPath(dataDir, 1), RepoBatchLogPath(workspaceDir, 1)} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		log := string(data)
		if !strings.Contains(log, "OK\n") {
			t.Fatalf("batch log %s missing final response: %s", path, log)
		}
		if strings.Contains(log, `{"response":"OK","stats":{}}`) || strings.Contains(log, "Gemini noise") {
			t.Fatalf("batch log %s should contain only filtered output, got: %s", path, log)
		}
	}
}

func TestRunnerOpenCodeWritesOnlyFinalResponseToStdoutAndLogsRawEvents(t *testing.T) {
	workspaceDir := t.TempDir()
	binDir := filepath.Join(workspaceDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	opencodeScript := "#!/usr/bin/env bash\nprintf '%s\\n' 'Opencode noise' >&2\nprintf '%s\\n' '{\"type\":\"tool_use\",\"part\":{\"type\":\"tool\",\"tool\":\"grep\",\"state\":{\"output\":\"raw tool output\"}}}'\nprintf '%s\\n' '{\"type\":\"text\",\"part\":{\"type\":\"text\",\"messageID\":\"msg_1\",\"text\":\"FINAL\"}}'\n"
	opencodePath := filepath.Join(binDir, "opencode")
	if err := os.WriteFile(opencodePath, []byte(opencodeScript), 0o755); err != nil {
		t.Fatal(err)
	}

	dataDir := filepath.Join(workspaceDir, "data")
	repoPath := filepath.Join(workspaceDir, "docs", "orchestration", "rally-progress.yaml")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := New(Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		RepoProgressPath: repoPath,
		AgentSpecs:       []string{"op:1"},
		Iterations:       1,
		Stdout:           &stdout,
		Stderr:           &stderr,
	})
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	results, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("unexpected results length: %d", len(results))
	}
	if got := stdout.String(); got != "FINAL\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if strings.Contains(stdout.String(), "tool_use") || strings.Contains(stdout.String(), "raw tool output") {
		t.Fatalf("stdout should only contain final response: %s", stdout.String())
	}

	sessionData, err := os.ReadFile(filepath.Join(dataDir, "sessions", "session-1", "terminal.log"))
	if err != nil {
		t.Fatal(err)
	}
	sessionLog := string(sessionData)
	if !strings.Contains(sessionLog, "raw tool output") || !strings.Contains(sessionLog, "Opencode noise") {
		t.Fatalf("session transcript missing raw Opencode history: %s", sessionLog)
	}

	for _, path := range []string{BatchLogPath(dataDir, 1), RepoBatchLogPath(workspaceDir, 1)} {
		batchData, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		batchLog := string(batchData)
		if !strings.Contains(batchLog, "rally: batch 1") || !strings.Contains(batchLog, "FINAL\n") {
			t.Fatalf("batch log %s missing expected filtered history: %s", path, batchLog)
		}
		if strings.Contains(batchLog, "tool_use") || strings.Contains(batchLog, "raw tool output") || strings.Contains(batchLog, "Opencode noise") {
			t.Fatalf("batch log %s should contain only filtered output, got: %s", path, batchLog)
		}
	}
}

func TestPruneRepoBatchLogsKeepsLatestTen(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()
	for i := 1; i <= 12; i++ {
		path := RepoBatchLogPath(workspaceDir, i)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("log\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := pruneRepoBatchLogs(workspaceDir, 10); err != nil {
		t.Fatalf("pruneRepoBatchLogs returned error: %v", err)
	}
	for i := 1; i <= 2; i++ {
		if _, err := os.Stat(RepoBatchLogPath(workspaceDir, i)); !os.IsNotExist(err) {
			t.Fatalf("expected batch %d pruned, err=%v", i, err)
		}
	}
	for i := 3; i <= 12; i++ {
		if _, err := os.Stat(RepoBatchLogPath(workspaceDir, i)); err != nil {
			t.Fatalf("expected batch %d kept: %v", i, err)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
	return string(output)
}
