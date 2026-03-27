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

	agentScript := "#!/usr/bin/env bash\nprintf '%s\n' \"${@: -1}\"\n"
	for _, name := range []string{"claude", "codex"} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte(agentScript), 0o755); err != nil {
			t.Fatal(err)
		}
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
			want:   []string{"claude", "-p", "--dangerously-skip-permissions", "--model", "sonnet", "--output-format", "text", "prompt"},
			stderr: false,
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
			want:   []string{"opencode", "run", "--model", "anthropic/claude-sonnet-4", "prompt"},
			stderr: false,
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

	want := []string{"opencode", "run", "prompt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command mismatch:\n got %#v\nwant %#v", got, want)
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

	hash, err := autoCommitWorkspace(repo, 3, 2, "codex")
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

func TestAutoCommitWorkspaceSkipsCleanRepo(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Rally Test")
	runGit(t, repo, "config", "user.email", "rally@example.com")

	hash, err := autoCommitWorkspace(repo, 1, 1, "claude")
	if err != nil {
		t.Fatalf("autoCommitWorkspace returned error: %v", err)
	}
	if hash != "" {
		t.Fatalf("expected empty hash for clean repo, got %q", hash)
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
	if !strings.Contains(log, "OK\n") {
		t.Fatalf("session transcript missing final response: %s", log)
	}
	if strings.Contains(log, `{"response":"OK","stats":{}}`) {
		t.Fatalf("session transcript should not include raw Gemini JSON: %s", log)
	}
	if !strings.Contains(log, "Gemini noise") {
		t.Fatalf("session transcript missing Gemini stderr: %s", log)
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
