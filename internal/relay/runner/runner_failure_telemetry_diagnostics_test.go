package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

func TestRunOne_LapPinMismatchTelemetryIsWarningDiagnostic(t *testing.T) {
	tests := []struct {
		name         string
		recordedLaps []string
		wantReason   string
	}{
		{name: "wrong lap", recordedLaps: []string{"other-lap"}, wantReason: "wrong_lap_consumed"},
		{name: "multiple laps", recordedLaps: []string{"lap-1", "lap-2"}, wantReason: "multi_lap_consumed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceDir := t.TempDir()
			rallyDir := store.RallyDir(workspaceDir)
			os.MkdirAll(rallyDir, 0o755)
			initRepo(t, workspaceDir)
			runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

			s := newTestStore(t, rallyDir)
			exec := &funcExecutor{
				fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
					rs, _ := progress.LoadRunState(workspaceDir)
					rs.RecordedLaps = tt.recordedLaps
					progress.SaveRunState(workspaceDir, rs)
					return &harnessapi.TryResult{Completed: true, Summary: "completed wrong lap"}, nil
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
				runTask{Name: "pinned task", Prompt: "do work", Assignee: "senior", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
				nil, nil, false, false, nil, nil, io.Discard,
			)
			if err != nil {
				t.Fatalf("runOne error = %v", err)
			}
			if !res.Success {
				t.Fatal("expected lap mismatch to remain warning-only")
			}
			if len(sink.failures) != 0 {
				t.Fatalf("lap mismatch captured %d RallyFailure event(s), want none", len(sink.failures))
			}
			if len(sink.events) != 1 {
				t.Fatalf("captured diagnostic events = %d, want 1", len(sink.events))
			}

			evt := sink.events[0].evt
			if evt.Level != telemetry.LevelWarning {
				t.Fatalf("diagnostic level = %q, want %q", evt.Level, telemetry.LevelWarning)
			}
			wantTag(t, evt.Tags, "event_kind", "lap_pin_mismatch")
			wantTag(t, evt.Tags, "mismatch_reason", tt.wantReason)
			wantTag(t, evt.Tags, "lap_id", "lap-1")
			wantTag(t, evt.Tags, "expected_lap_id", "lap-1")
			wantTag(t, evt.Tags, "consumed_lap_count", fmt.Sprintf("%d", len(tt.recordedLaps)))
			wantTag(t, evt.Tags, "consumed_lap_ids", strings.Join(tt.recordedLaps, ","))
			wantNoTag(t, evt.Tags, "failure_category")
			if evt.Level == telemetry.LevelError {
				t.Fatal("lap mismatch diagnostic must not be error-level")
			}

			log := findTryLogByOutcome(t, sink, string(reliability.OutcomeCompleted))
			if got := log["event_kind"]; got != "lap_pin_mismatch" {
				t.Fatalf("try log event_kind = %#v, want lap_pin_mismatch", got)
			}
			if got := log["mismatch_reason"]; got != tt.wantReason {
				t.Fatalf("try log mismatch_reason = %#v, want %q", got, tt.wantReason)
			}
			if got := log["expected_lap_id"]; got != "lap-1" {
				t.Fatalf("try log expected_lap_id = %#v, want lap-1", got)
			}
			if got := log["consumed_lap_count"]; got != len(tt.recordedLaps) {
				t.Fatalf("try log consumed_lap_count = %#v, want %d", got, len(tt.recordedLaps))
			}
			if got := log["consumed_lap_ids"]; got != strings.Join(tt.recordedLaps, ",") {
				t.Fatalf("try log consumed_lap_ids = %#v, want %q", got, strings.Join(tt.recordedLaps, ","))
			}
			if _, found := log["failure_category"]; found {
				t.Fatalf("lap mismatch try log must not carry failure_category: %#v", log)
			}

			span := findTrySpanByOutcome(t, sink, string(reliability.OutcomeCompleted))
			wantTag(t, span.tags, "event_kind", "lap_pin_mismatch")
			wantNoTag(t, span.tags, "failure_category")
			if got := span.data["mismatch_reason"]; got != tt.wantReason {
				t.Fatalf("try span mismatch_reason = %#v, want %q", got, tt.wantReason)
			}

			tries := s.AllTries()
			if len(tries) != 1 {
				t.Fatalf("persisted tries = %d, want 1", len(tries))
			}
			if tries[0].FailReason != tt.wantReason {
				t.Fatalf("FailReason = %q, want %q", tries[0].FailReason, tt.wantReason)
			}
		})
	}
}

func TestRunOne_ResolvedModelBareAliasPropagatesToFailureDiagnosticAndTryTags(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	reset := time.Now().Add(3 * time.Hour).UTC()
	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{
				Completed:     false,
				Summary:       "boom",
				ResolvedModel: "opencode-go/kimi-k2.6",
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
		AgentMixSpecs:    []string{"op"},
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
		harnessapi.ResolvedAgent{Harness: "opencode", Model: ""},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", IsLapsBacked: true, LapID: "lap-1"},
		nil, nil, false, false, nil, nil, io.Discard,
	); err != nil {
		t.Fatalf("runOne error = %v", err)
	}

	const wantRunner = "opencode:opencode-go/kimi-k2.6"

	evt := findFailure(t, sink, "failed:")
	wantTag(t, evt.Tags, "runner", wantRunner)

	diag := findEvent(t, sink, "provider limit signal")
	wantTag(t, diag.Tags, "runner", wantRunner)

	log := findLogByEvent(t, sink, "try")
	if got := log["runner"]; got != wantRunner {
		t.Fatalf("try log runner = %#v, want %q", got, wantRunner)
	}

	span := findTrySpanByOutcome(t, sink, string(reliability.OutcomeFailed))
	if got := span.tags["runner"]; got != wantRunner {
		t.Fatalf("try span runner = %q, want %q", got, wantRunner)
	}
}

func TestRunOneTimeoutHandoffOutcomesStaySpanLogOnly(t *testing.T) {
	var workspaceDir string
	attempt := 0
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			if attempt == 1 {
				<-ctx.Done()
				return &harnessapi.TryResult{Completed: false, SessionID: "sess-timeout"}, ctx.Err()
			}
			if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
				RunID:   "relay-1-run-1",
				Summary: "handoff",
				Handoff: &progress.HandoffEntry{
					Summary: "handoff",
				},
			}); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: true, Summary: "handoff recorded"}, nil
		},
	}
	r, _, ws := newTimeoutTestRunner(t, exec, Config{
		RetryBudget:    1,
		RunTimeout:     time.Hour,
		HandoffTimeout: time.Hour,
		LapsEnabled:    true,
	})
	workspaceDir = ws
	sink := &capturingSink{}
	r.SetTelemetry(sink)
	r.timerFunc = fireOnCall(1)

	res := driveRunOneTask(t, r, runTimeoutLapsTask())
	if !res.Success || res.Outcome != reliability.OutcomeHandoffRequested {
		t.Fatalf("run outcome = success %v outcome %q, want handoff_requested success", res.Success, res.Outcome)
	}
	if len(sink.failures) != 0 {
		t.Fatalf("timeout/handoff outcomes must not capture Issues, got %d", len(sink.failures))
	}

	runTimeoutLog := findTryLogByOutcome(t, sink, string(reliability.OutcomeRunTimeout))
	if _, found := runTimeoutLog["failure_category"]; found {
		t.Fatalf("run_timeout log must not carry failure_category: %#v", runTimeoutLog)
	}
	if runTimeoutLog["timeout_kind"] != "run_budget" {
		t.Fatalf("run_timeout timeout_kind = %#v, want run_budget", runTimeoutLog["timeout_kind"])
	}
	if runTimeoutLog["timeout_budget_ms"] != time.Hour.Milliseconds() {
		t.Fatalf("run_timeout timeout_budget_ms = %#v, want %d", runTimeoutLog["timeout_budget_ms"], time.Hour.Milliseconds())
	}
	if runTimeoutLog["session_captured"] != true || runTimeoutLog["resume_supported"] != true || runTimeoutLog["handoff_only_attempted"] != true {
		t.Fatalf("run_timeout context fields missing: %#v", runTimeoutLog)
	}
	runTimeoutSpan := findTrySpanByOutcome(t, sink, string(reliability.OutcomeRunTimeout))
	if runTimeoutSpan.tags["timeout_kind"] != "run_budget" || runTimeoutSpan.data["session_captured"] != true || runTimeoutSpan.data["handoff_only_attempted"] != true {
		t.Fatalf("run_timeout span context = tags %#v data %#v", runTimeoutSpan.tags, runTimeoutSpan.data)
	}
	handoffLog := findTryLogByOutcome(t, sink, string(reliability.OutcomeHandoffRequested))
	if runTimeoutLog["handoff_only_try_id"] != handoffLog["try_id"] {
		t.Fatalf("run_timeout handoff_only_try_id = %#v, continuation try_id %#v", runTimeoutLog["handoff_only_try_id"], handoffLog["try_id"])
	}
	if runTimeoutSpan.data["handoff_only_try_id"] != handoffLog["try_id"] {
		t.Fatalf("run_timeout span handoff_only_try_id = %#v, continuation try_id %#v", runTimeoutSpan.data["handoff_only_try_id"], handoffLog["try_id"])
	}
	if handoffLog["handoff_only"] != true {
		t.Fatalf("handoff continuation log handoff_only = %#v, want true", handoffLog["handoff_only"])
	}
	if handoffLog["timeout_kind"] != "handoff" || handoffLog["timeout_budget_ms"] != time.Hour.Milliseconds() {
		t.Fatalf("handoff continuation timeout fields = %#v", handoffLog)
	}
	if handoffLog["session_captured"] != true || handoffLog["resume_supported"] != true || handoffLog["handoff_only_attempted"] != true {
		t.Fatalf("handoff continuation context fields missing: %#v", handoffLog)
	}
	handoffSpan := findTrySpanByOutcome(t, sink, string(reliability.OutcomeHandoffRequested))
	if handoffSpan.tags["handoff_only"] != "true" || handoffSpan.data["handoff_only"] != true {
		t.Fatalf("handoff continuation span not identifiable as handoff-only: tags=%#v data=%#v", handoffSpan.tags, handoffSpan.data)
	}
	if handoffSpan.tags["timeout_kind"] != "handoff" || handoffSpan.data["session_captured"] != true {
		t.Fatalf("handoff continuation span timeout context = tags %#v data %#v", handoffSpan.tags, handoffSpan.data)
	}
}

func TestRunOneHandoffTimeoutOutcomeStaysSpanLogOnly(t *testing.T) {
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			<-ctx.Done()
			return &harnessapi.TryResult{Completed: false}, ctx.Err()
		},
	}
	r, _, _ := newTimeoutTestRunner(t, exec, Config{
		RetryBudget: 1,
		RunTimeout:  time.Hour,
		LapsEnabled: true,
	})
	sink := &capturingSink{}
	r.SetTelemetry(sink)
	r.timerFunc = fireOnCall(1)

	res := driveRunOneTask(t, r, runTimeoutLapsTask())
	if res.Outcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("run outcome = %q, want handoff_timeout", res.Outcome)
	}
	if len(sink.failures) != 0 {
		t.Fatalf("handoff_timeout must not capture an Issue, got %d", len(sink.failures))
	}
	log := findTryLogByOutcome(t, sink, string(reliability.OutcomeHandoffTimeout))
	if _, found := log["failure_category"]; found {
		t.Fatalf("handoff_timeout log must not carry failure_category: %#v", log)
	}
	if log["timeout_kind"] != "run_budget" || log["timeout_budget_ms"] != time.Hour.Milliseconds() {
		t.Fatalf("handoff_timeout timeout fields = %#v", log)
	}
	if log["session_captured"] != false || log["resume_supported"] != true || log["handoff_only_attempted"] != false {
		t.Fatalf("handoff_timeout context fields missing: %#v", log)
	}
	if got := log["handoff_resume_blocker"]; got != "run timeout; no session captured for handoff" {
		t.Fatalf("handoff_resume_blocker = %#v", got)
	}
	span := findTrySpanByOutcome(t, sink, string(reliability.OutcomeHandoffTimeout))
	if span.tags["timeout_kind"] != "run_budget" || span.data["handoff_resume_blocker"] != "run timeout; no session captured for handoff" {
		t.Fatalf("handoff_timeout span timeout context = tags %#v data %#v", span.tags, span.data)
	}
}

func TestRunOne_AppendTryFailureDoesNotEmitRallyTry(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)
	r := makeRunner(t, s, workspaceDir, sink, successfulFileChangingExecutor(t, workspaceDir), 1)
	blockTryPersistence(t, workspaceDir)

	_, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", ResolvedRoute: "senior"},
		nil, nil, false, false, nil, nil, io.Discard,
	)
	if err == nil {
		t.Fatal("runOne error = nil, want AppendTry failure")
	}
	if got := len(sink.logs); got != 0 {
		t.Fatalf("RallyTry logs = %d, want 0 after failed AppendTry: %#v", got, sink.logs)
	}
	if got := len(s.AllTries()); got != 0 {
		t.Fatalf("persisted tries = %d, want 0", got)
	}
}

func TestRunOne_PersistedTryEmitsExactlyOneRallyTry(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)
	r := makeRunner(t, s, workspaceDir, sink, successfulFileChangingExecutor(t, workspaceDir), 1)

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", ResolvedRoute: "senior"},
		nil, nil, false, false, nil, nil, io.Discard,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	if !res.Success || res.Outcome != reliability.OutcomeCompleted {
		t.Fatalf("runOne result = success %v outcome %q, want completed success", res.Success, res.Outcome)
	}
	tries := s.AllTries()
	if got := len(tries); got != 1 {
		t.Fatalf("persisted tries = %d, want 1", got)
	}
	if got := len(sink.logs); got != 1 {
		t.Fatalf("RallyTry logs = %d, want 1: %#v", got, sink.logs)
	}
	if got := sink.logs[0]["try_id"]; got != tries[0].ID {
		t.Fatalf("RallyTry try_id = %#v, want persisted try id %d", got, tries[0].ID)
	}
}

func TestRunBoundedHandoffOnly_AppendTryFailureDoesNotEmitRallyTry(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)
	r := makeRunner(t, s, workspaceDir, sink, &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{Completed: false, Summary: "handoff not completed"}, nil
		},
	}, 1)
	blockTryPersistence(t, workspaceDir)
	relay := &store.RelayRecord{ID: 1, TargetIterations: 1}

	_, _, _, _, err := r.runBoundedHandoffOnly(
		context.Background(),
		relay,
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", ResolvedRoute: "recovery", LapID: "lap-1", IsLapsBacked: true},
		r.rallyContext(relay),
		"",
		"sess-handoff",
		2,
		1,
		progressSummaryEntryCount(workspaceDir),
		"relay-1-run-1",
		map[string]string{},
		io.Discard,
	)
	if err == nil {
		t.Fatal("runBoundedHandoffOnly error = nil, want AppendTry failure")
	}
	if got := len(sink.logs); got != 0 {
		t.Fatalf("RallyTry logs = %d, want 0 after failed handoff-only AppendTry: %#v", got, sink.logs)
	}
	if got := len(s.AllTries()); got != 0 {
		t.Fatalf("persisted tries = %d, want 0", got)
	}
}

func TestRunBoundedHandoffOnly_PersistedTryEmitsExactlyOneRallyTry(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)
	r := makeRunner(t, s, workspaceDir, sink, &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{Completed: false, Summary: "handoff not completed"}, nil
		},
	}, 1)
	relay := &store.RelayRecord{ID: 1, TargetIterations: 1}

	outcome, _, succeeded, _, err := r.runBoundedHandoffOnly(
		context.Background(),
		relay,
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", ResolvedRoute: "recovery", LapID: "lap-1", IsLapsBacked: true},
		r.rallyContext(relay),
		"",
		"sess-handoff",
		2,
		1,
		progressSummaryEntryCount(workspaceDir),
		"relay-1-run-1",
		map[string]string{},
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runBoundedHandoffOnly error = %v", err)
	}
	if succeeded || outcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("handoff-only result = succeeded %v outcome %q, want handoff_timeout without success", succeeded, outcome)
	}
	tries := s.AllTries()
	if got := len(tries); got != 1 {
		t.Fatalf("persisted tries = %d, want 1", got)
	}
	if got := len(sink.logs); got != 1 {
		t.Fatalf("RallyTry logs = %d, want 1: %#v", got, sink.logs)
	}
	if got := sink.logs[0]["try_id"]; got != tries[0].ID {
		t.Fatalf("RallyTry try_id = %#v, want persisted try id %d", got, tries[0].ID)
	}
	if got := sink.logs[0]["handoff_only"]; got != true {
		t.Fatalf("RallyTry handoff_only = %#v, want true", got)
	}
}

func TestRunOne_LimitSignalDiagnostic_EmittedWithoutIssue(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)

	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{
				Completed: false,
				Summary:   "usage limited",
				Evidence: &reliability.FailureEvidence{
					Category:   reliability.CategoryUsageLimit,
					QuotaScope: "anthropic",
					RawSignal:  "usage limit reached until 5pm",
					Message:    "usage limit reached",
				},
			}, nil
		},
	}

	r := makeRunner(t, s, workspaceDir, sink, exec, 1)

	if _, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", IsLapsBacked: false, LapID: "lap-1"},
		nil, nil, false, false, nil, nil, io.Discard,
	); err != nil {
		t.Fatalf("runOne error = %v", err)
	}

	if got := findFailureCount(sink, "failed:"); got != 0 {
		t.Errorf("usage-limit agent-class failure became Issue(s): %d", got)
	}

	diag := findEvent(t, sink, "provider limit signal")
	if diag.Level != telemetry.LevelInfo {
		t.Errorf("diagnostic level = %q, want %q", diag.Level, telemetry.LevelInfo)
	}
	wantTag(t, diag.Tags, "event_kind", "limit_signal")
	wantTag(t, diag.Tags, "failure_category", "usage_limit")
	wantTag(t, diag.Tags, "quota_scope", "anthropic")
	ev, ok := diag.Contexts["failure_evidence"]
	if !ok {
		t.Fatal("diagnostic missing failure_evidence context")
	}
	if ev["raw_signal"] != "usage limit reached until 5pm" {
		t.Errorf("raw_signal = %v", ev["raw_signal"])
	}
	if ev["message"] != "usage limit reached" {
		t.Errorf("message = %v", ev["message"])
	}
}
