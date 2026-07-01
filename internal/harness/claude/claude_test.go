package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/reliability"
)

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
	finalText := strings.Repeat("start ", 1000) + "useful tail"
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
	if got := len([]rune(tr.Summary)); got > 1000 {
		t.Fatalf("summary rune length = %d, want <= %d", got, 1000)
	}
	if !strings.Contains(tr.Summary, "useful tail") {
		t.Errorf("summary = %q, want useful tail", tr.Summary)
	}
}

func TestClaudeAdapterCapabilities(t *testing.T) {
	c := &Executor{}
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
	exec := &Executor{}
	res, err := exec.Execute(context.Background(), harnessapi.RunOptions{
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
	exec := &Executor{}
	_, err := exec.Execute(context.Background(), harnessapi.RunOptions{
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

	exec := &Executor{}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from claude mock")
	}
	if tr == nil {
		t.Fatal("expected harnessapi.TryResult with Evidence, got nil")
	}
	if tr.Evidence == nil {
		t.Fatal("expected Evidence to be populated for rate_limit_event")
	}
	if tr.Evidence.Category != reliability.CategoryUsageLimit {
		t.Errorf("Category = %q, want %q (five-hour window is a usage limit)", tr.Evidence.Category, reliability.CategoryUsageLimit)
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

	exec := &Executor{}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from claude mock")
	}
	if tr != nil {
		t.Fatalf("expected nil harnessapi.TryResult for unknown error, got %+v", tr)
	}
}

func TestClaudeExecutor_EffortFlagInArgs(t *testing.T) {
	binDir, argsPath := testMockBinDir(t, "claude")
	scriptPath := filepath.Join(binDir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
printf '%%s\n' '{"type":"system","session_id":"mock-eff-sess"}'
printf '%%s\n' '{"type":"result","result":{"completed":true,"summary":"ok"}}'
`, argsPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "try.log")
	exec := &Executor{}
	_, err := exec.Execute(context.Background(), harnessapi.RunOptions{
		Prompt:          "do work",
		ReasoningEffort: "xhigh",
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
	if !strings.Contains(args, "--effort") || !strings.Contains(args, "xhigh") {
		t.Errorf("claude args missing --effort xhigh, got:\n%s", args)
	}
}

func TestClaudeExecutor_ExplicitModelWinsWithEffort(t *testing.T) {
	binDir, argsPath := testMockBinDir(t, "claude")
	scriptPath := filepath.Join(binDir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
printf '%%s\n' '{"type":"system","session_id":"mock-model-sess"}'
printf '%%s\n' '{"type":"result","result":{"completed":true,"summary":"ok"}}'
`, argsPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "try.log")
	exec := &Executor{Model: "claude-opus-default"}
	_, err := exec.Execute(context.Background(), harnessapi.RunOptions{
		Prompt:          "do work",
		Model:           "claude-opus-4-8",
		ReasoningEffort: "max",
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
	if !strings.Contains(args, "claude-opus-4-8") {
		t.Errorf("expected opts.Model to win, got:\n%s", args)
	}
	if strings.Contains(args, "claude-opus-default") {
		t.Errorf("executor default model should be overridden, got:\n%s", args)
	}
	if !strings.Contains(args, "--effort") || !strings.Contains(args, "max") {
		t.Errorf("effort flag should be present alongside explicit model, got:\n%s", args)
	}
}

// TestExecutor_PopulateResolvedModel verifies the claude executor populates
// harnessapi.TryResult.ResolvedModel with the model actually passed to the CLI:
// the executor's configured default for a bare-alias route (opts.Model empty),
// and the per-try opts.Model override when set. Carved out of
// internal/agent/agent_test.go's TestExecutors_PopulateResolvedModel when the
// claude adapter moved into its own package.
func TestExecutor_PopulateResolvedModel(t *testing.T) {
	binDir, _ := testMockBinDir(t, "claude")
	scriptPath := filepath.Join(binDir, "claude")
	script := `#!/bin/sh
printf '%s\n' '{"type":"system","session_id":"claude-sess"}'
printf '%s\n' '{"type":"result","result":{"completed":true,"summary":"ok"}}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &Executor{Model: "sonnet-4"}
	if res, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	} else if res.ResolvedModel != "sonnet-4" {
		t.Errorf("ResolvedModel = %q, want default %q", res.ResolvedModel, "sonnet-4")
	}

	exec = &Executor{Model: "sonnet-4"}
	if res, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", Model: "haiku-4"}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	} else if res.ResolvedModel != "haiku-4" {
		t.Errorf("ResolvedModel = %q, want opts override %q", res.ResolvedModel, "haiku-4")
	}
}
