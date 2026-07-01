package opencode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
)

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

// TestResumeSupportImpliesSessionCapture is a contract test for the opencode
// executor: a harness reporting ResumeSupported()==true MUST also capture a
// session ID from realistic harness output. Capture is half the resume
// contract — without it, result.SessionID is always empty, the runner never has
// a session to feed back as the next attempt's ResumeSessionID, and resume
// silently no-ops even though the resume flag is wired (the opencode bug fixed
// in 0.8.7). The relocated claude/codex/antigravity adapters carry their own
// equivalent capture+capability coverage in-package.
func TestResumeSupportImpliesSessionCapture(t *testing.T) {
	// The extractor feeds a realistic opencode output sample through the
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
		"opencode": &Executor{},
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

	exec := &Executor{}
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

func TestOpenCodeAdapterCapabilities(t *testing.T) {
	o := &Executor{Model: "initial-model"}
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
	o := &Executor{Model: "original-model"}
	if err := o.RotateModel("new-model"); err != nil {
		t.Fatalf("RotateModel() returned error: %v", err)
	}
	if o.Model != "new-model" {
		t.Errorf("Model = %q, want %q", o.Model, "new-model")
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

	exec := &Executor{}
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

	exec := &Executor{}
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

	exec := &Executor{}
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

	exec := &Executor{Model: "zai-coding-plan/glm-default"}
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

// TestExecutor_PopulateResolvedModel verifies the opencode executor populates
// harnessapi.TryResult.ResolvedModel with the model actually passed to the CLI:
// the executor's configured default for a bare-alias route (opts.Model empty),
// and the per-try opts.Model override when set. This is the source the runner
// uses for the runner-tag fallback (tasks.md §2.2/§2.3/§2.5). The relocated
// claude/codex/antigravity adapters each carry their own
// TestExecutor_PopulateResolvedModel in internal/harness/<name>.
func TestExecutor_PopulateResolvedModel(t *testing.T) {
	binDir, _ := testMockBinDir(t, "opencode")
	scriptPath := filepath.Join(binDir, "opencode")
	script := `#!/bin/sh
printf '%s\n' '{"type":"text","part":{"type":"text","text":"{\"completed\":true,\"summary\":\"ok\"}"}}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &Executor{Model: "anthropic/claude-4"}
	if res, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	} else if res.ResolvedModel != "anthropic/claude-4" {
		t.Errorf("ResolvedModel = %q, want default %q", res.ResolvedModel, "anthropic/claude-4")
	}

	exec = &Executor{Model: "anthropic/claude-4"}
	if res, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", Model: "zai-coding-plan/glm-5.1"}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	} else if res.ResolvedModel != "zai-coding-plan/glm-5.1" {
		t.Errorf("ResolvedModel = %q, want opts override %q", res.ResolvedModel, "zai-coding-plan/glm-5.1")
	}
}
