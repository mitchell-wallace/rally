package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

// TestRunOne_AgentClassFailureStaysSpanLogOnly drives a run that fails once with
// a plain agent error then recovers, and asserts no failure is captured as an
// Issue — recoverable agent-class failures remain spans/logs only.
func TestRunOne_AgentClassFailureStaysSpanLogOnly(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	attempts := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempts++
			if attempts < 2 {
				return &harnessapi.TryResult{Completed: false, Summary: "transient agent hiccup"}, nil
			}
			f, _ := os.Create(fmt.Sprintf("%s/done-%d.txt", workspaceDir, attempts))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "done"}, nil
		},
	}

	sink := &capturingSink{}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      2,
		Resolver:         cheapTestResolver,
	}, map[string]harnessapi.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior"},
		nil, nil, false, false, nil, nil, io.Discard,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	if !res.Success {
		t.Fatalf("expected run to recover and succeed, got %+v", res)
	}
	if len(sink.failures) != 0 {
		var msgs []string
		for _, f := range sink.failures {
			msgs = append(msgs, f.msg)
		}
		t.Errorf("recoverable agent-class failure became Issue(s): %v", msgs)
	}
}

// TestRun_AllFrozen_CapturesFrozenState drives a relay where the only runner is
// frozen and asserts the relay-stall capture carries agent_state=frozen with the
// relay/global context, while omitting every try-only field (attempt, try_id,
// category, reset evidence) and the failure_evidence context.
func TestRun_AllFrozen_CapturesFrozenState(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	resilience := NewResilience(s)
	if err := resilience.FreezeAgent(ResilienceKey{Harness: "claude", Model: "test"}, 1, "test freeze"); err != nil {
		t.Fatalf("FreezeAgent: %v", err)
	}

	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{Completed: true, Summary: "unused"}, nil
		},
	}
	sink := &capturingSink{}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:test"},
		TargetIterations: 1,
		RetryBudget:      1,
		Resolver:         testResolver,
	}, map[string]harnessapi.Executor{"claude": exec})
	r.SetTelemetry(sink)
	r.resilience = resilience

	err := r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "all agents frozen") {
		t.Fatalf("Run error = %v, want 'all agents frozen'", err)
	}

	evt := findFailure(t, sink, "all agents frozen")
	wantTag(t, evt.Tags, "agent_state", "frozen")
	wantTag(t, evt.Tags, "relay_id", "1")
	// Try-only fields must never appear on a relay-level stall.
	for _, k := range []string{"attempt", "max_attempts", "failure_category", "try_id", "run_id", "quota_scope", "reset_at", "reset_after"} {
		wantNoTag(t, evt.Tags, k)
	}
	if _, ok := evt.Contexts["failure_evidence"]; ok {
		t.Error("relay-stall capture must not carry a failure_evidence context")
	}
}

func TestRun_RouteFallbackTelemetryIncludesTriggerCause(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	var executedModels []string
	exec := &funcExecutor{
		rotateSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			executedModels = append(executedModels, opts.Model)
			if opts.Model == "model-a" {
				return &harnessapi.TryResult{
					Completed: false,
					Summary:   "bad model",
					Evidence: &reliability.FailureEvidence{
						Category:  reliability.CategoryInvalidModel,
						RawSignal: "model-a is unavailable",
						Message:   "invalid model",
					},
				}, nil
			}
			if err := os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done\n"), 0o644); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: true, Summary: "done"}, nil
		},
	}

	sink := &capturingSink{}
	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RouteSpecs: map[string][]string{
			"default": {"op:model-a:1", "op:model-b:1"},
		},
		TargetIterations: 1,
		RetryBudget:      1,
		Resolver:         testResolver,
		TaskPrompt:       "route fallback",
	}, map[string]harnessapi.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := executedModels, []string{"model-a", "model-b"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("executed models = %v, want %v", got, want)
	}

	routeEvent := findRouteEventByEvent(t, sink, "route_fallback")
	assertNoTryLogEvent(t, sink, "route_fallback")
	assertTryLogsHaveOutcome(t, sink)
	if routeEvent["from_runner"] != "opencode:model-a" || routeEvent["to_runner"] != "opencode:model-b" {
		t.Fatalf("route_fallback runners = from %#v to %#v", routeEvent["from_runner"], routeEvent["to_runner"])
	}
	if routeEvent["trigger_run_id"] != 1 || routeEvent["trigger_try_id"] != 1 {
		t.Fatalf("route_fallback trigger ids = run %#v try %#v", routeEvent["trigger_run_id"], routeEvent["trigger_try_id"])
	}
	if routeEvent["trigger_outcome"] != string(reliability.OutcomeFailed) {
		t.Fatalf("trigger_outcome = %#v", routeEvent["trigger_outcome"])
	}
	if routeEvent["trigger_failure_class"] != string(reliability.FailureAgent) {
		t.Fatalf("trigger_failure_class = %#v", routeEvent["trigger_failure_class"])
	}
	if routeEvent["trigger_failure_category"] != string(reliability.CategoryInvalidModel) {
		t.Fatalf("trigger_failure_category = %#v", routeEvent["trigger_failure_category"])
	}
	if routeEvent["route_name"] != "default" || routeEvent["route_entry_exhausted_reason"] != "category:invalid_model" {
		t.Fatalf("route cause = route %#v exhausted %#v", routeEvent["route_name"], routeEvent["route_entry_exhausted_reason"])
	}
	if _, hasOutcome := routeEvent["outcome"]; hasOutcome {
		t.Fatalf("route_fallback must not carry try-only outcome: %#v", routeEvent)
	}
	if _, hasTryID := routeEvent["try_id"]; hasTryID {
		t.Fatalf("route_fallback must not carry try-only try_id: %#v", routeEvent)
	}

	var fallbackSpan *capturedSpan
	for _, span := range sink.spans {
		if span.operation == "run" && span.tags["route_fallback"] == "true" {
			fallbackSpan = span
			break
		}
	}
	if fallbackSpan == nil {
		t.Fatalf("run span with route_fallback tag not found in %#v", sink.spans)
	}
	if fallbackSpan.data["trigger_try_id"] != 1 || fallbackSpan.tags["trigger_failure_category"] != string(reliability.CategoryInvalidModel) {
		t.Fatalf("fallback span trigger fields = tags %#v data %#v", fallbackSpan.tags, fallbackSpan.data)
	}
}

func TestRunOneRecoveryClassificationTelemetryAndNeedsUserIssue(t *testing.T) {
	tests := []struct {
		name         string
		class        string
		wantFailures int
	}{
		{name: "ordinary recovery classification is span log only", class: "repair_plan"},
		{name: "needs_user captures operator issue", class: "needs_user", wantFailures: 1},
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
					if err := os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done\n"), 0o644); err != nil {
						return nil, err
					}
					if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
						return nil, err
					}
					if err := progress.AppendOutingEntry(workspaceDir, progress.OutingEntry{
						OutingID:       "relay-1-run-1",
						Summary:        "done",
						Classification: tt.class,
					}); err != nil {
						return nil, err
					}
					return &harnessapi.TryResult{Completed: true, Summary: "done"}, nil
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
				runTask{Name: "task", Prompt: "do work", Assignee: "senior", EffectiveAssignee: "recovery", ResolvedRoute: "recovery", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
				nil, nil, false, false, nil, nil, io.Discard,
			)
			if err != nil {
				t.Fatalf("runOne error = %v", err)
			}
			if !res.Success || res.Outcome != reliability.OutcomeCompleted {
				t.Fatalf("run outcome = success %v outcome %q, want completed success", res.Success, res.Outcome)
			}

			log := findTryLogByOutcome(t, sink, string(reliability.OutcomeCompleted))
			if log["recovery_classification"] != tt.class {
				t.Fatalf("log recovery_classification = %#v, want %q", log["recovery_classification"], tt.class)
			}
			span := findTrySpanByOutcome(t, sink, string(reliability.OutcomeCompleted))
			if span.tags["recovery_classification"] != tt.class {
				t.Fatalf("span recovery_classification = %q, want %q", span.tags["recovery_classification"], tt.class)
			}
			if len(sink.failures) != tt.wantFailures {
				t.Fatalf("captured failures = %d, want %d", len(sink.failures), tt.wantFailures)
			}
			if tt.wantFailures == 1 {
				evt := sink.failures[0].evt
				wantTag(t, evt.Tags, "recovery_classification", "needs_user")
				wantTag(t, evt.Tags, "outcome", "completed")
				wantNoTag(t, evt.Tags, "failure_category")
				wantFingerprintCategory(t, evt, "needs_user")
			}
		})
	}
}

func TestRunRecoveryCapHitCapturesNeedsUserIssue(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	if err := s.AppendTry(store.TryRecord{ID: 1, OutingID: 1, RelayID: 1, LapID: "lap-cap", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "recovery"}); err != nil {
		t.Fatalf("append try 1: %v", err)
	}
	if err := s.AppendTry(store.TryRecord{ID: 2, OutingID: 2, RelayID: 1, LapID: "lap-cap", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "recovery"}); err != nil {
		t.Fatalf("append try 2: %v", err)
	}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{ID: "lap-cap", Title: "cap task", Description: "finish", Assignee: "senior"}, nil
	}
	oldQueueSize := queueSize
	queueSize = func(context.Context, string) (int, error) { return 1, nil }
	defer func() {
		headPullLap = oldHeadPull
		queueSize = oldQueueSize
	}()

	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done\n"), 0o644); err != nil {
				return nil, err
			}
			if err := progress.RecordLap(workspaceDir, "lap-cap"); err != nil {
				return nil, err
			}
			if err := progress.AppendOutingEntry(workspaceDir, progress.OutingEntry{
				OutingID: "relay-1-run-1",
				Summary:  "done",
			}); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: true, Summary: "done"}, nil
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

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
	}

	evt := findFailure(t, sink, "recovery cap reached")
	wantTag(t, evt.Tags, "recovery_classification", "needs_user")
	wantTag(t, evt.Tags, "lap_id", "lap-cap")
	wantNoTag(t, evt.Tags, "failure_category")
	wantNoTag(t, evt.Tags, "outcome")
	wantFingerprintCategory(t, evt, "needs_user")

	routeEvent := findRouteEventByEvent(t, sink, "route_fallback")
	if routeEvent["lap_id"] != "lap-cap" || routeEvent["recovery_classification"] != "needs_user" {
		t.Fatalf("recovery cap route event = %#v", routeEvent)
	}
	if routeEvent["from_runner"] != "opencode:opencode/big-pickle" || routeEvent["to_runner"] != "opencode:opencode/big-pickle" {
		t.Fatalf("recovery cap route runners = from %#v to %#v", routeEvent["from_runner"], routeEvent["to_runner"])
	}
	if routeEvent["route_entry_exhausted_reason"] != "recovery_cap_hit" {
		t.Fatalf("route_entry_exhausted_reason = %#v, want recovery_cap_hit", routeEvent["route_entry_exhausted_reason"])
	}
	assertTryLogsHaveOutcome(t, sink)
}

// TestRun_AllFrozen_CarriesRallyContext verifies the all-frozen relay stall
// capture carries the rally context block with relay-level identity and has
// no try-level or provider-limit fields.
func TestRun_AllFrozen_CarriesRallyContext(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	resilience := NewResilience(s)
	if err := resilience.FreezeAgent(ResilienceKey{Harness: "claude", Model: "test"}, 1, "test freeze"); err != nil {
		t.Fatalf("FreezeAgent: %v", err)
	}

	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{Completed: true, Summary: "unused"}, nil
		},
	}
	sink := &capturingSink{}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:test"},
		TargetIterations: 1,
		RetryBudget:      1,
		Resolver:         testResolver,
	}, map[string]harnessapi.Executor{"claude": exec})
	r.SetTelemetry(sink)
	r.resilience = resilience

	err := r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "all agents frozen") {
		t.Fatalf("Run error = %v, want 'all agents frozen'", err)
	}

	evt := findFailure(t, sink, "all agents frozen")
	wantTag(t, evt.Tags, "agent_state", "frozen")
	wantTag(t, evt.Tags, "relay_id", "1")

	rallyCtx, ok := evt.Contexts["rally"]
	if !ok {
		t.Fatal("rally context block missing on frozen-stall capture")
	}
	if _, ok := rallyCtx["version"]; !ok {
		t.Error("rally context missing version field")
	}

	for _, k := range []string{"attempt", "max_attempts", "failure_category", "try_id", "run_id", "quota_scope", "reset_at", "reset_after"} {
		wantNoTag(t, evt.Tags, k)
	}
	wantNoContext(t, evt, "failure_evidence")
}
