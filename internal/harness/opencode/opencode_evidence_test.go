package opencode

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

	exec := &Executor{Model: "opencode-go/kimi"}
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

	exec := &Executor{Model: "opencode-go/kimi"}
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

	exec := &Executor{Model: "opencode-go/kimi"}
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

	exec := &Executor{Model: "opencode-go/kimi"}
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

	exec := &Executor{Model: "opencode-go/kimi"}
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

	exec := &Executor{Model: "opencode-go/kimi"}
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

	exec := &Executor{Model: "anthropic/claude-4"}
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

	exec := &Executor{Model: "openai/gpt-4o"}
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
