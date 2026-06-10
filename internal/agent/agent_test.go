package agent

import (
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

	"github.com/mitchell-wallace/rally/internal/agent_prompt"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/reliability"
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

func TestBuildPrompt_SharedGuidanceIncludedWhenLapsEnabled(t *testing.T) {
	opts := RunOptions{
		TaskName:         "Do the thing",
		RoleInstructions: "Role instructions.",
		LapsEnabled:      true,
	}
	p := BuildPrompt(opts)

	// The shared general/ snippets must always be composed into a laps-driven
	// agent prompt, sourced verbatim from the embedded agent_prompt package.
	if !strings.Contains(p, agent_prompt.Headless()) {
		t.Errorf("prompt missing shared headless guidance:\n%s", p)
	}
	if !strings.Contains(p, agent_prompt.Finalize()) {
		t.Errorf("prompt missing shared finalize guidance:\n%s", p)
	}
	// The role slot and existing task context survive alongside the snippets.
	if !strings.Contains(p, "## Role Instructions\nRole instructions.") {
		t.Errorf("prompt missing role slot:\n%s", p)
	}
	if !strings.Contains(p, "## Run Exit Conditions") {
		t.Errorf("prompt missing existing exit-conditions section:\n%s", p)
	}
}

func TestBuildPrompt_SharedGuidanceOmittedInNoBackendMode(t *testing.T) {
	opts := RunOptions{
		TaskName:    "Do the thing",
		LapsEnabled: false,
	}
	p := BuildPrompt(opts)

	// No-backend behavior is preserved: the laps-specific shared snippets are
	// not injected, and the documented `rally progress` exit action remains.
	if strings.Contains(p, agent_prompt.Finalize()) {
		t.Errorf("no-backend prompt should not include finalize guidance:\n%s", p)
	}
	if strings.Contains(p, agent_prompt.Headless()) {
		t.Errorf("no-backend prompt should not include headless guidance:\n%s", p)
	}
	if !strings.Contains(p, "rally progress --summary") {
		t.Errorf("no-backend prompt missing rally progress exit action:\n%s", p)
	}
}

func TestBuildPrompt_ExplicitOverrideSkipsSharedGuidance(t *testing.T) {
	opts := RunOptions{
		Prompt:      "CUSTOM PROMPT",
		LapsEnabled: true,
	}
	p := BuildPrompt(opts)
	if p != "CUSTOM PROMPT" {
		t.Fatalf("explicit override not preserved verbatim, got %q", p)
	}
}

func TestBuildPrompt_SharedGuidanceOrdering(t *testing.T) {
	opts := RunOptions{
		Persona:          "claude",
		TaskName:         "Do the thing",
		RoleInstructions: "Role instructions.",
		TaskPrompt:       "Task body.",
		LapsEnabled:      true,
	}
	p := BuildPrompt(opts)

	headlessIndex := strings.Index(p, agent_prompt.Headless())
	finalizeIndex := strings.Index(p, agent_prompt.Finalize())
	taskNameIndex := strings.Index(p, "Task: Do the thing")
	taskBodyIndex := strings.Index(p, "## Task\nTask body.")
	exitIndex := strings.Index(p, "## Run Exit Conditions")
	if headlessIndex == -1 || finalizeIndex == -1 || taskNameIndex == -1 || taskBodyIndex == -1 || exitIndex == -1 {
		t.Fatalf("prompt missing expected sections:\n%s", p)
	}

	// Reusable general snippets are appended ahead of the task context, and the
	// up-front finalize guidance precedes the exit-conditions block.
	if !(headlessIndex < taskNameIndex && finalizeIndex < taskNameIndex) {
		t.Fatalf("expected shared general snippets before task context:\n%s", p)
	}
	if !(finalizeIndex < exitIndex) {
		t.Fatalf("expected finalize wrapup guidance up front, before exit conditions:\n%s", p)
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
	if tr.Summary != claudeNoResultSummary {
		t.Errorf("summary = %q, want %q", tr.Summary, claudeNoResultSummary)
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
	if tr.Summary != claudeNoResultSummary {
		t.Errorf("summary = %q, want %q", tr.Summary, claudeNoResultSummary)
	}
	if strings.Contains(tr.Summary, "not json") {
		t.Errorf("summary leaked raw output: %q", tr.Summary)
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
	if tr.Summary != claudeNoResultSummary {
		t.Errorf("summary = %q, want %q", tr.Summary, claudeNoResultSummary)
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
	if tr.Completed {
		t.Error("expected malformed structured result to remain incomplete")
	}
	if tr.Summary != claudeMalformedResultSummary {
		t.Errorf("summary = %q, want %q", tr.Summary, claudeMalformedResultSummary)
	}
	if strings.Contains(tr.Summary, "not-json-at-all") {
		t.Errorf("summary leaked raw result: %q", tr.Summary)
	}
}

func TestParseClaudeOutput_MissingStructuredSummary(t *testing.T) {
	tr, err := parseClaudeResult(nil, []byte(`{"completed":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected result without summary to remain incomplete")
	}
	if tr.Summary != claudeMissingSummary {
		t.Errorf("summary = %q, want %q", tr.Summary, claudeMissingSummary)
	}
}

func TestParseClaudeOutput_BoundsFinalTextFallback(t *testing.T) {
	finalText := strings.Repeat("start ", executorFinalTextRuneLimit) + "useful tail"
	resultRaw, err := json.Marshal(finalText)
	if err != nil {
		t.Fatal(err)
	}

	tr, err := parseClaudeResult(nil, resultRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected final assistant text fallback to be completed")
	}
	if got := len([]rune(tr.Summary)); got > executorFinalTextRuneLimit {
		t.Fatalf("summary rune length = %d, want <= %d", got, executorFinalTextRuneLimit)
	}
	if !strings.Contains(tr.Summary, "useful tail") {
		t.Errorf("summary = %q, want useful tail", tr.Summary)
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
	if tr.Summary != geminiMissingResponseSummary {
		t.Errorf("summary = %q, want %q", tr.Summary, geminiMissingResponseSummary)
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
		t.Errorf("expected final response text in summary, got %q", tr.Summary)
	}
}

func TestParseGeminiOutput_MalformedWrapperDoesNotLeakRawOutput(t *testing.T) {
	out := []byte(`raw transcript that must not leak`)
	tr, err := parseGeminiOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected malformed wrapper to remain incomplete")
	}
	if tr.Summary != geminiUnparseableOutputSummary {
		t.Errorf("summary = %q, want %q", tr.Summary, geminiUnparseableOutputSummary)
	}
	if strings.Contains(tr.Summary, "raw transcript") {
		t.Errorf("summary leaked raw output: %q", tr.Summary)
	}
}

func TestParseGeminiOutput_MissingStructuredSummary(t *testing.T) {
	out := []byte(`{"response":"{\"completed\":true}","session_id":"abc","stats":{"tools":{"totalCalls":2}}}`)
	tr, err := parseGeminiOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected response without summary to remain incomplete")
	}
	if tr.Summary != geminiMissingSummary {
		t.Errorf("summary = %q, want %q", tr.Summary, geminiMissingSummary)
	}
	if tr.ToolCalls != 2 {
		t.Errorf("tool calls = %d, want 2", tr.ToolCalls)
	}
}

func TestParseGeminiOutput_BoundsFinalTextFallback(t *testing.T) {
	finalText := strings.Repeat("start ", executorFinalTextRuneLimit) + "useful tail"
	out, err := json.Marshal(geminiWrapper{Response: finalText, SessionID: "abc"})
	if err != nil {
		t.Fatal(err)
	}

	tr, err := parseGeminiOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected final assistant text fallback to be completed")
	}
	if got := len([]rune(tr.Summary)); got > executorFinalTextRuneLimit {
		t.Fatalf("summary rune length = %d, want <= %d", got, executorFinalTextRuneLimit)
	}
	if !strings.Contains(tr.Summary, "useful tail") {
		t.Errorf("summary = %q, want useful tail", tr.Summary)
	}
}

func TestParseOpenCodeOutput_Valid(t *testing.T) {
	out := []byte(`{"type":"text","part":{"type":"text","text":"{\"completed\":true,\"summary\":\"text1\"}"}}`)
	tr, err := parseOpenCodeOutput(out, true)
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

func TestParseOpenCodeOutput_CapturesSessionID(t *testing.T) {
	out := []byte(`{"type":"step_start","sessionID":"ses_test123","part":{"type":"step-start"}}
{"type":"text","sessionID":"ses_test123","part":{"type":"text","text":"{\"completed\":true,\"summary\":\"ok\"}"}}`)
	tr, err := parseOpenCodeOutput(out, true)
	if err != nil {
		t.Fatal(err)
	}
	if tr.SessionID != "ses_test123" {
		t.Errorf("SessionID = %q, want %q", tr.SessionID, "ses_test123")
	}
}

// TestResumeSupportImpliesSessionCapture is a contract test across all executors:
// every harness reporting ResumeSupported()==true MUST also capture a session ID
// from realistic harness output. Capture is half the resume contract — without it,
// result.SessionID is always empty, the runner never has a session to feed back as
// the next attempt's ResumeSessionID, and resume silently no-ops even though the
// resume flag is wired (the opencode bug fixed in 0.8.7). Each resume-supporting
// harness needs an entry in `captures` proving its parse path extracts a non-empty
// session ID; a harness that does not claim resume support must NOT have one.
//
// Adding a new resume-supporting executor without a capture fixture fails this test,
// forcing the author to prove the session is actually captured end-to-end.
func TestResumeSupportImpliesSessionCapture(t *testing.T) {
	// Each extractor feeds a realistic, harness-specific output sample through the
	// executor's real capture path and returns the captured session ID.
	captures := map[string]func() string{
		"claude": func() string {
			out := []byte(`{"type":"system","session_id":"sess-claude-1"}
{"type":"result","session_id":"sess-claude-1","result":"{\"completed\":true,\"summary\":\"ok\"}"}`)
			_, sid, _ := scanClaudeOutput(out)
			return sid
		},
		"codex": func() string {
			out := []byte(`{"type":"thread.started","thread_id":"codex-sess-1"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"ok"}}`)
			sid, _ := scanCodexEvents(out)
			return sid
		},
		"opencode": func() string {
			out := []byte(`{"type":"step_start","sessionID":"ses_oc1","part":{"type":"step-start"}}
{"type":"text","sessionID":"ses_oc1","part":{"type":"text","text":"{\"completed\":true,\"summary\":\"ok\"}"}}`)
			tr, err := parseOpenCodeOutput(out, true)
			if err != nil {
				return ""
			}
			return tr.SessionID
		},
		"antigravity": func() string {
			return scanAntigravityConversationID([]byte("Print mode: conversation=11111111-2222-3333-4444-555555555555\n"))
		},
	}

	executors := map[string]Executor{
		"claude":      &ClaudeExecutor{},
		"codex":       &CodexExecutor{},
		"opencode":    &OpenCodeExecutor{},
		"antigravity": &AntigravityExecutor{},
		"gemini":      &GeminiExecutor{},
		"generic":     &GenericExecutor{},
		"fixture":     &FixtureExecutor{},
	}

	for name, exec := range executors {
		if !exec.ResumeSupported() {
			if _, ok := captures[name]; ok {
				t.Errorf("%s reports ResumeSupported()==false but has a session-capture fixture; either wire resume or drop the fixture", name)
			}
			continue
		}
		capture, ok := captures[name]
		if !ok {
			t.Errorf("%s reports ResumeSupported()==true but has no session-capture fixture — resume will silently no-op unless its parse path captures a session ID. Add a fixture proving capture.", name)
			continue
		}
		if sid := capture(); sid == "" {
			t.Errorf("%s reports ResumeSupported()==true but captured an empty session ID from realistic output — resume cannot fire", name)
		}
	}
}

func TestParseOpenCodeOutput_MissingText(t *testing.T) {
	for _, tc := range []struct {
		name        string
		out         []byte
		wantSummary string
	}{
		{
			name:        "empty",
			wantSummary: "opencode produced no output",
		},
		{
			name:        "unparseable",
			out:         []byte("raw transcript that must not leak"),
			wantSummary: "opencode produced no parseable JSON events",
		},
		{
			name:        "events without result",
			out:         []byte(`{"type":"step_start","part":{"type":"step-start"}}`),
			wantSummary: "opencode produced no parseable result",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, err := parseOpenCodeOutput(tc.out, true)
			if err != nil {
				t.Fatal(err)
			}
			if tr.Completed {
				t.Error("expected not completed")
			}
			if tr.Summary != tc.wantSummary {
				t.Errorf("Summary = %q, want %q", tr.Summary, tc.wantSummary)
			}
			if strings.Contains(tr.Summary, "raw transcript") {
				t.Errorf("summary leaked raw output: %q", tr.Summary)
			}
			if len([]rune(tr.Summary)) > openCodeFailureSummaryLimit {
				t.Errorf("summary length = %d, want <= %d", len([]rune(tr.Summary)), openCodeFailureSummaryLimit)
			}
		})
	}
}

func TestParseOpenCodeOutput_CompletedFalse(t *testing.T) {
	out := []byte(`{"type":"text","part":{"type":"text","text":"{\"completed\":false,\"summary\":\"not yet\"}"}}`)
	tr, err := parseOpenCodeOutput(out, true)
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

func TestParseOpenCodeOutput_PlainText(t *testing.T) {
	out := []byte(`{"type":"text","part":{"type":"text","text":"garbled output"}}`)
	tr, err := parseOpenCodeOutput(out, true)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed fallback for plain text")
	}
	if tr.Summary != "garbled output" {
		t.Errorf("expected assistant text in summary, got %q", tr.Summary)
	}
}

// TestParseOpenCodeOutput_TrimsWhitespace guards against the minimax-m2.5-free
// behaviour where the model emits multiple "\n\n\n" text steps before the
// final answer. The streamed parts get joined verbatim, so the fallback
// summary used to start with ~11 newlines. We trim them.
func TestParseOpenCodeOutput_TrimsWhitespace(t *testing.T) {
	out := []byte(`{"type":"text","part":{"type":"text","text":"\n\n\n"}}
{"type":"text","part":{"type":"text","text":"\n\n\n"}}
{"type":"text","part":{"type":"text","text":"\n\nDone! file created."}}`)
	tr, err := parseOpenCodeOutput(out, true)
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

// TestParseOpenCodeOutput_CapturedToolUseEventFamily follows the live
// opencode 1.15.11 event family recorded in spike-evidence.
func TestParseOpenCodeOutput_CapturedToolUseEventFamily(t *testing.T) {
	out := []byte(`{"type":"step_start","part":{"type":"step-start"}}
{"type":"tool_use","part":{"type":"tool","tool":"write"}}
{"type":"step_finish","part":{"type":"step-finish","reason":"tool-calls"}}
{"type":"step_start","part":{"type":"step-start"}}
{"type":"text","part":{"type":"text","text":"DONE_TOOLS"}}`)

	tr, err := parseOpenCodeOutput(out, true)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed")
	}
	if tr.Summary != "DONE_TOOLS" {
		t.Errorf("Summary = %q, want %q", tr.Summary, "DONE_TOOLS")
	}
	if tr.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", tr.ToolCalls)
	}
}

func TestParseOpenCodeOutput_ConcatenatesTextInEventOrder(t *testing.T) {
	out := []byte(`{"type":"text","part":{"type":"text","text":"first "}}
{"type":"tool_use","part":{"type":"tool","tool":"read"}}
{"type":"text","part":{"type":"text","text":"second"}}
{"type":"step_finish","part":{"type":"step-finish","reason":"stop"}}`)

	tr, err := parseOpenCodeOutput(out, true)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed")
	}
	if tr.Summary != "first second" {
		t.Errorf("Summary = %q, want %q", tr.Summary, "first second")
	}
	if tr.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", tr.ToolCalls)
	}
}

func TestParseOpenCodeOutput_StepFinishCompletionRequiresSuccessfulProcess(t *testing.T) {
	out := []byte(`{"type":"step_finish","part":{"type":"step-finish","reason":"stop"}}`)

	tr, err := parseOpenCodeOutput(out, true)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected exit-0 step_finish to complete")
	}
	if tr.Summary != "opencode completed without assistant text" {
		t.Errorf("Summary = %q, want no-text completion indicator", tr.Summary)
	}

	tr, err = parseOpenCodeOutput(out, false)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected non-zero exit to remain incomplete")
	}
	if tr.Summary != "opencode process exited unsuccessfully without a parseable result" {
		t.Errorf("Summary = %q, want unsuccessful-process indicator", tr.Summary)
	}
}

func TestParseOpenCodeOutput_TextCompletionRequiresSuccessfulProcess(t *testing.T) {
	out := []byte(`{"type":"text","part":{"type":"text","text":"partial result"}}`)

	tr, err := parseOpenCodeOutput(out, false)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected non-zero exit to keep parsed text incomplete")
	}
	if tr.Summary != "partial result" {
		t.Errorf("Summary = %q, want parsed assistant text", tr.Summary)
	}
}

// TestParseOpenCodeOutput_CapturedErrorEventFamily follows the top-level error
// event recorded in spike-evidence/opencode-error-event-try167.jsonl.
func TestParseOpenCodeOutput_CapturedErrorEventFamily(t *testing.T) {
	out := []byte(`raw transcript that must not leak
{"type":"error","timestamp":1780285834220,"sessionID":"ses_17eb1fcb4ffeaM4Hrx1qJbTQHa","error":{"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_e558e8ba"}}}`)

	tr, err := parseOpenCodeOutput(out, true)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected error event to remain incomplete")
	}
	want := "opencode error: Unexpected server error. Check server logs for details. (err_e558e8ba)"
	if tr.Summary != want {
		t.Errorf("Summary = %q, want %q", tr.Summary, want)
	}
	if strings.Contains(tr.Summary, "raw transcript") {
		t.Errorf("summary leaked raw output: %q", tr.Summary)
	}
}

func TestParseOpenCodeOutput_ErrorSummaryFallbackAndBound(t *testing.T) {
	t.Run("name fallback", func(t *testing.T) {
		out := []byte(`{"type":"error","error":{"name":"UnknownError","data":{}}}`)
		tr, err := parseOpenCodeOutput(out, true)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Summary != "opencode error: UnknownError" {
			t.Errorf("Summary = %q, want name fallback", tr.Summary)
		}
	})

	t.Run("missing payload", func(t *testing.T) {
		out := []byte(`{"type":"error"}`)
		tr, err := parseOpenCodeOutput(out, true)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Completed {
			t.Error("expected error event to remain incomplete")
		}
		if tr.Summary != "opencode error: unknown error" {
			t.Errorf("Summary = %q, want generic error fallback", tr.Summary)
		}
	})

	t.Run("bounded message and ref", func(t *testing.T) {
		out := []byte(fmt.Sprintf(
			`{"type":"error","error":{"name":"UnknownError","data":{"message":"%s","ref":"%s"}}}`,
			strings.Repeat("m", 2000),
			strings.Repeat("r", 1000),
		))
		tr, err := parseOpenCodeOutput(out, true)
		if err != nil {
			t.Fatal(err)
		}
		if got := len([]rune(tr.Summary)); got > openCodeFailureSummaryLimit {
			t.Errorf("summary length = %d, want <= %d", got, openCodeFailureSummaryLimit)
		}
		if !strings.HasPrefix(tr.Summary, "opencode error: ") {
			t.Errorf("Summary = %q, want error prefix", tr.Summary)
		}
		if !strings.HasSuffix(tr.Summary, "...)") {
			t.Errorf("Summary = %q, want bounded ref suffix", tr.Summary)
		}
	})
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
{"type":"other","part":{"type":"tool","tool":"write"}}
{"type":"text","part":{"type":"text","text":"done"}}`)
	tr, err := parseOpenCodeOutput(out, true)
	if err != nil {
		t.Fatal(err)
	}
	if tr.ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want 2", tr.ToolCalls)
	}
}

func TestOpenCodeExecutor_NonZeroExitReturnsParsedIncompleteResult(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "opencode")
	script := `#!/bin/sh
printf '%s\n' '{"type":"text","part":{"type":"text","text":"partial result"}}'
exit 7
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &OpenCodeExecutor{}
	tr, err := exec.Execute(context.Background(), RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected non-zero exit error")
	}
	if tr == nil {
		t.Fatal("expected parsed result alongside process error")
	}
	if tr.Completed {
		t.Error("expected non-zero exit to keep result incomplete")
	}
	if tr.Summary != "partial result" {
		t.Errorf("Summary = %q, want parsed assistant text", tr.Summary)
	}
	if strings.Contains(err.Error(), "partial result") {
		t.Errorf("executor error leaked subprocess output: %q", err)
	}
}

func TestParseAntigravityOutput_JSON(t *testing.T) {
	tr, err := parseAntigravityOutput([]byte(`{"completed":true,"summary":"ok"}`), "agy-session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected completed")
	}
	if tr.Summary != "ok" {
		t.Errorf("Summary = %q, want ok", tr.Summary)
	}
	if tr.SessionID != "agy-session-1" {
		t.Errorf("SessionID = %q, want agy-session-1", tr.SessionID)
	}
}

func TestParseAntigravityOutput_ResumeUsesLastJSONLine(t *testing.T) {
	out := []byte("previous response\n{\"completed\":false,\"summary\":\"new response\"}\n")
	tr, err := parseAntigravityOutput(out, "agy-session-2")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Completed {
		t.Error("expected completed=false from last JSON line")
	}
	if tr.Summary != "new response" {
		t.Errorf("Summary = %q, want new response", tr.Summary)
	}
}

func TestParseAntigravityOutput_PlainText(t *testing.T) {
	tr, err := parseAntigravityOutput([]byte("plain summary\n"), "agy-session-3")
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Completed {
		t.Error("expected plain text to be treated as completed")
	}
	if tr.Summary != "plain summary" {
		t.Errorf("Summary = %q, want plain summary", tr.Summary)
	}
}

func TestAntigravityAdapter_SessionIDCapture(t *testing.T) {
	logData := []byte(`I0521 printmode.go:130] Print mode: conversation=8eb5b287-eadb-4fc6-ae08-ae5f1ae773f3, sending message`)
	got := scanAntigravityConversationID(logData)
	if got != "8eb5b287-eadb-4fc6-ae08-ae5f1ae773f3" {
		t.Errorf("sessionID = %q", got)
	}
}

func TestAntigravityExecutor_ExecuteUsesPrintModeAndRestoresModel(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)

	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(tmp, "args.txt")
	settingsSnapshotPath := filepath.Join(tmp, "settings-snapshot.json")
	scriptPath := filepath.Join(binDir, "agy")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
cat "$HOME/.gemini/antigravity-cli/settings.json" > %q
printf '{"completed":true,"summary":"agy ok"}'
`, argsPath, settingsSnapshotPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &AntigravityExecutor{Model: DefaultAntigravityModel, PrintTimeout: time.Second}
	res, err := exec.Execute(context.Background(), RunOptions{Prompt: "do work", LogPath: filepath.Join(tmp, "try.log")})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !res.Completed || res.Summary != "agy ok" {
		t.Fatalf("unexpected result: %+v", res)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := string(argsData)
	for _, want := range []string{"--print-timeout=1s", "--dangerously-skip-permissions", "--print", "do work"} {
		if !strings.Contains(args, want) {
			t.Errorf("agy args missing %q:\n%s", want, args)
		}
	}

	snapshot, err := os.ReadFile(settingsSnapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snapshot), DefaultAntigravityModel) {
		t.Errorf("settings snapshot missing model %q:\n%s", DefaultAntigravityModel, string(snapshot))
	}

	settingsPath := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Errorf("settings file should be restored/removed, stat err = %v", err)
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
	if g.ResumeSupported() {
		t.Error("ResumeSupported() = true, want false (gemini --resume takes index/latest, not session UUID)")
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

func TestAntigravityAdapterCapabilities(t *testing.T) {
	a := &AntigravityExecutor{}
	if !a.ResumeSupported() {
		t.Error("ResumeSupported() = false, want true")
	}
	if a.RotateSupported() {
		t.Error("RotateSupported() = true, want false")
	}
	if a.LivenessProbeSupported() {
		t.Error("LivenessProbeSupported() = true, want false")
	}
	if err := a.RotateModel("new-model"); err == nil {
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

func testMockBinDir(t *testing.T, binName string) (binDir string, argsPath string) {
	t.Helper()
	tmp := t.TempDir()
	binDir = filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	argsPath = filepath.Join(tmp, "args.txt")
	return binDir, argsPath
}

func TestClaudeExecutor_ResumeFlagInArgs(t *testing.T) {
	binDir, argsPath := testMockBinDir(t, "claude")
	scriptPath := filepath.Join(binDir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
printf '%%s\n' '{"type":"system","session_id":"mock-sess"}'
printf '%%s\n' '{"type":"result","result":{"completed":true,"summary":"ok"}}'
`, argsPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "try.log")
	exec := &ClaudeExecutor{}
	res, err := exec.Execute(context.Background(), RunOptions{
		Prompt:          "do work",
		ResumeSessionID: "sess-resume-42",
		LogPath:         logPath,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !res.Completed {
		t.Error("expected completed")
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := string(argsData)
	if !strings.Contains(args, "--resume") || !strings.Contains(args, "sess-resume-42") {
		t.Errorf("claude args missing --resume sess-resume-42, got:\n%s", args)
	}
}

func TestClaudeExecutor_NoResumeFlagWhenSessionIDEmpty(t *testing.T) {
	binDir, argsPath := testMockBinDir(t, "claude")
	scriptPath := filepath.Join(binDir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
printf '%%s\n' '{"type":"system","session_id":"mock-sess"}'
printf '%%s\n' '{"type":"result","result":{"completed":true,"summary":"ok"}}'
`, argsPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "try.log")
	exec := &ClaudeExecutor{}
	_, err := exec.Execute(context.Background(), RunOptions{
		Prompt:  "do work",
		LogPath: logPath,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argsData), "--resume") {
		t.Errorf("claude args should not contain --resume when ResumeSessionID is empty, got:\n%s", string(argsData))
	}
}

func TestAntigravityExecutor_ResumeFlagInArgs(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)

	binDir, argsPath := testMockBinDir(t, "antigravity")
	scriptPath := filepath.Join(binDir, "agy")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
printf '%%s\n' '{"completed":true,"summary":"agy ok"}'
`, argsPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	logPath := filepath.Join(tmp, "try.log")
	exec := &AntigravityExecutor{PrintTimeout: time.Second}
	_, err := exec.Execute(context.Background(), RunOptions{
		Prompt:          "do work",
		ResumeSessionID: "conv-abc-123",
		LogPath:         logPath,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := string(argsData)
	if !strings.Contains(args, "--conversation=conv-abc-123") {
		t.Errorf("antigravity args missing conversation flag, got:\n%s", args)
	}
}

func TestAntigravityExecutor_NoResumeFlagWhenSessionIDEmpty(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)

	binDir, argsPath := testMockBinDir(t, "antigravity")
	scriptPath := filepath.Join(binDir, "agy")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
printf '%%s\n' '{"completed":true,"summary":"agy ok"}'
`, argsPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	logPath := filepath.Join(tmp, "try.log")
	exec := &AntigravityExecutor{PrintTimeout: time.Second}
	_, err := exec.Execute(context.Background(), RunOptions{
		Prompt:  "do work",
		LogPath: logPath,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argsData), "--conversation=") {
		t.Errorf("antigravity args should not contain --conversation when ResumeSessionID is empty, got:\n%s", string(argsData))
	}
}

func TestOpenCodeExecutor_ResumeFlagInArgs(t *testing.T) {
	binDir, argsPath := testMockBinDir(t, "opencode")
	scriptPath := filepath.Join(binDir, "opencode")
	innerJSON := `{"completed":true,"summary":"ok"}`
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
printf '%%s\n' '{"type":"text","part":{"type":"text","text":"%s"}}'
`, argsPath, strings.ReplaceAll(innerJSON, `"`, `\"`))
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &OpenCodeExecutor{}
	res, err := exec.Execute(context.Background(), RunOptions{
		Prompt:          "do work",
		ResumeSessionID: "ses-resume-99",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !res.Completed {
		t.Error("expected completed")
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := string(argsData)
	if !strings.Contains(args, "--session") || !strings.Contains(args, "ses-resume-99") {
		t.Errorf("opencode args missing --session ses-resume-99, got:\n%s", args)
	}
}

func TestOpenCodeExecutor_NoResumeFlagWhenSessionIDEmpty(t *testing.T) {
	binDir, argsPath := testMockBinDir(t, "opencode")
	scriptPath := filepath.Join(binDir, "opencode")
	innerJSON := `{"completed":true,"summary":"ok"}`
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
printf '%%s\n' '{"type":"text","part":{"type":"text","text":"%s"}}'
`, argsPath, strings.ReplaceAll(innerJSON, `"`, `\"`))
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &OpenCodeExecutor{}
	_, err := exec.Execute(context.Background(), RunOptions{
		Prompt: "do work",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argsData), "--session") {
		t.Errorf("opencode args should not contain --session when ResumeSessionID is empty, got:\n%s", string(argsData))
	}
}

func TestCodexExecutor_ResumeFlagInArgs(t *testing.T) {
	binDir, argsPath := testMockBinDir(t, "codex")
	scriptPath := filepath.Join(binDir, "codex")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
printf '%%s\n' '{"type":"thread.started","thread_id":"codex-mock-sess"}'
printf '%%s\n' '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"ok"}}'
next=0
for i in "$@"; do
  if [ "$next" = "1" ]; then printf '{"completed":true,"summary":"codex ok"}' > "$i"; break; fi
  if [ "$i" = "-o" ]; then next=1; fi
done
`, argsPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &CodexExecutor{}
	res, err := exec.Execute(context.Background(), RunOptions{
		Prompt:          "do work",
		ResumeSessionID: "sess-resume-77",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !res.Completed {
		t.Error("expected completed")
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := string(argsData)
	if !strings.Contains(args, "resume") || !strings.Contains(args, "sess-resume-77") {
		t.Errorf("codex args missing resume sess-resume-77, got:\n%s", args)
	}
}

func TestAntigravityExecutor_EvidenceOnGeminiQuotaError(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)

	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "agy")
	script := `#!/bin/sh
printf 'RESOURCE_EXHAUSTED\nIndividual quota reached\nResets in 1h30m\n'
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &AntigravityExecutor{PrintTimeout: time.Second}
	tr, err := exec.Execute(context.Background(), RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from antigravity mock")
	}
	if tr == nil {
		t.Fatal("expected TryResult with Evidence, got nil")
	}
	if tr.Evidence == nil {
		t.Fatal("expected Evidence to be populated for RESOURCE_EXHAUSTED")
	}
	if tr.Evidence.Category != reliability.CategoryUsageLimit {
		t.Errorf("Category = %q, want %q", tr.Evidence.Category, reliability.CategoryUsageLimit)
	}
	if tr.Evidence.Provider != reliability.ProviderGemini {
		t.Errorf("Provider = %q, want %q", tr.Evidence.Provider, reliability.ProviderGemini)
	}
}

func TestClaudeExecutor_EvidenceOnRateLimitError(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
printf 'rate_limit_event: five hour window\n'
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &ClaudeExecutor{}
	tr, err := exec.Execute(context.Background(), RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from claude mock")
	}
	if tr == nil {
		t.Fatal("expected TryResult with Evidence, got nil")
	}
	if tr.Evidence == nil {
		t.Fatal("expected Evidence to be populated for rate_limit_event")
	}
	if tr.Evidence.Category != reliability.CategoryShortRateLimit {
		t.Errorf("Category = %q, want %q", tr.Evidence.Category, reliability.CategoryShortRateLimit)
	}
	if tr.Evidence.Provider != reliability.ProviderAnthropic {
		t.Errorf("Provider = %q, want %q", tr.Evidence.Provider, reliability.ProviderAnthropic)
	}
}

func TestClaudeExecutor_NoEvidenceOnUnknownError(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
printf 'something went wrong\n'
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &ClaudeExecutor{}
	tr, err := exec.Execute(context.Background(), RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from claude mock")
	}
	if tr != nil {
		t.Fatalf("expected nil TryResult for unknown error, got %+v", tr)
	}
}

func TestCodexExecutor_EvidenceOnUsageLimitError(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "codex")
	script := `#!/bin/sh
printf 'You hit your usage limit. Try again at 3:00 PM\n'
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &CodexExecutor{}
	tr, err := exec.Execute(context.Background(), RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from codex mock")
	}
	if tr == nil {
		t.Fatal("expected TryResult with Evidence, got nil")
	}
	if tr.Evidence == nil {
		t.Fatal("expected Evidence to be populated for usage limit")
	}
	if tr.Evidence.Category != reliability.CategoryUsageLimit {
		t.Errorf("Category = %q, want %q", tr.Evidence.Category, reliability.CategoryUsageLimit)
	}
	if tr.Evidence.Provider != reliability.ProviderOpenAI {
		t.Errorf("Provider = %q, want %q", tr.Evidence.Provider, reliability.ProviderOpenAI)
	}
}

func TestGeminiExecutor_EvidenceOnQuotaError(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "gemini")
	script := `#!/bin/sh
printf 'RESOURCE_EXHAUSTED\nIndividual quota reached\nResets in 1h30m\n' >&2
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &GeminiExecutor{}
	tr, err := exec.Execute(context.Background(), RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from gemini mock")
	}
	if tr == nil {
		t.Fatal("expected TryResult with Evidence, got nil")
	}
	if tr.Evidence == nil {
		t.Fatal("expected Evidence to be populated for RESOURCE_EXHAUSTED")
	}
	if tr.Evidence.Category != reliability.CategoryUsageLimit {
		t.Errorf("Category = %q, want %q", tr.Evidence.Category, reliability.CategoryUsageLimit)
	}
	if tr.Evidence.Provider != reliability.ProviderGemini {
		t.Errorf("Provider = %q, want %q", tr.Evidence.Provider, reliability.ProviderGemini)
	}
}

func TestGeminiExecutor_NoEvidenceOnUnknownError(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "gemini")
	script := `#!/bin/sh
printf 'something unexpected\n' >&2
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &GeminiExecutor{}
	tr, err := exec.Execute(context.Background(), RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from gemini mock")
	}
	if tr != nil {
		t.Fatalf("expected nil TryResult for unknown error, got %+v", tr)
	}
}

func TestOpenCodeExecutor_EvidenceOnRateLimitError(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "opencode")
	script := `#!/bin/sh
printf '{"type":"error","error":{"name":"RateLimitError","data":{"message":"rate limit exceeded, retry after 60 seconds"}}}\n'
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &OpenCodeExecutor{Model: "anthropic/claude-4"}
	tr, err := exec.Execute(context.Background(), RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from opencode mock")
	}
	if tr == nil {
		t.Fatal("expected TryResult, got nil")
	}
	if tr.Evidence == nil {
		t.Fatal("expected Evidence to be populated for rate limit error event")
	}
	if tr.Evidence.Category != reliability.CategoryShortRateLimit {
		t.Errorf("Category = %q, want %q", tr.Evidence.Category, reliability.CategoryShortRateLimit)
	}
	if tr.Evidence.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", tr.Evidence.Provider, "anthropic")
	}
}

func TestOpenCodeExecutor_EvidenceOnUsageLimitError(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "opencode")
	script := `#!/bin/sh
printf '{"type":"error","error":{"name":"UsageLimitError","data":{"message":"usage limit exceeded"}}}\n'
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &OpenCodeExecutor{Model: "openai/gpt-4o"}
	tr, err := exec.Execute(context.Background(), RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from opencode mock")
	}
	if tr == nil {
		t.Fatal("expected TryResult, got nil")
	}
	if tr.Evidence == nil {
		t.Fatal("expected Evidence to be populated for usage limit error event")
	}
	if tr.Evidence.Category != reliability.CategoryUsageLimit {
		t.Errorf("Category = %q, want %q", tr.Evidence.Category, reliability.CategoryUsageLimit)
	}
	if tr.Evidence.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", tr.Evidence.Provider, "openai")
	}
}

func TestCodexExecutor_NoResumeFlagWhenSessionIDEmpty(t *testing.T) {
	binDir, argsPath := testMockBinDir(t, "codex")
	scriptPath := filepath.Join(binDir, "codex")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
printf '%%s\n' '{"type":"thread.started","thread_id":"codex-mock-sess"}'
printf '%%s\n' '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"ok"}}'
next=0
for i in "$@"; do
  if [ "$next" = "1" ]; then printf '{"completed":true,"summary":"codex ok"}' > "$i"; break; fi
  if [ "$i" = "-o" ]; then next=1; fi
done
`, argsPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &CodexExecutor{}
	_, err := exec.Execute(context.Background(), RunOptions{
		Prompt: "do work",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argsData), "resume") {
		t.Errorf("codex args should not contain resume when ResumeSessionID is empty, got:\n%s", string(argsData))
	}
}
