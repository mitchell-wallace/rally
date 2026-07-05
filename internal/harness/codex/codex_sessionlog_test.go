package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/reliability"
)

// writeCodexRollout writes a codex rollout-*.jsonl session log under dir with
// the given session_meta scalars and event lines, using the real nested rollout
// shape captured from ~/.codex/sessions/2026/07/03 with sensitive content
// replaced by short stubs.
func writeCodexRollout(t *testing.T, dir, name, cwd, ts string, eventLines ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"timestamp":` + jsonString(ts))
	b.WriteString(`,"type":"session_meta","payload":{"session_id":"019f26cb-5611-7202-87ca-812ea5b71a01","id":"019f26cb-5611-7202-87ca-812ea5b71a01"`)
	b.WriteString(`,"timestamp":` + jsonString(ts))
	b.WriteString(`,"cwd":` + jsonString(cwd))
	b.WriteString(`,"originator":"codex_exec","cli_version":"0.142.4","source":"exec","thread_source":"user","model_provider":"openai"`)
	b.WriteString(`,"git":{"commit_hash":"abc123","branch":"main"}`)
	b.WriteString(`,"base_instructions":{"text":"BASE INSTRUCTIONS STUB - must never leak"}`)
	b.WriteString(`,"turn_context":{"payload":{"model":"gpt-5.5"}}`)
	b.WriteString("}}\n")
	for _, ln := range eventLines {
		b.WriteString(ln)
		b.WriteString("\n")
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// rfcNow returns an RFC3339 timestamp near time.Now(), suitable for a
// session_meta.timestamp within a try window.
func rfcNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// §4.6 (a): a codex exit-1 with a matching session log yields Source
// "codex_session_log" and Message from the last event subtype.
func TestCodexSessionLog_MatchingLogProducesSessionLogEvidence(t *testing.T) {
	sessionsRoot := t.TempDir()
	workspaceDir := filepath.Join(t.TempDir(), "ws")
	os.MkdirAll(workspaceDir, 0o755)
	t.Setenv("CODEX_HOME", sessionsRoot)

	ts := rfcNow()
	dayDir := filepath.Join(sessionsRoot, "sessions", "2026", "06", "26")
	writeCodexRollout(t, dayDir, "rollout-20260626T120000-deadbeef.jsonl", workspaceDir, ts,
		`{"timestamp":"2026-07-03T07:04:50.581Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1","started_at":1783062290,"model_context_window":258400}}`,
		`{"timestamp":"2026-07-03T07:05:01.555Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":4096}}}}`,
		`{"timestamp":"2026-07-03T07:05:02.000Z","type":"event_msg","payload":{"type":"response_item","item":{"content":[{"text":"full assistant body"}]}}}`,
		`{"timestamp":"2026-07-03T07:05:03.000Z","type":"event_msg","payload":{"type":"turn_context","context":{"reasoning":"high"}}}`,
		`{"timestamp":"2026-07-03T07:05:04.000Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-1"}}`,
	)

	start, end := time.Now().Add(-time.Minute), time.Now().Add(time.Minute)
	ev, err := codexSessionLogEvidence(workspaceDir, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev == nil {
		t.Fatal("expected evidence, got nil")
	}
	if ev.Source != "codex_session_log" {
		t.Errorf("Source = %q, want codex_session_log", ev.Source)
	}
	if ev.Message != "turn_aborted" {
		t.Errorf("Message = %q, want turn_aborted (last event subtype)", ev.Message)
	}
	if ev.Provider != reliability.ProviderOpenAI {
		t.Errorf("Provider = %q, want %q", ev.Provider, reliability.ProviderOpenAI)
	}
}

func TestCodexSessionLog_TaskCompleteIsAgentClassCompletedSessionEvidence(t *testing.T) {
	sessionsRoot := t.TempDir()
	workspaceDir := filepath.Join(t.TempDir(), "ws")
	os.MkdirAll(workspaceDir, 0o755)
	t.Setenv("CODEX_HOME", sessionsRoot)

	ts := rfcNow()
	dayDir := filepath.Join(sessionsRoot, "sessions", "2026", "07", "03")
	writeCodexRollout(t, dayDir, "rollout-2026-07-03T07-04-47-real-shape.jsonl", workspaceDir, ts,
		`{"timestamp":"2026-07-03T07:04:50.581Z","type":"event_msg","payload":{"type":"task_started","turn_id":"019f26cb-6073-7b61-8582-81f3d8e3f0aa","started_at":1783062290,"model_context_window":258400}}`,
		`{"timestamp":"2026-07-03T07:05:01.555Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":16106,"output_tokens":247}}}}`,
		`{"timestamp":"2026-07-03T07:08:36.186Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"019f26cb-6073-7b61-8582-81f3d8e3f0aa","last_agent_message":"completed summary body","completed_at":1783062516,"duration_ms":225631}}`,
	)

	start, end := time.Now().Add(-time.Minute), time.Now().Add(time.Minute)
	ev, err := codexSessionLogEvidence(workspaceDir, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev == nil {
		t.Fatal("expected evidence")
	}
	if ev.Category != reliability.CategoryUnidentifiedIssue {
		t.Fatalf("Category = %q, want %q", ev.Category, reliability.CategoryUnidentifiedIssue)
	}
	if got := reliability.CategoryToClass(ev.Category); got != reliability.FailureAgent {
		t.Fatalf("CategoryToClass(%q) = %q, want %q", ev.Category, got, reliability.FailureAgent)
	}
	wantMessage := "codex session completed (task_complete) but harness exited non-zero"
	if ev.Message != wantMessage {
		t.Fatalf("Message = %q, want %q", ev.Message, wantMessage)
	}
	if !strings.Contains(ev.RawSignal, "last_event=task_complete") {
		t.Fatalf("RawSignal = %q, want terminal event type", ev.RawSignal)
	}
	if strings.Contains(ev.RawSignal, "completed summary body") {
		t.Fatalf("RawSignal leaked task_complete message body: %q", ev.RawSignal)
	}
}

// §4.6 (b): a codex exit-1 with no matching session log yields executor-level
// CategoryHarnessLaunch + "codex_no_session_log", which ClassifyError Priority
// 1 resolves to StrategyFreshRestart / FailureInfra (NOT Rotate / FailureAgent).
func TestCodexSessionLog_NoMatchingLogProducesHarnessLaunchEvidence(t *testing.T) {
	sessionsRoot := t.TempDir()
	workspaceDir := filepath.Join(t.TempDir(), "ws")
	os.MkdirAll(workspaceDir, 0o755)
	t.Setenv("CODEX_HOME", sessionsRoot)

	// Sessions dir exists and is scannable, but the only rollout is for a
	// different workspace cwd.
	dayDir := filepath.Join(sessionsRoot, "sessions", "2026", "06", "26")
	writeCodexRollout(t, dayDir, "rollout-20260626T120000-other.jsonl",
		filepath.Join(t.TempDir(), "other-ws"), rfcNow(),
		`{"timestamp":"2026-07-03T07:08:36.186Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","last_agent_message":"completed summary"}}`,
	)

	start, end := time.Now().Add(-time.Minute), time.Now().Add(time.Minute)
	ev, err := codexSessionLogEvidence(workspaceDir, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev == nil {
		t.Fatal("expected codex_no_session_log evidence, got nil")
	}
	if ev.Source != "codex_no_session_log" {
		t.Errorf("Source = %q, want codex_no_session_log", ev.Source)
	}
	if ev.Category != reliability.CategoryHarnessLaunch {
		t.Errorf("Category = %q, want %q", ev.Category, reliability.CategoryHarnessLaunch)
	}
	if ev.Message != "codex launched but wrote no session log" {
		t.Errorf("Message = %q, want the harness_launch message", ev.Message)
	}

	// Confirm ClassifyError Priority 1 maps it correctly.
	decision := reliability.ClassifyError(nil, "codex", nil, ev)
	if decision.Category != reliability.CategoryHarnessLaunch {
		t.Errorf("decision Category = %q, want harness_launch", decision.Category)
	}
	if decision.Strategy != reliability.StrategyFreshRestart {
		t.Errorf("Strategy = %v, want StrategyFreshRestart", decision.Strategy)
	}
	if decision.FailureClass != reliability.FailureInfra {
		t.Errorf("FailureClass = %v, want FailureInfra", decision.FailureClass)
	}
	if decision.Evidence == nil || decision.Evidence.Source != "codex_no_session_log" {
		t.Errorf("decision Evidence Source not preserved: %+v", decision.Evidence)
	}
}

// §4.6 (c): base_instructions / token_count / turn_context / response_item
// payloads never appear in RawSignal.
func TestCodexSessionLog_RawSignalExcludesVerboseAndSensitivePayloads(t *testing.T) {
	sessionsRoot := t.TempDir()
	workspaceDir := filepath.Join(t.TempDir(), "ws")
	os.MkdirAll(workspaceDir, 0o755)
	t.Setenv("CODEX_HOME", sessionsRoot)

	ts := rfcNow()
	dayDir := filepath.Join(sessionsRoot, "sessions", "2026", "06", "26")
	writeCodexRollout(t, dayDir, "rollout-20260626T120000-secret.jsonl", workspaceDir, ts,
		`{"timestamp":"2026-07-03T07:04:50.581Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1","started_at":1783062290,"model_context_window":258400}}`,
		`{"timestamp":"2026-07-03T07:05:01.555Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":4096,"output_tokens":512}}}}`,
		`{"timestamp":"2026-07-03T07:05:02.000Z","type":"event_msg","payload":{"type":"response_item","item":{"content":[{"text":"FULL USER PROMPT BODY"}]}}}`,
		`{"timestamp":"2026-07-03T07:05:03.000Z","type":"event_msg","payload":{"type":"turn_context","context":{"model":"gpt-5.5","reasoning":"high"}}}`,
		`{"timestamp":"2026-07-03T07:08:36.186Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","last_agent_message":"FINAL SUMMARY BODY"}}`,
	)

	start, end := time.Now().Add(-time.Minute), time.Now().Add(time.Minute)
	ev, err := codexSessionLogEvidence(workspaceDir, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev == nil {
		t.Fatal("expected evidence")
	}
	for _, forbidden := range []string{
		"SECRET BASE INSTRUCTIONS",
		"BASE INSTRUCTIONS STUB",
		"FINAL SUMMARY BODY",
		"FULL USER PROMPT BODY",
		"reasoning",
		`"input":4096`,
		"turn_context",
		"token_count",
		"response_item",
		"base_instructions",
	} {
		if strings.Contains(ev.RawSignal, forbidden) {
			t.Errorf("RawSignal must not contain %q; got: %q", forbidden, ev.RawSignal)
		}
	}
	// The 256-rune bound holds even when the rollout is large.
	if n := len([]rune(ev.RawSignal)); n > 257 {
		t.Errorf("RawSignal is %d runes, exceeds 256-rune bound (+1 ellipsis): %q", n, ev.RawSignal)
	}
}

// §4.6 (d): a missing/unreadable session dir is not an error — the function
// returns (nil, err) so the executor falls through to the existing
// safe_exec_error path (nil harnessapi.TryResult.Evidence on no usable evidence).
func TestCodexSessionLog_MissingSessionsDirIsNotAnError(t *testing.T) {
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "does-not-exist"))
	workspaceDir := filepath.Join(t.TempDir(), "ws")
	os.MkdirAll(workspaceDir, 0o755)

	start, end := time.Now().Add(-time.Minute), time.Now().Add(time.Minute)
	ev, err := codexSessionLogEvidence(workspaceDir, start, end)
	if err == nil {
		t.Fatalf("expected a non-nil error for a missing sessions dir, got ev=%v", ev)
	}
	if ev != nil {
		t.Errorf("expected nil evidence on missing sessions dir, got %+v", ev)
	}
}

// A timestamp outside the try window must not match even when the cwd matches.
func TestCodexSessionLog_StaleTimestampDoesNotMatch(t *testing.T) {
	sessionsRoot := t.TempDir()
	workspaceDir := filepath.Join(t.TempDir(), "ws")
	os.MkdirAll(workspaceDir, 0o755)
	t.Setenv("CODEX_HOME", sessionsRoot)

	stale := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)
	dayDir := filepath.Join(sessionsRoot, "sessions", "2026", "06", "26")
	writeCodexRollout(t, dayDir, "rollout-stale.jsonl", workspaceDir, stale,
		`{"timestamp":"2026-07-03T07:04:50.581Z","type":"event_msg","payload":{"type":"task_started"}}`,
	)

	start, end := time.Now().Add(-time.Minute), time.Now().Add(time.Minute)
	ev, err := codexSessionLogEvidence(workspaceDir, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev == nil || ev.Source != "codex_no_session_log" {
		t.Fatalf("expected codex_no_session_log for stale match, got %+v", ev)
	}
}

// The newest matching rollout (by mtime) wins when several match.
func TestCodexSessionLog_NewestMatchWins(t *testing.T) {
	sessionsRoot := t.TempDir()
	workspaceDir := filepath.Join(t.TempDir(), "ws")
	os.MkdirAll(workspaceDir, 0o755)
	t.Setenv("CODEX_HOME", sessionsRoot)

	ts := rfcNow()
	dayDir := filepath.Join(sessionsRoot, "sessions", "2026", "06", "26")
	writeCodexRollout(t, dayDir, "rollout-older.jsonl", workspaceDir, ts,
		`{"timestamp":"2026-07-03T07:04:50.581Z","type":"event_msg","payload":{"type":"task_started"}}`,
	)
	older := filepath.Join(dayDir, "rollout-older.jsonl")
	past := time.Now().Add(-30 * time.Minute)
	os.Chtimes(older, past, past)

	writeCodexRollout(t, dayDir, "rollout-newer.jsonl", workspaceDir, ts,
		`{"timestamp":"2026-07-03T07:05:04.000Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-1"}}`,
	)
	newer := filepath.Join(dayDir, "rollout-newer.jsonl")
	os.Chtimes(newer, time.Now(), time.Now())

	start, end := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	ev, err := codexSessionLogEvidence(workspaceDir, start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev == nil {
		t.Fatal("expected evidence")
	}
	// The newest file's terminal event is turn_aborted.
	if ev.Message != "turn_aborted" {
		t.Errorf("Message = %q, want turn_aborted from newest rollout", ev.Message)
	}
}

// End-to-end: a real codex mock that exits 1 with no in-band signal wires the
// codex_no_session_log evidence onto the returned harnessapi.TryResult when no session log
// matches (the executor-level error path).
func TestCodexExecutor_NoSessionLogEvidenceWiredOnSilentExit1(t *testing.T) {
	sessionsRoot := t.TempDir()
	t.Setenv("CODEX_HOME", sessionsRoot)
	// Sessions dir exists but is empty (scannable, no match).
	os.MkdirAll(filepath.Join(sessionsRoot, "sessions"), 0o755)

	binDir := filepath.Join(t.TempDir(), "bin")
	os.MkdirAll(binDir, 0o755)
	scriptPath := filepath.Join(binDir, "codex")
	// Silent exit-1: writes nothing to either stream.
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	workspaceDir := filepath.Join(t.TempDir(), "ws")
	os.MkdirAll(workspaceDir, 0o755)

	exec := &Executor{}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", WorkspaceDir: workspaceDir})
	if err == nil {
		t.Fatal("expected error from codex mock")
	}
	if tr == nil {
		t.Fatal("expected harnessapi.TryResult with codex_no_session_log Evidence, got nil")
	}
	if tr.Evidence == nil {
		t.Fatal("expected Evidence on silent exit-1, got nil (would fall through to safe_exec_error)")
	}
	if tr.Evidence.Source != "codex_no_session_log" {
		t.Errorf("Evidence.Source = %q, want codex_no_session_log", tr.Evidence.Source)
	}
	if tr.Evidence.Category != reliability.CategoryHarnessLaunch {
		t.Errorf("Evidence.Category = %q, want harness_launch", tr.Evidence.Category)
	}
}

// End-to-end: a codex mock that exits 1 silently, but whose session log matches
// the workspace, yields codex_session_log evidence on the returned harnessapi.TryResult.
func TestCodexExecutor_SessionLogEvidenceWiredOnMatchingLog(t *testing.T) {
	sessionsRoot := t.TempDir()
	t.Setenv("CODEX_HOME", sessionsRoot)

	workspaceDir := filepath.Join(t.TempDir(), "ws")
	os.MkdirAll(workspaceDir, 0o755)

	ts := rfcNow()
	dayDir := filepath.Join(sessionsRoot, "sessions", "2026", "06", "26")
	writeCodexRollout(t, dayDir, "rollout-20260626T120000-wired.jsonl", workspaceDir, ts,
		`{"timestamp":"2026-07-03T07:04:50.581Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-07-03T07:05:04.000Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-1"}}`,
	)
	// Ensure mtime is fresh.
	os.Chtimes(filepath.Join(dayDir, "rollout-20260626T120000-wired.jsonl"), time.Now(), time.Now())

	binDir := filepath.Join(t.TempDir(), "bin")
	os.MkdirAll(binDir, 0o755)
	scriptPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &Executor{}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work", WorkspaceDir: workspaceDir})
	if err == nil {
		t.Fatal("expected error from codex mock")
	}
	if tr == nil || tr.Evidence == nil {
		t.Fatalf("expected harnessapi.TryResult with codex_session_log Evidence, got %+v", tr)
	}
	if tr.Evidence.Source != "codex_session_log" {
		t.Errorf("Evidence.Source = %q, want codex_session_log", tr.Evidence.Source)
	}
	if tr.Evidence.Message != "turn_aborted" {
		t.Errorf("Evidence.Message = %q, want turn_aborted", tr.Evidence.Message)
	}
}
