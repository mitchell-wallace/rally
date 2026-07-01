package antigravity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/reliability"
)

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

	exec := &Executor{Model: DefaultModel, PrintTimeout: time.Second}
	res, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", LogPath: filepath.Join(tmp, "try.log")})
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
	if !strings.Contains(string(snapshot), DefaultModel) {
		t.Errorf("settings snapshot missing model %q:\n%s", DefaultModel, string(snapshot))
	}

	settingsPath := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Errorf("settings file should be restored/removed, stat err = %v", err)
	}
}

func TestAntigravityAdapterCapabilities(t *testing.T) {
	a := &Executor{}
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
	exec := &Executor{PrintTimeout: time.Second}
	_, err := exec.Execute(context.Background(), harnessapi.RunOptions{
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
	exec := &Executor{PrintTimeout: time.Second}
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
	if strings.Contains(string(argsData), "--conversation=") {
		t.Errorf("antigravity args should not contain --conversation when ResumeSessionID is empty, got:\n%s", string(argsData))
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

	exec := &Executor{PrintTimeout: time.Second}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from antigravity mock")
	}
	if tr == nil {
		t.Fatal("expected harnessapi.TryResult with Evidence, got nil")
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

func TestAntigravityExecutor_EffortSkippedInArgs(t *testing.T) {
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
	exec := &Executor{PrintTimeout: time.Second}
	_, err := exec.Execute(context.Background(), harnessapi.RunOptions{
		Prompt:          "do work",
		ReasoningEffort: "high",
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
	// Antigravity resolves reasoning via model aliases/names only; no CLI flag.
	if strings.Contains(args, "--effort") || strings.Contains(args, "model_reasoning_effort") || strings.Contains(args, "--variant") {
		t.Errorf("antigravity args should not contain effort flag, but got:\n%s", args)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "antigravity has no reasoning-effort flag") {
		t.Errorf("expected unsupported-harness warning in try log, got:\n%s", string(logData))
	}
}

// TestExecutor_PopulateResolvedModel verifies the antigravity executor
// populates harnessapi.TryResult.ResolvedModel with the model actually passed
// to the CLI: the executor's configured default for a bare-alias route
// (opts.Model empty), and the per-try opts.Model override when set. Carved out
// of internal/agent/agent_test.go's TestExecutors_PopulateResolvedModel when
// the antigravity adapter moved into its own package.
func TestExecutor_PopulateResolvedModel(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(binDir, "agy")
	script := `#!/bin/sh
printf '%s\n' '{"completed":true,"summary":"ok"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &Executor{Model: DefaultModel, PrintTimeout: time.Second}
	if res, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	} else if res.ResolvedModel != DefaultModel {
		t.Errorf("ResolvedModel = %q, want default %q", res.ResolvedModel, DefaultModel)
	}

	exec = &Executor{Model: DefaultModel, PrintTimeout: time.Second}
	if res, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", Model: "Gemini 3 Pro"}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	} else if res.ResolvedModel != "Gemini 3 Pro" {
		t.Errorf("ResolvedModel = %q, want opts override %q", res.ResolvedModel, "Gemini 3 Pro")
	}
}
