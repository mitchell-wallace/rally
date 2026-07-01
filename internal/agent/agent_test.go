package agent

import (
	"bytes"
	"context"
	"fmt"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent_prompt"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/harness/process"
	"github.com/mitchell-wallace/rally/internal/reliability"
)

func TestBuildPrompt_AllFields(t *testing.T) {
	opts := harnessapi.RunOptions{
		Persona:          "Expert Go developer",
		TaskName:         "Refactor store layer",
		TaskRequirements: "Use generics for JSONL records.",
		Instructions:     "Always write tests first.",
		TaskPrompt:       "Fix the caching bug.",
		InboxMessage:     "Urgent: fix race condition.",
		PreviousSummary:  "Added basic cache.",
		RecentTryContext: "Try #5 failed with timeout.",
	}
	p := harnessapi.BuildPrompt(opts)
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
	opts := harnessapi.RunOptions{
		Prompt:  "CUSTOM PROMPT",
		Persona: "ignored",
	}
	p := harnessapi.BuildPrompt(opts)
	if p != "CUSTOM PROMPT" {
		t.Fatalf("expected explicit prompt, got %q", p)
	}
}

func TestBuildPrompt_PreviousSummary(t *testing.T) {
	opts := harnessapi.RunOptions{
		TaskName:        "Foo",
		PreviousSummary: "Bar",
	}
	p := harnessapi.BuildPrompt(opts)
	if !strings.Contains(p, "Previous Summary:") {
		t.Error("expected Previous Summary section")
	}
	if !strings.Contains(p, "Bar") {
		t.Error("expected summary text")
	}
}

func TestBuildPrompt_Instructions(t *testing.T) {
	opts := harnessapi.RunOptions{
		Instructions: "Always use TDD.",
	}
	p := harnessapi.BuildPrompt(opts)
	if !strings.Contains(p, "## Project Instructions") {
		t.Error("expected ## Project Instructions section")
	}
	if !strings.Contains(p, "Always use TDD.") {
		t.Error("expected instructions text")
	}
}

func TestBuildPrompt_RoleInstructionsBetweenProjectInstructionsAndTask(t *testing.T) {
	opts := harnessapi.RunOptions{
		Instructions:     "Base instructions.",
		RoleInstructions: "Role instructions.",
		TaskPrompt:       "Task body.",
	}
	p := harnessapi.BuildPrompt(opts)

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
	opts := harnessapi.RunOptions{
		TaskPrompt: "Fix the race condition.",
	}
	p := harnessapi.BuildPrompt(opts)
	if !strings.Contains(p, "## Task") {
		t.Error("expected ## Task section")
	}
	if !strings.Contains(p, "Fix the race condition.") {
		t.Error("expected task prompt text")
	}
}

func TestBuildPrompt_SharedGuidanceIncludedWhenLapsEnabled(t *testing.T) {
	opts := harnessapi.RunOptions{
		TaskName:         "Do the thing",
		RoleInstructions: "Role instructions.",
		LapsEnabled:      true,
	}
	p := harnessapi.BuildPrompt(opts)

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

func TestBuildPrompt_VerifyExitGuidanceOmitsHandoff(t *testing.T) {
	p := harnessapi.BuildPrompt(harnessapi.RunOptions{
		Role:             "verify",
		RoleInstructions: "Do not call `laps handoff`.",
		LapsEnabled:      true,
	})

	if strings.Contains(p, agent_prompt.Finalize()) {
		t.Fatalf("verify prompt should not include generic finalize handoff guidance:\n%s", p)
	}
	if strings.Contains(p, "If you are blocked and cannot proceed, run this shell command:\n  laps handoff") {
		t.Fatalf("verify prompt should not instruct blocked verify agents to hand off:\n%s", p)
	}
	if !strings.Contains(p, "For VERIFY work, do not use `laps handoff`") {
		t.Fatalf("verify prompt missing role-aware no-handoff guidance:\n%s", p)
	}
	if !strings.Contains(p, "laps done") {
		t.Fatalf("verify prompt still needs completion guidance:\n%s", p)
	}
}

func TestBuildPrompt_SharedGuidanceOmittedInNoBackendMode(t *testing.T) {
	opts := harnessapi.RunOptions{
		TaskName:    "Do the thing",
		LapsEnabled: false,
	}
	p := harnessapi.BuildPrompt(opts)

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
	opts := harnessapi.RunOptions{
		Prompt:      "CUSTOM PROMPT",
		LapsEnabled: true,
	}
	p := harnessapi.BuildPrompt(opts)
	if p != "CUSTOM PROMPT" {
		t.Fatalf("explicit override not preserved verbatim, got %q", p)
	}
}

func TestBuildPrompt_SharedGuidanceOrdering(t *testing.T) {
	opts := harnessapi.RunOptions{
		Persona:          "claude",
		TaskName:         "Do the thing",
		RoleInstructions: "Role instructions.",
		TaskPrompt:       "Task body.",
		LapsEnabled:      true,
	}
	p := harnessapi.BuildPrompt(opts)

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

func TestBuildPrompt_RecoveryClassificationOnlyFromRecoveryRole(t *testing.T) {
	recoveryRole, ok := agent_prompt.Role("recovery")
	if !ok {
		t.Fatal("missing recovery role")
	}
	recoveryPrompt := harnessapi.BuildPrompt(harnessapi.RunOptions{
		RoleInstructions: recoveryRole,
		LapsEnabled:      true,
	})
	if !strings.Contains(recoveryPrompt, "laps wrapup --classification <value>") {
		t.Fatalf("recovery prompt missing classification instruction:\n%s", recoveryPrompt)
	}

	for _, role := range []string{"junior", "senior", "ui", "verify"} {
		roleInstructions, ok := agent_prompt.Role(role)
		if !ok {
			t.Fatalf("missing %s role", role)
		}
		prompt := harnessapi.BuildPrompt(harnessapi.RunOptions{
			RoleInstructions: roleInstructions,
			LapsEnabled:      true,
		})
		for _, forbidden := range []string{"laps wrapup --classification", "course_correct", "repair_plan", "needs_user"} {
			if strings.Contains(prompt, forbidden) {
				t.Fatalf("%s prompt unexpectedly contains recovery classification marker %q:\n%s", role, forbidden, prompt)
			}
		}
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
// forcing the author to prove the session is actually captured end-to-end. The
// relocated harness adapters (claude/codex/antigravity, now in
// internal/harness/<name>) carry their own equivalent capture+capability
// coverage in-package; this in-agent assertion covers opencode while it remains
// here ahead of its own relocation.
func TestResumeSupportImpliesSessionCapture(t *testing.T) {
	// Each extractor feeds a realistic, harness-specific output sample through the
	// executor's real capture path and returns the captured session ID.
	captures := map[string]func() string{
		"opencode": func() string {
			out := []byte(`{"type":"step_start","sessionID":"ses_oc1","part":{"type":"step-start"}}
{"type":"text","sessionID":"ses_oc1","part":{"type":"text","text":"{\"completed\":true,\"summary\":\"ok\"}"}}`)
			tr, err := parseOpenCodeOutput(out, true)
			if err != nil {
				return ""
			}
			return tr.SessionID
		},
	}
	executors := map[string]harnessapi.Executor{
		"opencode": &OpenCodeExecutor{},
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

// TestParseOpenCodeOutput_TrimsWhitespace guards against OpenCode streams where
// the model emits multiple "\n\n\n" text steps before the final answer. The
// streamed parts get joined verbatim, so the fallback summary used to start with
// ~11 newlines. We trim them.
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
		out, err := process.RunLoggedCommand(cmd, logPath, false, func(pid int) {
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
		t.Fatalf("process.RunLoggedCommand failed: %v", res.err)
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
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"})
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

func TestOpenCodeExecutor_ServerLogTailEvidenceByWorkspaceSession(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "opencode")
	script := `#!/bin/sh
printf '%s\n' '{"type":"error","error":{"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_generic"}}}'
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	workspaceDir := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	serverLogPath := filepath.Join(tmp, "opencode.log")
	oldServerLogPath := openCodeServerLogPath
	openCodeServerLogPath = func() string { return serverLogPath }
	t.Cleanup(func() { openCodeServerLogPath = oldServerLogPath })

	ts := time.Now().UTC().Format(time.RFC3339Nano)
	logText := strings.Join([]string{
		fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_right directory="%s"`, ts, workspaceDir),
		fmt.Sprintf(`timestamp=%s level=ERROR message="stream error" providerID=opencode-go modelID=kimi session.id=ses_other small=false agent=build error.error="AI_APICallError: Monthly usage limit reached. Resets in 2 days."`, ts),
		fmt.Sprintf(`timestamp=%s level=ERROR message="stream error" providerID=opencode-go modelID=kimi session.id=ses_right small=true agent=title error.error="AI_RetryError: Failed after 3 attempts. Last error: Monthly usage limit reached. Resets in 7 days."`, ts),
	}, "\n")
	if err := os.WriteFile(serverLogPath, []byte(logText), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := &OpenCodeExecutor{Model: "opencode-go/kimi"}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", WorkspaceDir: workspaceDir})
	if err == nil {
		t.Fatal("expected error from opencode mock")
	}
	if tr == nil {
		t.Fatal("expected harnessapi.TryResult, got nil")
	}
	if tr.Summary != "opencode error: Unexpected server error. Check server logs for details. (err_generic)" {
		t.Fatalf("Summary = %q, want generic UnknownError summary", tr.Summary)
	}
	if tr.Evidence == nil {
		t.Fatal("expected server-log usage-limit evidence")
	}
	if tr.Evidence.Category != reliability.CategoryUsageLimit {
		t.Fatalf("Category = %q, want %q", tr.Evidence.Category, reliability.CategoryUsageLimit)
	}
	if tr.Evidence.Provider != "opencode-go" {
		t.Errorf("Provider = %q, want opencode-go", tr.Evidence.Provider)
	}
	if tr.Evidence.ResetAfter != 7*24*time.Hour {
		t.Errorf("ResetAfter = %v, want 168h", tr.Evidence.ResetAfter)
	}
	if tr.Evidence.Source != openCodeDiskLogSource {
		t.Errorf("Source = %q, want %q", tr.Evidence.Source, openCodeDiskLogSource)
	}
	if tr.Evidence.RawSignal == "" {
		t.Fatal("expected bounded disk-log RawSignal")
	}
	if !strings.Contains(tr.Evidence.RawSignal, "ses_right") {
		t.Errorf("RawSignal = %q, want matched session tail", tr.Evidence.RawSignal)
	}
	if strings.Contains(tr.Evidence.RawSignal, "ses_other") {
		t.Errorf("RawSignal = %q, must not include other sessions", tr.Evidence.RawSignal)
	}
	if got := len([]rune(tr.Evidence.RawSignal)); got > 257 {
		t.Errorf("RawSignal rune length = %d, want <= 257 (256 + ellipsis)", got)
	}
}

func TestOpenCodeExecutor_ServerLogTailEvidenceByProviderWindowFallback(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "opencode")
	script := `#!/bin/sh
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	workspaceDir := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	serverLogPath := filepath.Join(tmp, "opencode.log")
	oldServerLogPath := openCodeServerLogPath
	openCodeServerLogPath = func() string { return serverLogPath }
	t.Cleanup(func() { openCodeServerLogPath = oldServerLogPath })

	recent := time.Now().UTC().Format(time.RFC3339Nano)
	stale := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
	logText := strings.Join([]string{
		fmt.Sprintf(`timestamp=%s level=ERROR message="stream error" providerID=opencode-go modelID=kimi session.id=ses_stale small=false agent=build error.error="AI_APICallError: Monthly usage limit reached. Resets in 5 days."`, stale),
		fmt.Sprintf(`timestamp=%s level=ERROR message="stream error" providerID=zai-coding-plan modelID=glm-5.2 session.id=ses_wrong_provider small=false agent=build error.error="AI_APICallError: Usage limit reached for 5 hour. Your limit will reset at 2026-06-16 18:29:51"`, recent),
		fmt.Sprintf(`timestamp=%s level=ERROR message="stream error" providerID=opencode-go modelID=kimi session.id=ses_right_provider small=false agent=build error.error="AI_RetryError: Failed after 3 attempts. Last error: Monthly usage limit reached. Resets in 7 days."`, recent),
	}, "\n")
	if err := os.WriteFile(serverLogPath, []byte(logText), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := &OpenCodeExecutor{Model: "opencode-go/kimi"}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", WorkspaceDir: workspaceDir})
	if err == nil {
		t.Fatal("expected error from opencode mock")
	}
	if tr == nil {
		t.Fatal("expected harnessapi.TryResult, got nil")
	}
	if tr.Evidence == nil {
		t.Fatal("expected provider/window fallback usage-limit evidence")
	}
	if tr.Evidence.Category != reliability.CategoryUsageLimit {
		t.Fatalf("Category = %q, want %q", tr.Evidence.Category, reliability.CategoryUsageLimit)
	}
	if tr.Evidence.Provider != "opencode-go" {
		t.Errorf("Provider = %q, want opencode-go", tr.Evidence.Provider)
	}
	if tr.Evidence.ResetAfter != 7*24*time.Hour {
		t.Errorf("ResetAfter = %v, want 168h", tr.Evidence.ResetAfter)
	}
	if tr.Evidence.ResetAt != nil {
		t.Errorf("ResetAt = %v, want nil (wrong provider's absolute reset must be ignored)", tr.Evidence.ResetAt)
	}
	if tr.Evidence.Source != openCodeDiskLogSource {
		t.Errorf("Source = %q, want %q", tr.Evidence.Source, openCodeDiskLogSource)
	}
	if tr.Evidence.RawSignal == "" {
		t.Fatal("expected bounded disk-log RawSignal")
	}
	if !strings.Contains(tr.Evidence.RawSignal, "ses_right_provider") {
		t.Errorf("RawSignal = %q, want provider-matched tail", tr.Evidence.RawSignal)
	}
	for _, forbidden := range []string{"ses_stale", "zai-coding-plan"} {
		if strings.Contains(tr.Evidence.RawSignal, forbidden) {
			t.Errorf("RawSignal = %q, must not include %q", tr.Evidence.RawSignal, forbidden)
		}
	}
	if got := len([]rune(tr.Evidence.RawSignal)); got > 257 {
		t.Errorf("RawSignal rune length = %d, want <= 257 (256 + ellipsis)", got)
	}
}

// Task 6.5(a): budget-killed try with WARN/ERROR lines showing a recognisable
// error -> Source = "opencode_disk_log", correct recognised category.
func TestOpenCodeDiskLog_RecognisableError(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	entries := []openCodeServerLogEntry{
		{
			raw:    fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_a directory="/tmp/ws"`, ts),
			fields: parseOpenCodeLogFields(fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_a directory="/tmp/ws"`, ts)),
		},
		{
			raw:    fmt.Sprintf(`timestamp=%s level=ERROR message="stream error" providerID=opencode-go error.error="AI_APICallError: Monthly usage limit reached. Resets in 3 days."`, ts),
			fields: parseOpenCodeLogFields(fmt.Sprintf(`timestamp=%s level=ERROR message="stream error" providerID=opencode-go error.error="AI_APICallError: Monthly usage limit reached. Resets in 3 days."`, ts)),
		},
	}
	ev := openCodeDiskLogEvidence(entries, "opencode-go/kimi")
	if ev == nil {
		t.Fatal("expected evidence")
	}
	if ev.Source != "opencode_disk_log" {
		t.Errorf("Source = %q, want opencode_disk_log", ev.Source)
	}
	if ev.Category != reliability.CategoryUsageLimit {
		t.Errorf("Category = %q, want %q", ev.Category, reliability.CategoryUsageLimit)
	}
	if ev.Provider == "" {
		t.Error("expected non-empty Provider")
	}
}

func TestOpenCodeDiskLog_RecognisableMessageField(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	line := fmt.Sprintf(`timestamp=%s level=ERROR message="API key invalid" providerID=opencode-go session.id=ses_auth`, ts)
	entries := []openCodeServerLogEntry{
		{
			raw:    fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_auth directory="/tmp/ws"`, ts),
			fields: parseOpenCodeLogFields(fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_auth directory="/tmp/ws"`, ts)),
		},
		{
			raw:    line,
			fields: parseOpenCodeLogFields(line),
		},
	}
	ev := openCodeDiskLogEvidence(entries, "opencode-go/kimi")
	if ev == nil {
		t.Fatal("expected evidence")
	}
	if ev.Source != "opencode_disk_log" {
		t.Errorf("Source = %q, want opencode_disk_log", ev.Source)
	}
	if ev.Category != reliability.CategoryAuthOrProxy {
		t.Errorf("Category = %q, want %q", ev.Category, reliability.CategoryAuthOrProxy)
	}
}

// Task 6.5(b): budget-killed try with WARN/ERROR lines but no recognisable
// shape -> Source = "opencode_disk_log", Category = agent_error, RawSignal
// includes error lines.
func TestOpenCodeDiskLog_UnrecognisedError(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	entries := []openCodeServerLogEntry{
		{
			raw:    fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_b directory="/tmp/ws"`, ts),
			fields: parseOpenCodeLogFields(fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_b directory="/tmp/ws"`, ts)),
		},
		{
			raw:    fmt.Sprintf(`timestamp=%s level=WARN message="something weird happened"`, ts),
			fields: parseOpenCodeLogFields(fmt.Sprintf(`timestamp=%s level=WARN message="something weird happened"`, ts)),
		},
	}
	ev := openCodeDiskLogEvidence(entries, "opencode-go/kimi")
	if ev == nil {
		t.Fatal("expected evidence")
	}
	if ev.Source != "opencode_disk_log" {
		t.Errorf("Source = %q, want opencode_disk_log", ev.Source)
	}
	if ev.Category != reliability.CategoryAgentError {
		t.Errorf("Category = %q, want %q", ev.Category, reliability.CategoryAgentError)
	}
	if ev.RawSignal == "" {
		t.Error("expected RawSignal to include error/warn lines")
	}
	if !strings.Contains(ev.RawSignal, "something weird happened") {
		t.Errorf("RawSignal = %q, expected it to contain the warning line", ev.RawSignal)
	}
}

// Task 6.5(c): budget-killed try with only structural loop/stream lines
// -> Source = "opencode_disk_log", Category = unidentified_issue,
// Message = "try budget exhausted; no parseable output".
func TestOpenCodeDiskLog_StructuralOnly(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	entries := []openCodeServerLogEntry{
		{
			raw:    fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_c directory="/tmp/ws"`, ts),
			fields: parseOpenCodeLogFields(fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_c directory="/tmp/ws"`, ts)),
		},
		{
			raw:    fmt.Sprintf(`timestamp=%s level=INFO message=stream`, ts),
			fields: parseOpenCodeLogFields(fmt.Sprintf(`timestamp=%s level=INFO message=stream`, ts)),
		},
	}
	ev := openCodeDiskLogEvidence(entries, "opencode-go/kimi")
	if ev == nil {
		t.Fatal("expected evidence")
	}
	if ev.Source != "opencode_disk_log" {
		t.Errorf("Source = %q, want opencode_disk_log", ev.Source)
	}
	if ev.Category != reliability.CategoryUnidentifiedIssue {
		t.Errorf("Category = %q, want %q", ev.Category, reliability.CategoryUnidentifiedIssue)
	}
	if ev.Message != "try budget exhausted; no parseable output" {
		t.Errorf("Message = %q, want 'try budget exhausted; no parseable output'", ev.Message)
	}
}

// Task 6.5(d): per-token log lines never appear in RawSignal.
func TestOpenCodeDiskLog_NoisyLinesExcluded(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	lines := []string{
		fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_d directory="/tmp/ws"`, ts),
		fmt.Sprintf(`timestamp=%s level=WARN message="token stream chunk secret-token" session.id=ses_d`, ts),
		fmt.Sprintf(`timestamp=%s level=ERROR message="tool_call fetch" session.id=ses_d`, ts),
		fmt.Sprintf(`timestamp=%s level=WARN message="permission granted" session.id=ses_d`, ts),
		fmt.Sprintf(`timestamp=%s level=WARN message="kept warning" session.id=ses_d`, ts),
	}
	var entries []openCodeServerLogEntry
	for _, line := range lines {
		fields := parseOpenCodeLogFields(line)
		if openCodeIsNoteworthyLogLine(fields) && !openCodeIsNoisyLogLine(fields) {
			entries = append(entries, openCodeServerLogEntry{raw: line, fields: fields})
		}
	}
	ev := openCodeDiskLogFailureEvidence(entries, "opencode-go/kimi", map[string]struct{}{"ses_d": {}}, "opencode-go", "")
	if ev == nil {
		t.Fatal("expected evidence")
	}
	if !strings.Contains(ev.RawSignal, "kept warning") {
		t.Fatalf("RawSignal = %q, want kept warning", ev.RawSignal)
	}
	for _, forbidden := range []string{"secret-token", "tool_call", "permission granted"} {
		if strings.Contains(ev.RawSignal, forbidden) {
			t.Errorf("RawSignal = %q, must not contain noisy %q", ev.RawSignal, forbidden)
		}
	}
}

// Task 6.5(e): 256-rune bound holds.
func TestOpenCodeDiskLog_RuneBound(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	longMsg := strings.Repeat("x", 500)
	entries := []openCodeServerLogEntry{
		{
			raw:    fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_e directory="/tmp/ws"`, ts),
			fields: parseOpenCodeLogFields(fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_e directory="/tmp/ws"`, ts)),
		},
		{
			raw:    fmt.Sprintf(`timestamp=%s level=ERROR message="%s"`, ts, longMsg),
			fields: parseOpenCodeLogFields(fmt.Sprintf(`timestamp=%s level=ERROR message="%s"`, ts, longMsg)),
		},
	}
	ev := openCodeDiskLogEvidence(entries, "opencode-go/kimi")
	if ev == nil {
		t.Fatal("expected evidence")
	}
	if got := len([]rune(ev.RawSignal)); got > 257 { // 256 + 1 ellipsis
		t.Errorf("RawSignal rune length = %d, want <= 257 (256 + ellipsis)", got)
	}
}

// Task 6.5(f): existing usage-limit extraction still works when the in-band
// wrapper-only UnknownError path has no usable category.
func TestOpenCodeDiskLog_UsageLimitRegressionGuard(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "opencode")
	// Produce a generic wrapper-only error event so the executor triggers the
	// evidence path without claiming a real in-band category.
	script := `#!/bin/sh
printf '%s\n' '{"type":"error","error":{"name":"UnknownError","data":{"message":"Unexpected server error.","ref":"err_x"}}}'
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	workspaceDir := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	serverLogPath := filepath.Join(tmp, "opencode.log")
	oldServerLogPath := openCodeServerLogPath
	openCodeServerLogPath = func() string { return serverLogPath }
	t.Cleanup(func() { openCodeServerLogPath = oldServerLogPath })

	ts := time.Now().UTC().Format(time.RFC3339Nano)
	logText := strings.Join([]string{
		fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_reg directory="%s"`, ts, workspaceDir),
		fmt.Sprintf(`timestamp=%s level=ERROR message="stream error" providerID=opencode-go modelID=kimi session.id=ses_reg small=false agent=build error.error="AI_APICallError: Monthly usage limit reached. Resets in 2 days."`, ts),
	}, "\n")
	if err := os.WriteFile(serverLogPath, []byte(logText), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := &OpenCodeExecutor{Model: "opencode-go/kimi"}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", WorkspaceDir: workspaceDir})
	if err == nil {
		t.Fatal("expected error from opencode mock")
	}
	if tr == nil {
		t.Fatal("expected harnessapi.TryResult, got nil")
	}
	if tr.Evidence == nil {
		t.Fatal("expected usage-limit evidence from existing stream-error path")
	}
	if tr.Evidence.Category != reliability.CategoryUsageLimit {
		t.Fatalf("Category = %q, want %q (regression: existing usage-limit path broken)", tr.Evidence.Category, reliability.CategoryUsageLimit)
	}
}

// Test the precedence update: in-band evidence with a non-empty Category
// must NOT be replaced by disk-log evidence.
func TestOpenCodeDiskLog_InBandEvidencePreserved(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "opencode")
	// Produce an error event that yields agent_error (a specific category from in-band).
	script := `#!/bin/sh
printf '%s\n' '{"type":"error","error":{"name":"UnknownError","data":{"message":"Agent runtime crashed while applying patch.","ref":"err_xx"}}}'
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	workspaceDir := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	serverLogPath := filepath.Join(tmp, "opencode.log")
	oldServerLogPath := openCodeServerLogPath
	openCodeServerLogPath = func() string { return serverLogPath }
	t.Cleanup(func() { openCodeServerLogPath = oldServerLogPath })

	// Server log has recognisable usage-limit evidence. The in-band
	// agent_error should still NOT be replaced.
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	logText := strings.Join([]string{
		fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_preserve directory="%s"`, ts, workspaceDir),
		fmt.Sprintf(`timestamp=%s level=ERROR message="stream error" providerID=opencode-go modelID=kimi session.id=ses_preserve small=false agent=build error.error="AI_APICallError: Monthly usage limit reached. Resets in 2 days."`, ts),
	}, "\n")
	if err := os.WriteFile(serverLogPath, []byte(logText), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := &OpenCodeExecutor{Model: "opencode-go/kimi"}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", WorkspaceDir: workspaceDir})
	if err == nil {
		t.Fatal("expected error from opencode mock")
	}
	if tr == nil {
		t.Fatal("expected harnessapi.TryResult, got nil")
	}
	if tr.Evidence == nil {
		t.Fatal("expected evidence")
	}
	if tr.Evidence.Category != reliability.CategoryAgentError {
		t.Errorf("Category = %q, want %q (in-band evidence should be preserved)", tr.Evidence.Category, reliability.CategoryAgentError)
	}
	if !strings.Contains(tr.Evidence.Message, "Agent runtime crashed") {
		t.Errorf("Message = %q, want in-band message", tr.Evidence.Message)
	}
	if tr.Evidence.Source == "opencode_disk_log" {
		t.Errorf("Source = opencode_disk_log, want in-band source (in-band evidence should not be replaced)")
	}
}

// Test the full executor integration: budget-killed try (exit 1) with no parseable
// output and only structural server log -> opencode_disk_log evidence.
func TestOpenCodeExecutor_DiskLogFallback_BudgetKilled(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "opencode")
	// Exit 1 with no output at all - simulates a budget kill.
	script := `#!/bin/sh
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	workspaceDir := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	serverLogPath := filepath.Join(tmp, "opencode.log")
	oldServerLogPath := openCodeServerLogPath
	openCodeServerLogPath = func() string { return serverLogPath }
	t.Cleanup(func() { openCodeServerLogPath = oldServerLogPath })

	ts := time.Now().UTC().Format(time.RFC3339Nano)
	logText := strings.Join([]string{
		fmt.Sprintf(`timestamp=%s level=ERROR message="API key invalid" providerID=opencode-go session.id=ses_wrong`, ts),
		fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_budget directory="%s"`, ts, workspaceDir),
		fmt.Sprintf(`timestamp=%s level=INFO message="loop session.id=ses_budget"`, ts),
		fmt.Sprintf(`timestamp=%s level=INFO message=stream session.id=ses_budget`, ts),
	}, "\n")
	if err := os.WriteFile(serverLogPath, []byte(logText), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := &OpenCodeExecutor{Model: "opencode-go/kimi"}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", WorkspaceDir: workspaceDir})
	if err == nil {
		t.Fatal("expected error from opencode mock")
	}
	if tr == nil {
		t.Fatal("expected harnessapi.TryResult, got nil")
	}
	if tr.Evidence == nil {
		t.Fatal("expected disk-log evidence for budget-killed try")
	}
	if tr.Evidence.Source != "opencode_disk_log" {
		t.Errorf("Source = %q, want opencode_disk_log", tr.Evidence.Source)
	}
	if tr.Evidence.Category != reliability.CategoryUnidentifiedIssue {
		t.Errorf("Category = %q, want %q", tr.Evidence.Category, reliability.CategoryUnidentifiedIssue)
	}
	if tr.Evidence.Message != "try budget exhausted; no parseable output" {
		t.Errorf("Message = %q, want 'try budget exhausted; no parseable output'", tr.Evidence.Message)
	}
}

func TestOpenCodeExecutor_DiskLogFallback_BudgetKilledRecognizedAuth(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "opencode")
	script := `#!/bin/sh
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	workspaceDir := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	serverLogPath := filepath.Join(tmp, "opencode.log")
	oldServerLogPath := openCodeServerLogPath
	openCodeServerLogPath = func() string { return serverLogPath }
	t.Cleanup(func() { openCodeServerLogPath = oldServerLogPath })

	ts := time.Now().UTC().Format(time.RFC3339Nano)
	logText := strings.Join([]string{
		fmt.Sprintf(`timestamp=%s level=INFO message=created id=ses_auth directory="%s"`, ts, workspaceDir),
		fmt.Sprintf(`timestamp=%s level=INFO message="loop session.id=ses_auth"`, ts),
		fmt.Sprintf(`timestamp=%s level=ERROR message="API key invalid" providerID=opencode-go session.id=ses_auth`, ts),
		fmt.Sprintf(`timestamp=%s level=WARN message="permission granted" providerID=opencode-go session.id=ses_auth`, ts),
	}, "\n")
	if err := os.WriteFile(serverLogPath, []byte(logText), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := &OpenCodeExecutor{Model: "opencode-go/kimi"}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", WorkspaceDir: workspaceDir})
	if err == nil {
		t.Fatal("expected error from opencode mock")
	}
	if tr == nil {
		t.Fatal("expected harnessapi.TryResult, got nil")
	}
	if tr.Evidence == nil {
		t.Fatal("expected disk-log evidence")
	}
	if tr.Evidence.Source != openCodeDiskLogSource {
		t.Errorf("Source = %q, want %q", tr.Evidence.Source, openCodeDiskLogSource)
	}
	if tr.Evidence.Category != reliability.CategoryAuthOrProxy {
		t.Errorf("Category = %q, want %q", tr.Evidence.Category, reliability.CategoryAuthOrProxy)
	}
	if !strings.Contains(tr.Evidence.RawSignal, "API key invalid") {
		t.Errorf("RawSignal = %q, want auth error line", tr.Evidence.RawSignal)
	}
	if strings.Contains(tr.Evidence.RawSignal, "permission granted") {
		t.Errorf("RawSignal = %q, must not contain permission noise", tr.Evidence.RawSignal)
	}
}

// Test noteworthy line filter.
func TestOpenCodeIsNoteworthyLogLine(t *testing.T) {
	for _, tc := range []struct {
		fields map[string]string
		want   bool
	}{
		{map[string]string{"level": "ERROR", "message": "stream error"}, true},
		{map[string]string{"level": "WARN", "message": "something"}, true},
		{map[string]string{"level": "INFO", "message": "created"}, true},
		{map[string]string{"level": "INFO", "message": "stream"}, true},
		{map[string]string{"level": "INFO", "message": "loop session.id=abc"}, true},
		{map[string]string{"level": "INFO", "message": "normal info"}, false},
		{map[string]string{"level": "DEBUG", "message": "debug stuff"}, false},
	} {
		got := openCodeIsNoteworthyLogLine(tc.fields)
		if got != tc.want {
			t.Errorf("openCodeIsNoteworthyLogLine(%v) = %v, want %v", tc.fields, got, tc.want)
		}
	}
}

// Test the 16-line cap.
func TestOpenCodeDiskLog_MaxLinesCap(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	var entries []openCodeServerLogEntry
	for i := 0; i < 30; i++ {
		line := fmt.Sprintf(`timestamp=%s level=WARN message="warning %d"`, ts, i)
		entries = append(entries, openCodeServerLogEntry{
			raw:    line,
			fields: parseOpenCodeLogFields(line),
		})
	}
	ev := openCodeDiskLogEvidence(entries, "test/model")
	if ev == nil {
		t.Fatal("expected evidence")
	}
	// The line cap keeps only the last 16 (indices 14-29). Message comes
	// from the last error line's message field; verify it's from entry 29.
	if ev.Message != "warning 29" {
		t.Errorf("Message = %q, want 'warning 29' (last entry should be used)", ev.Message)
	}
	// RawSignal is built from the capped tail and then tail-bounded to 256
	// runes, so the most recent warnings must survive while earlier ones can
	// fall off.
	if strings.Contains(ev.RawSignal, "warning 0\"") {
		t.Error("expected earliest warnings (0-13) to be trimmed by 16-line cap")
	}
	if !strings.Contains(ev.RawSignal, "warning 29") {
		t.Error("expected warning 29 (most recent entry) in RawSignal")
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

func TestTailString(t *testing.T) {
	if got := process.TailString("hello", 100); got != "hello" {
		t.Errorf("tailString short = %q, want hello", got)
	}
	if got := process.TailString("  hello  ", 100); got != "hello" {
		t.Errorf("tailString trimmed = %q, want hello", got)
	}
	got := process.TailString("abcdefghij", 4)
	if got != "…ghij" {
		t.Errorf("tailString long = %q, want …ghij", got)
	}
}

func TestTryResultSessionIDField(t *testing.T) {
	tr := &harnessapi.TryResult{Completed: true, Summary: "test", SessionID: "sess-123"}
	if tr.SessionID != "sess-123" {
		t.Errorf("SessionID = %q, want %q", tr.SessionID, "sess-123")
	}

	trZero := &harnessapi.TryResult{Completed: true}
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
	res, err := exec.Execute(context.Background(), harnessapi.RunOptions{
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
	_, err := exec.Execute(context.Background(), harnessapi.RunOptions{
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
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from opencode mock")
	}
	if tr == nil {
		t.Fatal("expected harnessapi.TryResult, got nil")
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
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from opencode mock")
	}
	if tr == nil {
		t.Fatal("expected harnessapi.TryResult, got nil")
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

// --- Harness-specific effort injection integration tests ---

func TestOpenCodeExecutor_EffortFlagInArgs(t *testing.T) {
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
	_, err := exec.Execute(context.Background(), harnessapi.RunOptions{
		Prompt:          "do work",
		ReasoningEffort: "max",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := string(argsData)
	if !strings.Contains(args, "--variant") || !strings.Contains(args, "max") {
		t.Errorf("opencode args missing --variant max, got:\n%s", args)
	}
}

// --- Route explicit model wins + effort coexistence ---

func TestOpenCodeExecutor_ExplicitModelWinsWithEffort(t *testing.T) {
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

	exec := &OpenCodeExecutor{Model: "zai-coding-plan/glm-default"}
	_, err := exec.Execute(context.Background(), harnessapi.RunOptions{
		Prompt:          "do work",
		Model:           "zai-coding-plan/glm-5.1",
		ReasoningEffort: "low",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := string(argsData)
	if !strings.Contains(args, "zai-coding-plan/glm-5.1") {
		t.Errorf("expected opts.Model to win, got:\n%s", args)
	}
	if strings.Contains(args, "zai-coding-plan/glm-default") {
		t.Errorf("executor default model should be overridden, got:\n%s", args)
	}
	if !strings.Contains(args, "--variant") || !strings.Contains(args, "low") {
		t.Errorf("variant flag should be present alongside explicit model, got:\n%s", args)
	}
}

// TestExecutors_PopulateResolvedModel verifies the opencode executor populates
// harnessapi.TryResult.ResolvedModel with the model actually passed to the CLI:
// the executor's configured default for a bare-alias route (opts.Model empty),
// and the per-try opts.Model override when set. This is the source the runner
// uses for the runner-tag fallback (tasks.md §2.2/§2.3/§2.5). The relocated
// claude/codex/antigravity adapters each carry their own
// TestExecutor_PopulateResolvedModel in internal/harness/<name>.
func TestExecutors_PopulateResolvedModel(t *testing.T) {
	t.Run("opencode", func(t *testing.T) {
		binDir, _ := testMockBinDir(t, "opencode")
		scriptPath := filepath.Join(binDir, "opencode")
		script := `#!/bin/sh
printf '%s\n' '{"type":"text","part":{"type":"text","text":"{\"completed\":true,\"summary\":\"ok\"}"}}'
`
		if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		exec := &OpenCodeExecutor{Model: "anthropic/claude-4"}
		if res, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"}); err != nil {
			t.Fatalf("Execute failed: %v", err)
		} else if res.ResolvedModel != "anthropic/claude-4" {
			t.Errorf("ResolvedModel = %q, want default %q", res.ResolvedModel, "anthropic/claude-4")
		}

		exec = &OpenCodeExecutor{Model: "anthropic/claude-4"}
		if res, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", Model: "zai-coding-plan/glm-5.1"}); err != nil {
			t.Fatalf("Execute failed: %v", err)
		} else if res.ResolvedModel != "zai-coding-plan/glm-5.1" {
			t.Errorf("ResolvedModel = %q, want opts override %q", res.ResolvedModel, "zai-coding-plan/glm-5.1")
		}
	})
}
