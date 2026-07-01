package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/reliability"
)

func TestClaudeSessionLogPath_DashNormalizesWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := claudeSessionLogPath("/tmp/rally/workspace", "session-123")
	want := filepath.Join(home, ".claude", "projects", "-tmp-rally-workspace", "session-123.jsonl")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestClaudeSessionLogEvidence_EndTurnWithoutErrorProducesNoEvidence(t *testing.T) {
	lines := []string{
		`{"type":"system","subtype":"init","message":"claude session started"}`,
		`{"type":"assistant","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"completed normally"}]}}`,
	}

	if ev := claudeSessionLogEvidenceFromLines(lines); ev != nil {
		t.Fatalf("expected no fallback evidence for clean end_turn, got %+v", ev)
	}
}

func TestClaudeSessionLogEvidence_OverloadedError(t *testing.T) {
	lines := []string{
		`{"type":"user","display":"FULL_PROMPT_SECRET must never leak"}`,
		`{"type":"thinking","text":"private chain of thought"}`,
		`{"type":"tool_result","content":"first tool line\nSECRET_SECOND_TOOL_LINE"}`,
		`{"type":"assistant","message":{"stop_reason":"error","content":[{"type":"text","text":"overloaded_error: Anthropic overloaded"}]}}`,
	}

	ev := claudeSessionLogEvidenceFromLines(lines)
	if ev == nil {
		t.Fatal("expected fallback evidence")
	}
	if ev.Source != claudeSessionLogSource {
		t.Errorf("Source = %q, want %q", ev.Source, claudeSessionLogSource)
	}
	if ev.Category != reliability.CategoryProviderOverloaded {
		t.Errorf("Category = %q, want %q", ev.Category, reliability.CategoryProviderOverloaded)
	}
	if ev.Message != "assistant stop_reason=error" {
		t.Errorf("Message = %q, want assistant stop_reason=error", ev.Message)
	}
	if strings.Contains(ev.RawSignal, "FULL_PROMPT_SECRET") {
		t.Fatalf("RawSignal leaked user display: %q", ev.RawSignal)
	}
	if strings.Contains(ev.RawSignal, "private chain of thought") {
		t.Fatalf("RawSignal leaked thinking event: %q", ev.RawSignal)
	}
	if strings.Contains(ev.RawSignal, "SECRET_SECOND_TOOL_LINE") {
		t.Fatalf("RawSignal leaked tool_result body beyond first line: %q", ev.RawSignal)
	}
	if utf8.RuneCountInString(ev.RawSignal) > 256 {
		t.Fatalf("RawSignal has %d runes, want <= 256", utf8.RuneCountInString(ev.RawSignal))
	}
}

func TestClaudeSessionLogEvidence_UnrecognizedErrorText(t *testing.T) {
	lines := []string{
		`{"type":"assistant","message":{"stop_reason":"error","content":[{"type":"text","text":"unexpected session failure with no known provider marker"}]}}`,
	}

	ev := claudeSessionLogEvidenceFromLines(lines)
	if ev == nil {
		t.Fatal("expected fallback evidence")
	}
	if ev.Source != claudeSessionLogSource {
		t.Errorf("Source = %q, want %q", ev.Source, claudeSessionLogSource)
	}
	if ev.Category != reliability.CategoryUnidentifiedIssue {
		t.Errorf("Category = %q, want %q", ev.Category, reliability.CategoryUnidentifiedIssue)
	}
}

func TestClaudeSessionLogEvidence_AuthenticationError(t *testing.T) {
	lines := []string{
		`{"type":"assistant","message":{"stop_reason":"error","content":[{"type":"text","text":"authentication_error: invalid API key"}]}}`,
	}

	ev := claudeSessionLogEvidenceFromLines(lines)
	if ev == nil {
		t.Fatal("expected fallback evidence")
	}
	if ev.Category != reliability.CategoryAuthOrProxy {
		t.Errorf("Category = %q, want %q", ev.Category, reliability.CategoryAuthOrProxy)
	}
}

func TestClaudeExecutor_SessionLogFallbackWiredOnUnknownError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspaceDir := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "claude-fallback-session"
	writeClaudeSessionJSONL(t, home, workspaceDir, sessionID, []string{
		`{"type":"assistant","message":{"stop_reason":"error","content":[{"type":"text","text":"overloaded_error: provider saturated"}]}}`,
	})

	binDir, _ := testMockBinDir(t, "claude")
	scriptPath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'unstructured failure\\n'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &Executor{}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{
		Prompt:          "do work",
		WorkspaceDir:    workspaceDir,
		ResumeSessionID: sessionID,
	})
	if err == nil {
		t.Fatal("expected error from claude mock")
	}
	if tr == nil || tr.Evidence == nil {
		t.Fatalf("expected harnessapi.TryResult with claude_session_log Evidence, got %+v", tr)
	}
	if tr.Evidence.Source != claudeSessionLogSource {
		t.Errorf("Evidence.Source = %q, want %q", tr.Evidence.Source, claudeSessionLogSource)
	}
	if tr.Evidence.Category != reliability.CategoryProviderOverloaded {
		t.Errorf("Evidence.Category = %q, want %q", tr.Evidence.Category, reliability.CategoryProviderOverloaded)
	}
}

func TestClaudeExecutor_MissingSessionLogFallsThroughToInBandParse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspaceDir := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}

	binDir, _ := testMockBinDir(t, "claude")
	scriptPath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'unstructured failure\\n'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &Executor{}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{
		Prompt:          "do work",
		WorkspaceDir:    workspaceDir,
		ResumeSessionID: "missing-session",
	})
	if err == nil {
		t.Fatal("expected error from claude mock")
	}
	if tr != nil {
		t.Fatalf("expected nil harnessapi.TryResult when in-band parse and session fallback miss, got %+v", tr)
	}
}

func writeClaudeSessionJSONL(t *testing.T, home, workspaceDir, sessionID string, lines []string) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "projects", claudeProjectPathName(workspaceDir), sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
