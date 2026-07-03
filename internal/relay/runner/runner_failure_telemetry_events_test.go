package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

// TestRunOne_UnfinalizedAgent_CapturesIncompleteFinalization drives a laps-backed
// run whose agent fails without finalizing and asserts the unfinalized capture
// carries failure_category=incomplete_finalization with run/runner/budget and the
// last attempt, and omits the provider-limit-only fields. Because the underlying
// try failure is plain agent-class, it does not itself become an Issue — only the
// unfinalized capture fires.
func TestRunOne_UnfinalizedAgent_CapturesIncompleteFinalization(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{Completed: false, Summary: "did not finalize"}, nil
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

	// The plain agent-class try failure must not itself become an Issue.
	if got := findFailureCount(sink, "failed:"); got != 0 {
		t.Errorf("agent-class try failure became an Issue (%d captures); should stay span/log-only", got)
	}

	evt := findFailure(t, sink, "without finalizing")
	wantTag(t, evt.Tags, "failure_category", "incomplete_finalization")
	wantFingerprintCategory(t, evt, "incomplete_finalization")
	wantTag(t, evt.Tags, "attempt", "1")
	wantTag(t, evt.Tags, "max_attempts", "1")
	wantTag(t, evt.Tags, "agent_state", "active")
	// run/runner correlation rides on the base tags.
	wantTag(t, evt.Tags, "run_id", "1")
	// Provider-limit-only fields must not appear for incomplete_finalization.
	for _, k := range []string{"quota_scope", "reset_at", "reset_after"} {
		wantNoTag(t, evt.Tags, k)
	}
	// The operator-worthy incomplete_finalization capture now carries the
	// Priority-3 dirty_tree evidence so the RallyFailure surfaces a non-empty
	// failure_evidence block (source/message/raw_signal). This run made no file
	// changes, so the raw_signal falls back to the bounded diagnostic marker.
	ev, ok := evt.Contexts["failure_evidence"]
	if !ok {
		t.Fatal("incomplete_finalization capture must carry a failure_evidence context with source=dirty_tree")
	}
	if ev["source"] != "dirty_tree" {
		t.Fatalf("failure_evidence.source = %v, want dirty_tree", ev["source"])
	}
	if ev["message"] != "agent exited without finalizing" {
		t.Fatalf("failure_evidence.message = %v, want agent exited without finalizing", ev["message"])
	}
	if raw, _ := ev["raw_signal"].(string); raw == "" {
		t.Errorf("failure_evidence.raw_signal must be non-empty: %#v", ev)
	}
}

// TestRunOne_UnfinalizedAgentDirtyTree_EmitsDirtyTreeEvidence drives the
// genuine Priority-3 scenario: a laps-backed agent that makes real file changes
// but exits without finalizing (no `laps done`/`laps handoff`). The
// operator-worthy incomplete_finalization capture must reuse the classifier-/
// bounded-changed-path evidence so the emitted RallyFailure carries
// failure_evidence.source=dirty_tree with a non-empty raw_signal (the changed
// paths) and message. (Tasks.md §3.10 / §9.6.)
func TestRunOne_UnfinalizedAgentDirtyTree_EmitsDirtyTreeEvidence(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	const changedPath = "feature.go"
	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			// Real file change on disk — a dirty working tree — but no laps
			// finalization (no laps done/handoff recorded). Written at the repo
			// root so `git status --porcelain` reports the path individually.
			if err := os.WriteFile(filepath.Join(workspaceDir, changedPath), []byte("package main\n"), 0o644); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: false, Summary: "did not finalize"}, nil
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

	// The dirty-tree try failure is incomplete-class; it must not itself surface
	// as a separate "failed:" Issue — only the unfinalized operator capture fires.
	if got := findFailureCount(sink, "failed:"); got != 0 {
		t.Fatalf("incomplete-class try failure became an Issue (%d captures); should stay span/log-only", got)
	}

	evt := findFailure(t, sink, "without finalizing")
	wantTag(t, evt.Tags, "failure_category", "incomplete_finalization")
	wantFingerprintCategory(t, evt, "incomplete_finalization")
	// Provider-limit-only fields must not appear for incomplete_finalization.
	for _, k := range []string{"quota_scope", "reset_at", "reset_after"} {
		wantNoTag(t, evt.Tags, k)
	}

	ev, ok := evt.Contexts["failure_evidence"]
	if !ok {
		t.Fatal("incomplete_finalization capture must carry a failure_evidence context with source=dirty_tree")
	}
	if ev["source"] != "dirty_tree" {
		t.Fatalf("failure_evidence.source = %v, want dirty_tree", ev["source"])
	}
	if ev["message"] != "agent exited without finalizing" {
		t.Fatalf("failure_evidence.message = %v, want agent exited without finalizing", ev["message"])
	}
	raw, _ := ev["raw_signal"].(string)
	if raw == "" {
		t.Fatalf("failure_evidence.raw_signal must be non-empty: %#v", ev)
	}
	if !strings.Contains(raw, changedPath) {
		t.Fatalf("failure_evidence.raw_signal = %q, want it to carry the changed path %q", raw, changedPath)
	}
	// The 256-rune bound (task §3.10) must hold for the surfaced raw_signal.
	if n := len([]rune(raw)); n > 257 {
		t.Fatalf("failure_evidence.raw_signal is %d runes, exceeds the 256-rune bound (+1 ellipsis)", n)
	}
}

func TestRunOne_UnfinalizedCaptureUsesResolvedModelRunnerTag(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "partial.txt"), []byte("partial\n"), 0o644); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{
				Completed:     false,
				Summary:       "did not finalize",
				ResolvedModel: "opencode-go/kimi-k2.6",
			}, nil
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

	evt := findFailure(t, sink, "without finalizing")
	wantTag(t, evt.Tags, "runner", "opencode:opencode-go/kimi-k2.6")
}

func TestRunOne_CancelledLapsAttemptDoesNotCaptureIncompleteFinalization(t *testing.T) {
	var attempts int32
	var workspaceDir string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "partial.txt"), []byte("partial\n"), 0o644); err != nil {
				return nil, err
			}
			atomic.AddInt32(&attempts, 1)
			<-ctx.Done()
			return &harnessapi.TryResult{Completed: false, Summary: "cancelled with partial work"}, ctx.Err()
		},
	}
	r, s, ws := newTimeoutTestRunner(t, exec, Config{
		RetryBudget: 3,
		LapsEnabled: true,
	})
	workspaceDir = ws
	sink := &capturingSink{}
	r.SetTelemetry(sink)

	controls := installOperatorKeyboard(t, r)
	done := driveRunOneTaskAsync(t, r, runTask{
		Name:          "lap task",
		Prompt:        "do work",
		Assignee:      "senior",
		ResolvedRoute: "senior",
		LapID:         "lap-1",
		IsLapsBacked:  true,
		LapsRemaining: 1,
	})
	waitForAttempts(t, &attempts, 1)
	sendOperatorAction(t, controls, runtimeevent.OperatorActionSkip)

	res := awaitRunOne(t, done)
	if res.Outcome != reliability.OutcomeCancelled {
		t.Fatalf("run outcome = %q, want %q", res.Outcome, reliability.OutcomeCancelled)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("executor attempts = %d, want 1: cancelled laps attempt must not retry", got)
	}

	status := runGit(t, workspaceDir, "status", "--porcelain", "partial.txt")
	if !strings.Contains(status, "partial.txt") {
		t.Fatalf("test setup should leave partial.txt dirty so incomplete_finalization would otherwise apply, status=%q", status)
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("persisted tries = %d, want 1", len(tries))
	}
	try := tries[0]
	if try.Outcome != reliability.OutcomeCancelled {
		t.Fatalf("try outcome = %q, want %q", try.Outcome, reliability.OutcomeCancelled)
	}
	if try.CancellationSource != "skip" {
		t.Fatalf("try cancellation source = %q, want skip", try.CancellationSource)
	}
	if try.Category != "" {
		t.Fatalf("try category = %q, want empty for cancelled laps attempt", try.Category)
	}
	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		t.Fatalf("LoadSummaryEntries error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("cancelled laps attempt wrote %d synthetic summary entries, want 0", len(entries))
	}
	rs, err := progress.LoadRunState(workspaceDir)
	if err != nil {
		t.Fatalf("LoadRunState error: %v", err)
	}
	if rs.RunID != "relay-1-run-1" || rs.PinnedLapID != "lap-1" {
		t.Fatalf("run-state after cancellation = run_id %q pinned %q, want relay-1-run-1/lap-1", rs.RunID, rs.PinnedLapID)
	}
	if rs.ActiveRelayID != 0 || rs.ActiveRunID != 0 || rs.ActiveTryID != 0 || rs.ActiveLogPath != "" || rs.ActiveStartedAt != "" {
		t.Fatalf("active try metadata not cleared after cancellation: %+v", rs)
	}
	if got := findFailureCount(sink, "without finalizing"); got != 0 {
		t.Fatalf("cancelled laps attempt emitted %d incomplete_finalization capture(s), want 0", got)
	}
	if len(sink.failures) != 0 {
		t.Fatalf("cancelled laps attempt emitted failure telemetry: %#v", sink.failures)
	}
	if len(sink.events) != 0 {
		t.Fatalf("cancelled laps attempt emitted diagnostic events: %#v", sink.events)
	}
	log := findTryLogByOutcome(t, sink, string(reliability.OutcomeCancelled))
	if _, found := log["failure_category"]; found {
		t.Fatalf("cancelled laps try log must not carry failure_category: %#v", log)
	}
	span := findTrySpanByOutcome(t, sink, string(reliability.OutcomeCancelled))
	wantNoTag(t, span.tags, "failure_category")
}

// TestRunOne_UnfinalizedAgent_MultiAttemptBudget drives a laps-backed run with a
// retry budget of 3 where the agent fails without finalizing on the second
// attempt, and asserts the unfinalized capture carries the correct attempt and
// budget values plus the Priority-3 dirty_tree failure_evidence.
func TestRunOne_UnfinalizedAgent_MultiAttemptBudget(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)

	attempt := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			return &harnessapi.TryResult{
				Completed: false,
				Summary:   fmt.Sprintf("attempt %d did not finalize", attempt),
			}, nil
		},
	}

	r := makeRunner(t, s, workspaceDir, sink, exec, 3)

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

	evt := findFailure(t, sink, "without finalizing")
	wantTag(t, evt.Tags, "failure_category", "incomplete_finalization")
	wantTag(t, evt.Tags, "attempt", "3")
	wantTag(t, evt.Tags, "max_attempts", "3")
	wantTag(t, evt.Tags, "agent_state", "active")
	for _, k := range []string{"quota_scope", "reset_at", "reset_after"} {
		wantNoTag(t, evt.Tags, k)
	}
	// The unfinalized capture reuses the Priority-3 dirty_tree evidence; this run
	// made no file changes so the raw_signal is the bounded diagnostic marker,
	// but the source/message must still surface on the RallyFailure.
	ev, ok := evt.Contexts["failure_evidence"]
	if !ok {
		t.Fatal("incomplete_finalization capture must carry a failure_evidence context with source=dirty_tree")
	}
	if ev["source"] != "dirty_tree" {
		t.Fatalf("failure_evidence.source = %v, want dirty_tree", ev["source"])
	}
	if raw, _ := ev["raw_signal"].(string); raw == "" {
		t.Errorf("failure_evidence.raw_signal must be non-empty: %#v", ev)
	}
}

// TestRunOneBudgetKillWithDirtyTreeEmitsOperatorWorthyCapture verifies scenario
// (f): when a wall-clock budget kills an attempt whose working tree is dirty
// (agent made file changes but did not finalize), the resulting telemetry
// carries the correct budget-kill classification and no regression is
// introduced in operator-worthy captures (the unfinalized capture is
// intentionally suppressed for designed timeout outcomes, and the terminal-try
// issue capture skips non-carrier outcomes).
func TestRunOneBudgetKillWithDirtyTreeEmitsOperatorWorthyCapture(t *testing.T) {
	t.Run("try-cap kill then incomplete succeeds with unfinalized capture", func(t *testing.T) {
		s, workspaceDir := setupRunnerForFailureTestDirty(t)
		sink := s.sink

		const changedPath = "feature.go"
		attempts := 0
		exec := &funcExecutor{
			fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
				attempts++
				if err := os.WriteFile(filepath.Join(workspaceDir, changedPath), []byte("package main\n"), 0o644); err != nil {
					return nil, err
				}
				if attempts == 1 {
					<-ctx.Done()
					return &harnessapi.TryResult{Completed: false}, ctx.Err()
				}
				return &harnessapi.TryResult{Completed: true, Summary: "ok"}, nil
			},
		}
		r := newBudgetKillRunner(t, s.store, workspaceDir, sink, exec, Config{
			RetryBudget: 2,
			TryTimeout:  time.Hour,
			LapsEnabled: true,
		})
		r.timerFunc = fireOnCall(1)

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

		evt := findFailure(t, sink, "without finalizing")
		wantTag(t, evt.Tags, "failure_category", "incomplete_finalization")
		wantFingerprintCategory(t, evt, "incomplete_finalization")
		ev, ok := evt.Contexts["failure_evidence"]
		if !ok {
			t.Fatal("unfinalized capture must carry failure_evidence context")
		}
		if ev["source"] != "dirty_tree" {
			t.Fatalf("failure_evidence.source = %v, want dirty_tree", ev["source"])
		}
		raw, _ := ev["raw_signal"].(string)
		if raw == "" || !strings.Contains(raw, changedPath) {
			t.Fatalf("failure_evidence.raw_signal = %q, want it to carry changed path %q", raw, changedPath)
		}
	})

	t.Run("run-budget kill with dirty tree stays span-log-only", func(t *testing.T) {
		s, workspaceDir := setupRunnerForFailureTestDirty(t)
		sink := s.sink

		const changedPath = "budget.go"
		exec := &funcExecutor{
			fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
				if err := os.WriteFile(filepath.Join(workspaceDir, changedPath), []byte("package main\n"), 0o644); err != nil {
					return nil, err
				}
				<-ctx.Done()
				return &harnessapi.TryResult{Completed: false}, ctx.Err()
			},
		}
		r := newBudgetKillRunner(t, s.store, workspaceDir, sink, exec, Config{
			RetryBudget: 2,
			RunTimeout:  time.Hour,
			LapsEnabled: true,
		})
		r.timerFunc = fireOnCall(1)

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

		// The unfinalized capture is intentionally suppressed for designed
		// timeout outcomes.
		if got := findFailureCount(sink, "without finalizing"); got != 0 {
			t.Fatalf("unfinalized captures = %d, want 0 for designed handoff_timeout outcome", got)
		}
		// The terminal-try capture is skipped (ShouldCaptureIssue false for
		// handoff_timeout). Verify no RallyFailure was captured.
		if got := findFailureCount(sink, "failed:"); got != 0 {
			t.Fatalf("terminal-try captures = %d, want 0 for handoff_timeout outcome", got)
		}

		// The log stays span-only: no failure_category flat field.
		log := findTryLogByOutcome(t, sink, string(reliability.OutcomeHandoffTimeout))
		if _, found := log["failure_category"]; found {
			t.Fatalf("handoff_timeout log must not carry failure_category: %#v", log)
		}
		if log["timeout_kind"] != "run_budget" {
			t.Fatalf("try log timeout_kind = %v, want run_budget", log["timeout_kind"])
		}

		// The span carries timeout telemetry and failure_evidence context.
		span := findTrySpanByOutcome(t, sink, string(reliability.OutcomeHandoffTimeout))
		if span.tags["timeout_kind"] != "run_budget" {
			t.Fatalf("try span timeout_kind = %v, want run_budget", span.tags["timeout_kind"])
		}
		// The failure_evidence data block carries the category (unidentified_issue)
		// and safe error evidence in the raw_signal (within the context, not as
		// flat tags). Verify the evidence context exists.
		evidence, ok := span.data["failure_evidence"].(map[string]interface{})
		if !ok {
			t.Logf("span data = %#v", span.data)
			t.Fatal("run-budget kill span must carry failure_evidence data context")
		}
		if source, _ := evidence["source"].(string); source == "" {
			t.Fatalf("failure_evidence.source must be non-empty, got %#v", evidence)
		}

		// The persisted try record carries the correct budget-kill category.
		tries := s.store.AllTries()
		if len(tries) != 1 {
			t.Fatalf("recorded tries = %d, want 1", len(tries))
		}
		if tries[0].Category != string(reliability.CategoryUnidentifiedIssue) {
			t.Fatalf("try record category = %q, want unidentified_issue (scenario d)", tries[0].Category)
		}
		if res.InfraFailures != 0 {
			t.Errorf("infra failures = %d, want 0 for timeout lifecycle outcome", res.InfraFailures)
		}
	})
}
