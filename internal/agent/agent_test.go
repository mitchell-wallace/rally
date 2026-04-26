package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/testutil"
)

func TestBuildPrompt_AllFields(t *testing.T) {
	opts := RunOptions{
		Persona:          "Expert Go developer",
		TaskName:         "Refactor store layer",
		TaskRequirements: "Use generics for JSONL records.",
		InboxMessage:     "Urgent: fix race condition.",
		PreviousSummary:  "Added basic cache.",
		RecentTryContext: "Try #5 failed with timeout.",
	}
	p := BuildPrompt(opts)
	if p == "" {
		t.Fatal("expected non-empty prompt")
	}
	checks := []string{
		"Expert Go developer",
		"Refactor store layer",
		"Use generics for JSONL records.",
		"Urgent: fix race condition.",
		"Added basic cache.",
		"Try #5 failed with timeout.",
		".rally/README.md",
	}
	for _, c := range checks {
		if !strings.Contains(p, c) {
			t.Errorf("prompt missing %q", c)
		}
	}
}

func TestBuildPrompt_ExplicitOverride(t *testing.T) {
	opts := RunOptions{
		Prompt: "CUSTOM PROMPT",
		Persona: "ignored",
	}
	p := BuildPrompt(opts)
	if p != "CUSTOM PROMPT" {
		t.Fatalf("expected explicit prompt, got %q", p)
	}
}

func TestBuildPrompt_PreviousSummary(t *testing.T) {
	opts := RunOptions{
		TaskName:        "Foo",
		PreviousSummary: "Bar",
	}
	p := BuildPrompt(opts)
	if !strings.Contains(p, "Previous Summary:") {
		t.Error("expected Previous Summary section")
	}
	if !strings.Contains(p, "Bar") {
		t.Error("expected summary text")
	}
}

func TestFixtureExecutor_RoundTrip(t *testing.T) {
	tmp := t.TempDir()

	// init git repo
	testutil.InitGitRepo(t, tmp)

	// create a file to diff
	origPath := filepath.Join(tmp, "hello.txt")
	if err := os.WriteFile(origPath, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustExec(t, tmp, "git", "add", "hello.txt")
	mustExec(t, tmp, "git", "commit", "-m", "init")

	// create diff
	if err := os.WriteFile(origPath, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(tmp, "change.diff")
	out, err := exec.Command("git", "-C", tmp, "diff", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git diff failed: %v\n%s", err, out)
	}
	if err := os.WriteFile(diffPath, out, 0644); err != nil {
		t.Fatal(err)
	}
	// reset file so diff can apply
	if err := os.WriteFile(origPath, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// create output JSON
	outputPath := filepath.Join(tmp, "output.json")
	outputData := `{"completed":true,"summary":"done","remaining_work":""}`
	if err := os.WriteFile(outputPath, []byte(outputData), 0644); err != nil {
		t.Fatal(err)
	}

	fex := &FixtureExecutor{
		DiffPath:   diffPath,
		OutputPath: outputPath,
		Delay:      10 * time.Millisecond,
	}

	res, err := fex.Execute(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("fixture execute failed: %v", err)
	}
	if !res.Completed {
		t.Error("expected completed")
	}
	if res.Summary != "done" {
		t.Errorf("expected summary 'done', got %q", res.Summary)
	}

	// verify file changed
	b, _ := os.ReadFile(origPath)
	if string(b) != "hello world\n" {
		t.Errorf("expected file to be patched, got %q", string(b))
	}

	// second execution should skip re-application because diff already applied
	res2, err := fex.Execute(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("second execute failed: %v", err)
	}
	if !res2.Completed {
		t.Error("expected completed on second run")
	}
}

func TestParseClaudeOutput_Valid(t *testing.T) {
	out := []byte(`{"type":"result","result":{"completed":true,"summary":"ok"}}`)
	tr, err := parseClaudeResult(out, nil)
	if err != nil {
		t.Fatal(err)
	}
	// No resultRaw found case
	if tr.Completed {
		// because resultRaw was nil, completed should be false
		t.Error("expected incomplete when no resultRaw")
	}

	// Now with resultRaw
	tr, err = parseClaudeResult(out, []byte(`{"completed":true,"summary":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed")
	}
	if tr.Summary != "ok" {
		t.Errorf("expected summary 'ok', got %q", tr.Summary)
	}
}

func TestParseClaudeOutput_Malformed(t *testing.T) {
	out := []byte(`this is not json`)
	tr, err := parseClaudeResult(out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected not completed")
	}
	if !strings.Contains(tr.Summary, "not json") {
		t.Errorf("expected raw output in summary, got %q", tr.Summary)
	}
}

func TestParseClaudeOutput_MissingResultField(t *testing.T) {
	out := []byte(`{"type":"ping"}`)
	tr, err := parseClaudeResult(out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected not completed")
	}
}

func TestParseGeminiOutput_Valid(t *testing.T) {
	out := []byte(`{"response":"{\"completed\":true,\"summary\":\"hello\"}","session_id":"abc","stats":{}}`)
	tr, err := parseGeminiOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed")
	}
	if tr.Summary != "hello" {
		t.Errorf("expected summary 'hello', got %q", tr.Summary)
	}
}

func TestParseGeminiOutput_MissingResponse(t *testing.T) {
	out := []byte(`{"session_id":"abc","stats":{}}`)
	tr, err := parseGeminiOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected not completed")
	}
}

func TestParseOpenCodeOutput_Valid(t *testing.T) {
	parts := []string{`{"completed":true,"summary":"text1"}`}
	tr, err := parseOpenCodeOutput(nil, parts)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed")
	}
	if tr.Summary != "text1" {
		t.Errorf("expected summary 'text1', got %q", tr.Summary)
	}
}

func TestParseOpenCodeOutput_MissingText(t *testing.T) {
	tr, err := parseOpenCodeOutput([]byte("some output"), []string{})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected not completed")
	}
	if tr.Summary != "some output" {
		t.Errorf("expected raw output in summary, got %q", tr.Summary)
	}
}

func TestGitHelpers(t *testing.T) {
	tmp := t.TempDir()
	mustExec(t, tmp, "git", "init")

	root, ok, err := gitx.GitRepoRoot(tmp)
	if err != nil {
		t.Fatalf("GitRepoRoot error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok")
	}
	if !strings.HasSuffix(root, filepath.Base(tmp)) {
		t.Errorf("unexpected root: %s", root)
	}

	// GitUserFallbackConfig when not configured
	fallback := gitx.GitUserFallbackConfig(tmp)
	if len(fallback) == 0 {
		t.Error("expected fallback config")
	}

	// configure user
	mustExec(t, tmp, "git", "config", "user.name", "A")
	mustExec(t, tmp, "git", "config", "user.email", "a@b")
	fallback = gitx.GitUserFallbackConfig(tmp)
	if len(fallback) != 0 {
		t.Error("expected no fallback when configured")
	}
}

func mustExec(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}
