package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/reliability"
)

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

func TestCodexAdapterCapabilities(t *testing.T) {
	c := &Executor{}
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

	exec := &Executor{}
	res, err := exec.Execute(context.Background(), harnessapi.RunOptions{
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

	exec := &Executor{}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from codex mock")
	}
	if tr == nil {
		t.Fatal("expected harnessapi.TryResult with Evidence, got nil")
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
	if strings.Contains(string(argsData), "resume") {
		t.Errorf("codex args should not contain resume when ResumeSessionID is empty, got:\n%s", string(argsData))
	}
}

// --- Harness-specific effort injection integration tests ---

func TestCodexExecutor_EffortFlagInArgs(t *testing.T) {
	binDir, argsPath := testMockBinDir(t, "codex")
	scriptPath := filepath.Join(binDir, "codex")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
printf '%%s\n' '{"type":"thread.started","thread_id":"codex-eff-sess"}'
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

	exec := &Executor{}
	_, err := exec.Execute(context.Background(), harnessapi.RunOptions{
		Prompt:          "do work",
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := string(argsData)
	if !strings.Contains(args, "-c") || !strings.Contains(args, "model_reasoning_effort=high") {
		t.Errorf("codex args missing -c model_reasoning_effort=high, got:\n%s", args)
	}
}

// --- Route explicit model wins + effort coexistence ---

func TestCodexExecutor_ExplicitModelWinsWithEffort(t *testing.T) {
	binDir, argsPath := testMockBinDir(t, "codex")
	scriptPath := filepath.Join(binDir, "codex")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
printf '%%s\n' '{"type":"thread.started","thread_id":"codex-model-sess"}'
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

	exec := &Executor{Model: "gpt-5.5-default"}
	_, err := exec.Execute(context.Background(), harnessapi.RunOptions{
		Prompt:          "do work",
		Model:           "gpt-5.5-extra-high",
		ReasoningEffort: "xhigh",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := string(argsData)
	// opts.Model must override the executor's default model.
	if !strings.Contains(args, "gpt-5.5-extra-high") {
		t.Errorf("expected opts.Model to win, got:\n%s", args)
	}
	if strings.Contains(args, "gpt-5.5-default") {
		t.Errorf("executor default model should be overridden, got:\n%s", args)
	}
	// Effort should still be injected alongside the explicit model.
	if !strings.Contains(args, "model_reasoning_effort=xhigh") {
		t.Errorf("effort flag should be present alongside explicit model, got:\n%s", args)
	}
}

// TestExecutor_PopulateResolvedModel verifies the codex executor populates
// harnessapi.TryResult.ResolvedModel with the model actually passed to the CLI:
// the executor's configured default for a bare-alias route (opts.Model empty),
// and the per-try opts.Model override when set. Carved out of
// internal/agent/agent_test.go's TestExecutors_PopulateResolvedModel when the
// codex adapter moved into its own package.
func TestExecutor_PopulateResolvedModel(t *testing.T) {
	binDir, _ := testMockBinDir(t, "codex")
	scriptPath := filepath.Join(binDir, "codex")
	script := `#!/bin/sh
printf '%s\n' '{"type":"thread.started","thread_id":"codex-sess"}'
next=0
for i in "$@"; do
  if [ "$next" = "1" ]; then printf '{"completed":true,"summary":"ok"}' > "$i"; break; fi
  if [ "$i" = "-o" ]; then next=1; fi
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &Executor{Model: "gpt-5.4"}
	if res, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	} else if res.ResolvedModel != "gpt-5.4" {
		t.Errorf("ResolvedModel = %q, want default %q", res.ResolvedModel, "gpt-5.4")
	}

	exec = &Executor{Model: "gpt-5.4"}
	if res, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", Model: "gpt-5.4-mini"}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	} else if res.ResolvedModel != "gpt-5.4-mini" {
		t.Errorf("ResolvedModel = %q, want opts override %q", res.ResolvedModel, "gpt-5.4-mini")
	}
}
