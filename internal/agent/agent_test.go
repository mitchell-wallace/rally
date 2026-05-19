package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
		Instructions:     "Always write tests first.",
		TaskPrompt:       "Fix the caching bug.",
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
		"Always write tests first.",
		"## Project Instructions",
		"Fix the caching bug.",
		"## Task",
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
		Prompt:  "CUSTOM PROMPT",
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

func TestBuildPrompt_Instructions(t *testing.T) {
	opts := RunOptions{
		Instructions: "Always use TDD.",
	}
	p := BuildPrompt(opts)
	if !strings.Contains(p, "## Project Instructions") {
		t.Error("expected ## Project Instructions section")
	}
	if !strings.Contains(p, "Always use TDD.") {
		t.Error("expected instructions text")
	}
}

func TestBuildPrompt_RoleInstructionsBetweenProjectInstructionsAndTask(t *testing.T) {
	opts := RunOptions{
		Instructions:     "Base instructions.",
		RoleInstructions: "Role instructions.",
		TaskPrompt:       "Task body.",
	}
	p := BuildPrompt(opts)

	projectIndex := strings.Index(p, "## Project Instructions\nBase instructions.")
	roleIndex := strings.Index(p, "## Role Instructions\nRole instructions.")
	taskIndex := strings.Index(p, "## Task\nTask body.")
	if projectIndex == -1 || roleIndex == -1 || taskIndex == -1 {
		t.Fatalf("prompt missing expected sections:\n%s", p)
	}
	if !(projectIndex < roleIndex && roleIndex < taskIndex) {
		t.Fatalf("expected project instructions before role instructions before task, got:\n%s", p)
	}
}

func TestBuildPrompt_TaskPrompt(t *testing.T) {
	opts := RunOptions{
		TaskPrompt: "Fix the race condition.",
	}
	p := BuildPrompt(opts)
	if !strings.Contains(p, "## Task") {
		t.Error("expected ## Task section")
	}
	if !strings.Contains(p, "Fix the race condition.") {
		t.Error("expected task prompt text")
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

func TestParseClaudeOutput_CompletedFalse(t *testing.T) {
	out := []byte(`{"type":"result","result":{"completed":false,"summary":"not done"}}`)
	tr, err := parseClaudeResult(out, []byte(`{"completed":false,"summary":"not done"}`))
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected not completed when agent reports false")
	}
	if tr.Summary != "not done" {
		t.Errorf("expected summary 'not done', got %q", tr.Summary)
	}
}

func TestParseClaudeOutput_MalformedJSON(t *testing.T) {
	out := []byte(`some output`)
	tr, err := parseClaudeResult(out, []byte(`not-json-at-all`))
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed fallback for malformed JSON")
	}
	if tr.Summary != "not-json-at-all" {
		t.Errorf("expected raw result in summary, got %q", tr.Summary)
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

func TestParseGeminiOutput_CompletedFalse(t *testing.T) {
	out := []byte(`{"response":"{\"completed\":false,\"summary\":\"still working\"}","session_id":"abc","stats":{}}`)
	tr, err := parseGeminiOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected not completed when agent reports false")
	}
	if tr.Summary != "still working" {
		t.Errorf("expected summary 'still working', got %q", tr.Summary)
	}
}

func TestParseGeminiOutput_MalformedJSON(t *testing.T) {
	out := []byte(`{"response":"not json content","session_id":"abc","stats":{}}`)
	tr, err := parseGeminiOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed fallback for malformed inner JSON")
	}
	if tr.Summary != "not json content" {
		t.Errorf("expected raw response in summary, got %q", tr.Summary)
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

func TestParseOpenCodeOutput_CompletedFalse(t *testing.T) {
	parts := []string{`{"completed":false,"summary":"not yet"}`}
	tr, err := parseOpenCodeOutput(nil, parts)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected not completed when agent reports false")
	}
	if tr.Summary != "not yet" {
		t.Errorf("expected summary 'not yet', got %q", tr.Summary)
	}
}

func TestParseOpenCodeOutput_MalformedJSON(t *testing.T) {
	parts := []string{`garbled output`}
	tr, err := parseOpenCodeOutput(nil, parts)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed fallback for malformed JSON")
	}
	if tr.Summary != "garbled output" {
		t.Errorf("expected raw text in summary, got %q", tr.Summary)
	}
}

// TestParseOpenCodeOutput_TrimsWhitespace guards against the minimax-m2.5-free
// behaviour where the model emits multiple "\n\n\n" text steps before the
// final answer. The streamed parts get joined verbatim, so the fallback
// summary used to start with ~11 newlines. We trim them.
func TestParseOpenCodeOutput_TrimsWhitespace(t *testing.T) {
	parts := []string{"\n\n\n", "\n\n\n", "\n\nDone! file created."}
	tr, err := parseOpenCodeOutput(nil, parts)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed fallback")
	}
	if tr.Summary != "Done! file created." {
		t.Errorf("expected trimmed summary, got %q", tr.Summary)
	}
}

// TestOpenCodeJSONEventExtraction verifies that text is read from part.text
// (not the top-level text field, which is always absent in the opencode JSONL stream).
func TestOpenCodeJSONEventExtraction(t *testing.T) {
	jsonl := `{"type":"step_start","part":{"type":"step-start"}}
{"type":"text","part":{"type":"text","text":"{\"completed\":true,\"summary\":\"task done\"}"}}
{"type":"step_finish","part":{"type":"step-finish","reason":"stop"}}`

	var textParts []string
	scanner := bufio.NewScanner(strings.NewReader(jsonl))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev opencodeJSONEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == "text" && ev.Part.Text != "" {
			textParts = append(textParts, ev.Part.Text)
		}
	}

	if len(textParts) != 1 {
		t.Fatalf("expected 1 text part, got %d", len(textParts))
	}
	tr, err := parseOpenCodeOutput(nil, textParts)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed=true")
	}
	if tr.Summary != "task done" {
		t.Errorf("expected summary 'task done', got %q", tr.Summary)
	}
}

func TestParseCodexResult_Valid(t *testing.T) {
	data := []byte(`{"completed":true,"summary":"done"}`)
	tr, err := parseCodexResult(data)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed")
	}
	if tr.Summary != "done" {
		t.Errorf("expected summary 'done', got %q", tr.Summary)
	}
}

func TestParseCodexResult_CompletedFalse(t *testing.T) {
	data := []byte(`{"completed":false,"summary":"still going"}`)
	tr, err := parseCodexResult(data)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected not completed when agent reports false")
	}
	if tr.Summary != "still going" {
		t.Errorf("expected summary 'still going', got %q", tr.Summary)
	}
}

func TestParseCodexResult_Malformed(t *testing.T) {
	data := []byte(`not valid json`)
	tr, err := parseCodexResult(data)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed fallback for malformed JSON")
	}
	if tr.Summary != "not valid json" {
		t.Errorf("expected raw data in summary, got %q", tr.Summary)
	}
}

func TestWriteCodexSchema(t *testing.T) {
	path, err := writeCodexSchema()
	if err != nil {
		t.Fatalf("writeCodexSchema failed: %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading schema file: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty schema file")
	}
	required := []string{"completed", "summary", "remaining_work", "message_addressed", "files_changed"}
	for _, r := range required {
		if !strings.Contains(string(data), r) {
			t.Errorf("schema missing field %q", r)
		}
	}
}

func TestGitHelpers(t *testing.T) {
	tmp := t.TempDir()
	mustExec(t, tmp, "git", "init")

	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

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

func TestRunLoggedCommandStreamsTryLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "try.log")
	cmd := exec.Command("sh", "-c", "printf 'first\\n'; sleep 0.3; printf 'second\\n'")

	type result struct {
		out []byte
		err error
	}
	resultCh := make(chan result, 1)
	started := make(chan int, 1)
	go func() {
		out, err := runLoggedCommand(cmd, logPath, false, func(pid int) {
			started <- pid
		})
		resultCh <- result{out: out, err: err}
	}()

	select {
	case pid := <-started:
		if pid <= 0 {
			t.Fatalf("expected child pid, got %d", pid)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for process start")
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		data, err := os.ReadFile(logPath)
		if err == nil && bytes.Contains(data, []byte("first\n")) && !bytes.Contains(data, []byte("second\n")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("log did not update while command was still running; latest contents: %q", string(data))
		}
		time.Sleep(25 * time.Millisecond)
	}

	res := <-resultCh
	if res.err != nil {
		t.Fatalf("runLoggedCommand failed: %v", res.err)
	}
	if string(res.out) != "first\nsecond\n" {
		t.Fatalf("unexpected combined output: %q", string(res.out))
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if string(logData) != "first\nsecond\n" {
		t.Fatalf("unexpected log contents: %q", string(logData))
	}
}

func TestAdapterCapabilityDefaults(t *testing.T) {
	adapters := map[string]Executor{
		"generic": &GenericExecutor{},
		"fixture": &FixtureExecutor{},
	}

	for name, adapter := range adapters {
		t.Run(name, func(t *testing.T) {
			if adapter.ResumeSupported() {
				t.Error("ResumeSupported() = true, want false")
			}
			if adapter.RotateSupported() {
				t.Error("RotateSupported() = true, want false")
			}
			if adapter.LivenessProbeSupported() {
				t.Error("LivenessProbeSupported() = true, want false")
			}
			if err := adapter.RotateModel("new-model"); err == nil {
				t.Error("RotateModel() = nil, want error")
			}
			ok, err := adapter.ProbeLiveness(context.Background())
			if ok {
				t.Error("ProbeLiveness() = true, want false")
			}
			if err == nil {
				t.Error("ProbeLiveness() err = nil, want error")
			}
		})
	}
}

func TestClaudeAdapterCapabilities(t *testing.T) {
	c := &ClaudeExecutor{}
	if !c.ResumeSupported() {
		t.Error("ResumeSupported() = false, want true")
	}
	if c.RotateSupported() {
		t.Error("RotateSupported() = true, want false")
	}
	if c.LivenessProbeSupported() {
		t.Error("LivenessProbeSupported() = true, want false")
	}
	if err := c.RotateModel("new-model"); err == nil {
		t.Error("RotateModel() should return error")
	}
}

func TestClaudeAdapter_SessionIDCapture(t *testing.T) {
	out := []byte(`{"type":"system","session_id":"sess-abc-123"}
{"type":"result","result":{"completed":true,"summary":"ok"}}`)
	resultRaw, sessionID, _ := scanClaudeOutput(out)
	if sessionID != "sess-abc-123" {
		t.Errorf("sessionID = %q, want %q", sessionID, "sess-abc-123")
	}
	if resultRaw == nil {
		t.Error("resultRaw = nil, expected non-nil")
	}
}

func TestClaudeAdapter_SessionIDEmptyWhenAbsent(t *testing.T) {
	out := []byte(`{"type":"result","result":{"completed":true,"summary":"ok"}}`)
	_, sessionID, _ := scanClaudeOutput(out)
	if sessionID != "" {
		t.Errorf("sessionID = %q, want empty", sessionID)
	}
}

func TestClaudeAdapter_ScanClaudeOutputNoResult(t *testing.T) {
	out := []byte(`{"type":"system","session_id":"sess-no-result"}`)
	resultRaw, sessionID, _ := scanClaudeOutput(out)
	if sessionID != "sess-no-result" {
		t.Errorf("sessionID = %q, want %q", sessionID, "sess-no-result")
	}
	if resultRaw != nil {
		t.Error("resultRaw should be nil when no result event")
	}
}

func TestClaudeAdapter_CountsToolUseBlocks(t *testing.T) {
	out := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"t1","name":"Bash"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Read"},{"type":"tool_use","id":"t3","name":"Bash"}]}}
{"type":"result","result":{"completed":true,"summary":"done"}}`)
	_, _, toolCalls := scanClaudeOutput(out)
	if toolCalls != 3 {
		t.Errorf("toolCalls = %d, want 3", toolCalls)
	}
}

func TestCodexAdapter_CountsToolItems(t *testing.T) {
	out := []byte(`{"type":"thread.started","thread_id":"abc"}
{"type":"item.completed","item":{"type":"command_execution","id":"i1"}}
{"type":"item.completed","item":{"type":"file_change","id":"i2"}}
{"type":"item.completed","item":{"type":"agent_message","id":"i3"}}
{"type":"item.completed","item":{"type":"command_execution","id":"i4"}}
{"type":"turn.completed"}`)
	sessionID, toolCalls := scanCodexEvents(out)
	if sessionID != "abc" {
		t.Errorf("sessionID = %q, want abc", sessionID)
	}
	if toolCalls != 3 {
		t.Errorf("toolCalls = %d, want 3 (2 command_execution + 1 file_change)", toolCalls)
	}
}

func TestGeminiAdapter_CountsToolCallsFromStats(t *testing.T) {
	out := []byte(`{"session_id":"s","response":"hello","stats":{"tools":{"totalCalls":5}}}`)
	tr, err := parseGeminiOutput(out)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if tr.ToolCalls != 5 {
		t.Errorf("toolCalls = %d, want 5", tr.ToolCalls)
	}
}

func TestOpenCodeAdapter_CountsToolUseEvents(t *testing.T) {
	out := []byte(`{"type":"tool_use","part":{"type":"tool","tool":"read"}}
{"type":"tool_use","part":{"type":"tool","tool":"write"}}
{"type":"text","part":{"type":"text","text":"done"}}`)
	// parseOpenCodeOutput doesn't take a tool count; the count is done in Execute.
	// Simulate the Execute loop logic.
	textParts := []string{"done"}
	toolCalls := 0
	// The opencode adapter increments on type=tool_use OR part.type=tool.
	// Count by hand here mirroring the implementation, then verify parser works.
	for _, line := range []string{
		`{"type":"tool_use","part":{"type":"tool","tool":"read"}}`,
		`{"type":"tool_use","part":{"type":"tool","tool":"write"}}`,
		`{"type":"text","part":{"type":"text","text":"done"}}`,
	} {
		var ev opencodeJSONEvent
		_ = json.Unmarshal([]byte(line), &ev)
		if ev.Type == "tool_use" || ev.Part.Type == "tool" {
			toolCalls++
		}
	}
	if toolCalls != 2 {
		t.Errorf("toolCalls counted = %d, want 2", toolCalls)
	}
	// Parser still works.
	tr, err := parseOpenCodeOutput(out, textParts)
	if err != nil || tr == nil {
		t.Fatalf("parse failed: %v", err)
	}
}

func TestCodexAdapterCapabilities(t *testing.T) {
	c := &CodexExecutor{}
	if !c.ResumeSupported() {
		t.Error("ResumeSupported() = false, want true")
	}
	if c.RotateSupported() {
		t.Error("RotateSupported() = true, want false")
	}
	if !c.LivenessProbeSupported() {
		t.Error("LivenessProbeSupported() = false, want true")
	}
	if err := c.RotateModel("new-model"); err == nil {
		t.Error("RotateModel() should return error")
	}
}

func TestCodexAdapter_SessionIDCapture(t *testing.T) {
	out := []byte(`{"type":"thread.started","thread_id":"codex-session-123"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"OK"}}`)
	if got := scanCodexSessionID(out); got != "codex-session-123" {
		t.Fatalf("scanCodexSessionID() = %q, want %q", got, "codex-session-123")
	}
}

func TestOpenCodeAdapterCapabilities(t *testing.T) {
	o := &OpenCodeExecutor{Model: "initial-model"}
	if !o.ResumeSupported() {
		t.Error("ResumeSupported() = false, want true")
	}
	if !o.RotateSupported() {
		t.Error("RotateSupported() = false, want true")
	}
	if o.LivenessProbeSupported() {
		t.Error("LivenessProbeSupported() = true, want false")
	}
}

func TestOpenCodeAdapter_RotateModel(t *testing.T) {
	o := &OpenCodeExecutor{Model: "original-model"}
	if err := o.RotateModel("new-model"); err != nil {
		t.Fatalf("RotateModel() returned error: %v", err)
	}
	if o.Model != "new-model" {
		t.Errorf("Model = %q, want %q", o.Model, "new-model")
	}
}

func TestGeminiAdapterCapabilities(t *testing.T) {
	g := &GeminiExecutor{}
	if !g.ResumeSupported() {
		t.Error("ResumeSupported() = false, want true")
	}
	if g.RotateSupported() {
		t.Error("RotateSupported() = true, want false")
	}
	if g.LivenessProbeSupported() {
		t.Error("LivenessProbeSupported() = true, want false")
	}
	if err := g.RotateModel("new-model"); err == nil {
		t.Error("RotateModel() should return error")
	}
}

func TestGeminiAdapter_SessionIDCapture(t *testing.T) {
	out := []byte(`{"response":"{\"completed\":true,\"summary\":\"hello\"}","session_id":"gem-sess-456","stats":{}}`)
	tr, err := parseGeminiOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	if tr.SessionID != "gem-sess-456" {
		t.Errorf("SessionID = %q, want %q", tr.SessionID, "gem-sess-456")
	}
}

func TestGeminiAdapter_SessionIDOnMissingResponse(t *testing.T) {
	out := []byte(`{"session_id":"gem-sess-789","stats":{}}`)
	tr, err := parseGeminiOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	if tr.SessionID != "gem-sess-789" {
		t.Errorf("SessionID = %q, want %q", tr.SessionID, "gem-sess-789")
	}
}

func TestClassifyGeminiExit(t *testing.T) {
	cases := []struct {
		name     string
		exitCode int
		want     string
	}{
		{"auth", 41, "authentication"},
		{"input", 42, "invalid CLI input"},
		{"sandbox", 44, "sandbox"},
		{"config", 52, "config error"},
		{"turn limit", 53, "turn limit"},
		{"tool exec", 54, "tool execution"},
		{"untrusted", 55, "workspace not trusted"},
		{"cancel", 130, "cancelled"},
		{"unknown", 99, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Build a synthetic *exec.ExitError by running `sh -c exit <code>`.
			cmd := exec.Command("sh", "-c", fmt.Sprintf("exit %d", tc.exitCode))
			err := cmd.Run()
			got := classifyGeminiExit(err, "")
			if tc.want == "" {
				if got != "" {
					t.Errorf("classifyGeminiExit(%d) = %q, want empty", tc.exitCode, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("classifyGeminiExit(%d) = %q, want substring %q", tc.exitCode, got, tc.want)
			}
		})
	}
}

func TestTailString(t *testing.T) {
	if got := tailString("hello", 100); got != "hello" {
		t.Errorf("tailString short = %q, want hello", got)
	}
	if got := tailString("  hello  ", 100); got != "hello" {
		t.Errorf("tailString trimmed = %q, want hello", got)
	}
	got := tailString("abcdefghij", 4)
	if got != "…ghij" {
		t.Errorf("tailString long = %q, want …ghij", got)
	}
}

func TestTryResultSessionIDField(t *testing.T) {
	tr := &TryResult{Completed: true, Summary: "test", SessionID: "sess-123"}
	if tr.SessionID != "sess-123" {
		t.Errorf("SessionID = %q, want %q", tr.SessionID, "sess-123")
	}

	trZero := &TryResult{Completed: true}
	if trZero.SessionID != "" {
		t.Errorf("SessionID = %q, want empty string", trZero.SessionID)
	}
}
