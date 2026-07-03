package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

func TestRunOne_OrdinaryAgentErrorEvidenceStaysTryTelemetryOnly(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{
				Completed: false,
				Summary:   "provider object failure",
				Evidence: &reliability.FailureEvidence{
					Category:  reliability.CategoryAgentError,
					RawSignal: `{"type":"error","error":{"message":"model refused"}}`,
					Message:   "model refused",
				},
			}, nil
		},
	}

	sink := &capturingSink{}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      1,
		LapsEnabled:      true,
		Resolver:         cheapTestResolver,
	}, map[string]harnessapi.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", IsLapsBacked: true, LapID: "lap-1"},
		nil, nil, false, false, nil, nil, io.Discard,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	if res.Success {
		t.Fatal("expected ordinary agent error to fail the run")
	}
	if got := findFailureCount(sink, "failed:"); got != 0 {
		t.Fatalf("ordinary agent_error emitted RallyFailure captures = %d, want 0", got)
	}

	log := findTryLogByOutcome(t, sink, string(reliability.OutcomeFailed))
	if log["failure_evidence.message"] != "model refused" {
		t.Fatalf("try log failure_evidence.message = %#v", log["failure_evidence.message"])
	}
	if log["failure_evidence.source"] != "executor_evidence" {
		t.Fatalf("try log failure_evidence.source = %#v", log["failure_evidence.source"])
	}
	if log["failure_evidence.evidence_shape"] != "provider_object" {
		t.Fatalf("try log evidence_shape = %#v", log["failure_evidence.evidence_shape"])
	}
	if log["failure_evidence.provider_signal"] == "" {
		t.Fatalf("try log provider_signal missing: %#v", log)
	}

	span := findTrySpanByOutcome(t, sink, string(reliability.OutcomeFailed))
	evidence, ok := span.data["failure_evidence"].(map[string]interface{})
	if !ok {
		t.Fatalf("try span failure_evidence context missing: %#v", span.data)
	}
	if evidence["source"] != "executor_evidence" || evidence["evidence_shape"] != "provider_object" {
		t.Fatalf("try span failure_evidence = %#v", evidence)
	}
}

// TestRunOne_TerminalTryFailure_EnrichesUsageLimitState drives a terminal try
// failure whose evidence is a usage limit and asserts the capture carries the
// attempt/budget, resolved category, the runner's resilience state, the parsed
// quota/reset, and the bounded raw provider signal.
func TestRunOne_TerminalTryFailure_EnrichesUsageLimitState(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	reset := time.Now().Add(3 * time.Hour).UTC()
	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			// usage_limit is FailureAgent (not issue-worthy alone); the harness
			// error makes this capture issue-worthy. The evidence supplies the
			// category + quota/reset/raw-signal the capture should carry.
			return &harnessapi.TryResult{
				Completed: false,
				Summary:   "boom",
				Evidence: &reliability.FailureEvidence{
					Category:   reliability.CategoryUsageLimit,
					QuotaScope: "anthropic",
					ResetAt:    &reset,
					RawSignal:  "You have hit your usage limit",
					Message:    "quota exhausted",
				},
			}, fmt.Errorf("harness exited non-zero")
		},
	}

	sink := &capturingSink{}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      1,
		LapsEnabled:      true,
		Resolver:         cheapTestResolver,
	}, map[string]harnessapi.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	if _, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", IsLapsBacked: true, LapID: "lap-1"},
		nil, nil, false, false, nil, nil, io.Discard,
	); err != nil {
		t.Fatalf("runOne error = %v", err)
	}

	evt := findFailure(t, sink, "failed:")
	wantTag(t, evt.Tags, "attempt", "1")
	wantTag(t, evt.Tags, "max_attempts", "1")
	wantTag(t, evt.Tags, "failure_category", "usage_limit")
	wantFingerprintCategory(t, evt, "usage_limit")
	wantTag(t, evt.Tags, "quota_scope", "anthropic")
	if evt.Tags["reset_at"] == "" {
		t.Error("reset_at tag missing on usage-limit capture")
	}
	// The failing runner had no prior resilience events, so it is active.
	wantTag(t, evt.Tags, "agent_state", "active")

	ev, ok := evt.Contexts["failure_evidence"]
	if !ok {
		t.Fatal("failure_evidence context missing on usage-limit capture")
	}
	if ev["raw_signal"] != "You have hit your usage limit" {
		t.Errorf("raw_signal = %v, want the provider signal text", ev["raw_signal"])
	}
	if ev["message"] != "quota exhausted" {
		t.Errorf("message = %v, want the bounded failure message", ev["message"])
	}
}

func TestRunOne_ExecErrorWithoutEvidenceUsesClassifierEvidence(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{Completed: false, Summary: "launcher failed"}, fmt.Errorf("launcher failed\nstderr: full command transcript")
		},
	}

	sink := &capturingSink{}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      1,
		LapsEnabled:      true,
		Resolver:         cheapTestResolver,
	}, map[string]harnessapi.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", IsLapsBacked: true, LapID: "lap-1"},
		nil, nil, false, false, nil, nil, io.Discard,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	if res.Success {
		t.Fatal("expected exec error to fail the run")
	}

	log := findTryLogByOutcome(t, sink, string(reliability.OutcomeFailed))
	if log["failure_evidence.raw_signal"] != "no log output" {
		t.Fatalf("try log raw_signal = %#v, want empty-log classifier marker", log["failure_evidence.raw_signal"])
	}
	if log["failure_evidence.source"] != "unmatched" {
		t.Fatalf("try log source = %#v", log["failure_evidence.source"])
	}

	evt := findFailure(t, sink, "failed:")
	ev, ok := evt.Contexts["failure_evidence"]
	if !ok {
		t.Fatal("failure_evidence context missing on exec error capture")
	}
	if ev["raw_signal"] != "no log output" || ev["source"] != "unmatched" {
		t.Fatalf("failure evidence = %#v", ev)
	}
}

// TestRunOne_ExecErrorWithLogPatternUsesTextPatternEvidence drives a failing
// try whose transcript carries a recognisable text pattern and no executor
// Evidence. ClassifyError Priority 4 must produce text_pattern evidence whose
// source / category / message / raw_signal flow onto the try-log telemetry.
// (Tasks.md §3.10: Priority 4 -> source "text_pattern" + the pattern's category
// + the matched line present in raw_signal.)
func TestRunOne_ExecErrorWithLogPatternUsesTextPatternEvidence(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	const matchedLine = "Error: 503 service unavailable from upstream provider"
	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("starting agent\n"+matchedLine+"\nagent exited\n"), 0o644)
			}
			return &harnessapi.TryResult{Completed: false, Summary: "upstream error"}, fmt.Errorf("agent failed")
		},
	}

	sink := &capturingSink{}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      1,
		LapsEnabled:      true,
		Resolver:         cheapTestResolver,
	}, map[string]harnessapi.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", IsLapsBacked: true, LapID: "lap-1"},
		nil, nil, false, false, nil, nil, io.Discard,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	if res.Success {
		t.Fatal("expected exec error to fail the run")
	}

	log := findTryLogByOutcome(t, sink, string(reliability.OutcomeFailed))
	if log["failure_evidence.source"] != "text_pattern" {
		t.Fatalf("try log source = %#v, want text_pattern", log["failure_evidence.source"])
	}
	if log["failure_evidence.message"] != "server error 5xx" {
		t.Fatalf("try log message = %#v, want the pattern name", log["failure_evidence.message"])
	}
	raw, _ := log["failure_evidence.raw_signal"].(string)
	if !strings.Contains(raw, "503 service unavailable") {
		t.Fatalf("try log raw_signal = %#v, want the matched line present", log["failure_evidence.raw_signal"])
	}

	// transient_infra is an infra-class failure, so it also surfaces as an Issue
	// carrying the pattern's category tag and the text_pattern evidence context.
	evt := findFailure(t, sink, "failed:")
	wantTag(t, evt.Tags, "failure_category", string(reliability.CategoryTransientInfra))
	ev, ok := evt.Contexts["failure_evidence"]
	if !ok {
		t.Fatal("failure_evidence context missing on text-pattern capture")
	}
	if ev["source"] != "text_pattern" {
		t.Fatalf("failure evidence source = %#v, want text_pattern", ev["source"])
	}
	if rawSig, _ := ev["raw_signal"].(string); !strings.Contains(rawSig, "503 service unavailable") {
		t.Fatalf("failure evidence raw_signal = %#v, want the matched line present", ev["raw_signal"])
	}
}

func TestLastOutputAgeUsesTryLogMtimeWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "try.log")
	if err := os.WriteFile(path, []byte("last output\n"), 0o644); err != nil {
		t.Fatalf("write try log: %v", err)
	}
	modTime := time.Now().Add(-7 * time.Second).Truncate(time.Second)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes try log: %v", err)
	}

	age, ok := lastOutputAge(path, modTime.Add(9*time.Second))
	if !ok {
		t.Fatal("lastOutputAge ok = false, want true")
	}
	if age != 9*time.Second {
		t.Fatalf("age = %v, want 9s", age)
	}

	empty := filepath.Join(dir, "empty.log")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatalf("write empty log: %v", err)
	}
	if _, ok := lastOutputAge(empty, time.Now()); ok {
		t.Fatal("lastOutputAge(empty) ok = true, want false")
	}
}

// TestRunOne_TerminalTryFailure_ShortRateLimit verifies a short_rate_limit
// terminal try failure captures the category, quota fields, and the bounded
// failure_evidence context with raw_signal and message.
func TestRunOne_TerminalTryFailure_ShortRateLimit(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)

	reset := time.Now().Add(2 * time.Minute).UTC()
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{
				Completed: false,
				Summary:   "rate limited",
				Evidence: &reliability.FailureEvidence{
					Category:   reliability.CategoryShortRateLimit,
					QuotaScope: "anthropic",
					ResetAt:    &reset,
					RawSignal:  "429 Too Many Requests retry_after=120",
					Message:    "short rate limit hit",
				},
			}, fmt.Errorf("harness exited non-zero")
		},
	}

	r := makeRunner(t, s, workspaceDir, sink, exec, 1)

	if _, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", IsLapsBacked: true, LapID: "lap-1"},
		nil, nil, false, false, nil, nil, io.Discard,
	); err != nil {
		t.Fatalf("runOne error = %v", err)
	}

	evt := findFailure(t, sink, "failed:")
	wantTag(t, evt.Tags, "failure_category", "short_rate_limit")
	wantTag(t, evt.Tags, "attempt", "1")
	wantTag(t, evt.Tags, "max_attempts", "1")
	wantTag(t, evt.Tags, "agent_state", "active")
	wantTag(t, evt.Tags, "quota_scope", "anthropic")
	if evt.Tags["reset_at"] == "" {
		t.Error("reset_at tag missing on short_rate_limit capture")
	}
	ev, ok := evt.Contexts["failure_evidence"]
	if !ok {
		t.Fatal("failure_evidence context missing on short_rate_limit capture")
	}
	if ev["raw_signal"] != "429 Too Many Requests retry_after=120" {
		t.Errorf("raw_signal = %v", ev["raw_signal"])
	}
	if ev["message"] != "short rate limit hit" {
		t.Errorf("message = %v", ev["message"])
	}

	diag := findEvent(t, sink, "provider limit signal")
	if diag.Level != telemetry.LevelInfo {
		t.Errorf("diagnostic level = %q, want %q", diag.Level, telemetry.LevelInfo)
	}
	wantTag(t, diag.Tags, "event_kind", "limit_signal")
	wantTag(t, diag.Tags, "failure_category", "short_rate_limit")
	wantTag(t, diag.Tags, "quota_scope", "anthropic")
}

// TestRunOne_TerminalTryFailure_ProviderOverloaded verifies a
// provider_overloaded terminal try failure carries the evidence context.
func TestRunOne_TerminalTryFailure_ProviderOverloaded(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)

	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{
				Completed: false,
				Summary:   "overloaded",
				Evidence: &reliability.FailureEvidence{
					Category:  reliability.CategoryProviderOverloaded,
					RawSignal: "529 Overloaded error: API is temporarily overloaded",
					Message:   "provider overloaded",
				},
			}, fmt.Errorf("harness exited non-zero")
		},
	}

	r := makeRunner(t, s, workspaceDir, sink, exec, 1)

	if _, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", IsLapsBacked: true, LapID: "lap-1"},
		nil, nil, false, false, nil, nil, io.Discard,
	); err != nil {
		t.Fatalf("runOne error = %v", err)
	}

	evt := findFailure(t, sink, "failed:")
	wantTag(t, evt.Tags, "failure_category", "provider_overloaded")
	ev, ok := evt.Contexts["failure_evidence"]
	if !ok {
		t.Fatal("failure_evidence context missing on provider_overloaded capture")
	}
	if ev["raw_signal"] != "529 Overloaded error: API is temporarily overloaded" {
		t.Errorf("raw_signal = %v", ev["raw_signal"])
	}
	if ev["message"] != "provider overloaded" {
		t.Errorf("message = %v", ev["message"])
	}
}

// TestRunOne_TerminalTryFailure_NonLimitCategory_BoundedEvidenceContext verifies
// that an issue-worthy terminal try failure classified as a non-limit category
// can carry bounded explicit failure_evidence without quota/reset fields.
func TestRunOne_TerminalTryFailure_NonLimitCategory_BoundedEvidenceContext(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)

	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{
				Completed: false,
				Summary:   "bad model",
				Evidence: &reliability.FailureEvidence{
					Category:  reliability.CategoryInvalidModel,
					RawSignal: "model not found: gpt-6-turbo-preview",
					Message:   "invalid model requested",
				},
			}, fmt.Errorf("harness exited non-zero")
		},
	}

	r := makeRunner(t, s, workspaceDir, sink, exec, 1)

	if _, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", IsLapsBacked: true, LapID: "lap-1"},
		nil, nil, false, false, nil, nil, io.Discard,
	); err != nil {
		t.Fatalf("runOne error = %v", err)
	}

	evt := findFailure(t, sink, "failed:")
	wantTag(t, evt.Tags, "failure_category", "invalid_model")
	for _, k := range []string{"quota_scope", "reset_at", "reset_after"} {
		wantNoTag(t, evt.Tags, k)
	}
	wantContextKey(t, evt, "failure_evidence", "raw_signal", "model not found: gpt-6-turbo-preview")
	wantContextKey(t, evt, "failure_evidence", "message", "invalid model requested")
	wantContextKey(t, evt, "failure_evidence", "source", "executor_evidence")
	wantContextKey(t, evt, "failure_evidence", "evidence_shape", "plain_text")
}

// TestRunOne_TerminalTryFailure_ScrubsHomePathInRawSignal drives a usage-limit
// failure whose raw_signal and message contain real home-directory paths and
// prompt/transcript-looking content, and asserts the scrubber collapses paths
// and the evidence context contains only the bounded evidence-shape keys.
func TestRunOne_TerminalTryFailure_ScrubsHomePathInRawSignal(t *testing.T) {
	prev := telemetry.HomeDir()
	telemetry.SetHomeDir("/home/engineer")
	defer telemetry.SetHomeDir(prev)

	s, workspaceDir, sink := setupRunnerForFailureTest(t)

	rawWithHomePath := `error reading /home/engineer/.config/rally/cache.json: ` +
		`you have exceeded your usage limit. prompt="analyze this" transcript=full`
	msgWithHomePath := `provider error at /home/engineer/.rally/state: ` +
		`usage limit reached. see /home/engineer/logs/trace.log`

	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{
				Completed: false,
				Summary:   "usage limit",
				Evidence: &reliability.FailureEvidence{
					Category:   reliability.CategoryUsageLimit,
					QuotaScope: "anthropic",
					RawSignal:  rawWithHomePath,
					Message:    msgWithHomePath,
				},
			}, fmt.Errorf("harness exited non-zero")
		},
	}

	r := makeRunner(t, s, workspaceDir, sink, exec, 1)

	if _, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", IsLapsBacked: true, LapID: "lap-1"},
		nil, nil, false, false, nil, nil, io.Discard,
	); err != nil {
		t.Fatalf("runOne error = %v", err)
	}

	evt := findFailure(t, sink, "failed:")
	ev, ok := evt.Contexts["failure_evidence"]
	if !ok {
		t.Fatal("failure_evidence context missing")
	}

	rawSignal, _ := ev["raw_signal"].(string)
	message, _ := ev["message"].(string)

	for _, v := range []string{rawSignal, message} {
		if strings.Contains(v, "engineer") {
			t.Errorf("username leaked into evidence value %q", v)
		}
		if strings.Contains(v, "/home/engineer") {
			t.Errorf("unresolved home path in evidence value %q", v)
		}
	}

	allowed := map[string]struct{}{"raw_signal": {}, "message": {}, "evidence_shape": {}, "provider_signal": {}}
	for k := range ev {
		if _, ok := allowed[k]; !ok {
			t.Errorf("unexpected key %q in failure_evidence context", k)
		}
	}
}
