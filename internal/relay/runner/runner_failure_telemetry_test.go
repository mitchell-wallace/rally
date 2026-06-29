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

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/keyboard"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

// capturedFailure records one CaptureFailure call so tests can assert on the
// tags and contexts the runner attached at a capture site.
type capturedFailure struct {
	msg string
	evt telemetry.FailureEvent
}

type capturedEvent struct {
	msg string
	evt telemetry.Event
}

type capturedSpan struct {
	operation   string
	description string
	tags        map[string]string
	data        map[string]interface{}
}

// capturingSink records telemetry calls so tests can assert Issue, event, span,
// and structured-log fields together.
type capturingSink struct {
	telemetry.NoopSink
	failures    []capturedFailure
	events      []capturedEvent
	logs        []map[string]interface{}
	routeEvents []map[string]interface{}
	spans       []*capturedSpan
}

type capturingSpan struct {
	span *capturedSpan
}

func (c *capturingSink) StartSpan(ctx context.Context, operation, description string) (context.Context, telemetry.Span) {
	span := &capturedSpan{
		operation:   operation,
		description: description,
		tags:        map[string]string{},
		data:        map[string]interface{}{},
	}
	c.spans = append(c.spans, span)
	return ctx, &capturingSpan{span: span}
}

func (s *capturingSpan) SetTag(key, value string) {
	s.span.tags[key] = value
}

func (s *capturingSpan) SetData(key string, value interface{}) {
	s.span.data[key] = value
}

func (s *capturingSpan) Finish() {}

func (c *capturingSink) EmitTryLog(_ context.Context, fields map[string]interface{}) {
	copied := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		copied[k] = v
	}
	c.logs = append(c.logs, copied)
}

func (c *capturingSink) EmitRouteEvent(_ context.Context, fields map[string]interface{}) {
	copied := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		copied[k] = v
	}
	c.routeEvents = append(c.routeEvents, copied)
}

func (c *capturingSink) CaptureFailure(_ context.Context, msg string, evt telemetry.FailureEvent) {
	c.failures = append(c.failures, capturedFailure{msg: msg, evt: evt})
}

func (c *capturingSink) CaptureEvent(_ context.Context, msg string, evt telemetry.Event) {
	c.events = append(c.events, capturedEvent{msg: msg, evt: evt})
}

// findFailure returns the single captured failure whose message contains substr,
// failing if there is not exactly one. Used to disambiguate the terminal-try,
// unfinalized, and relay-stall captures.
func findFailure(t *testing.T, sink *capturingSink, substr string) telemetry.FailureEvent {
	t.Helper()
	var matches []telemetry.FailureEvent
	for _, f := range sink.failures {
		if strings.Contains(f.msg, substr) {
			matches = append(matches, f.evt)
		}
	}
	if len(matches) != 1 {
		var msgs []string
		for _, f := range sink.failures {
			msgs = append(msgs, f.msg)
		}
		t.Fatalf("want exactly 1 captured failure containing %q, got %d (all: %v)", substr, len(matches), msgs)
	}
	return matches[0]
}

func wantTag(t *testing.T, tags map[string]string, key, want string) {
	t.Helper()
	if got := tags[key]; got != want {
		t.Errorf("tag %q = %q, want %q", key, got, want)
	}
}

func wantNoTag(t *testing.T, tags map[string]string, key string) {
	t.Helper()
	if got, found := tags[key]; found {
		t.Errorf("tag %q must be omitted, got %q", key, got)
	}
}

func wantFingerprintCategory(t *testing.T, evt telemetry.FailureEvent, want string) {
	t.Helper()
	if len(evt.Fingerprint) != 5 {
		t.Fatalf("fingerprint = %v, want 5 stable components", evt.Fingerprint)
	}
	if evt.Fingerprint[0] != "rally" || evt.Fingerprint[1] != "failure" {
		t.Errorf("fingerprint prefix = %v, want [rally failure]", evt.Fingerprint[:2])
	}
	if evt.Fingerprint[3] != want {
		t.Errorf("fingerprint category = %q, want %q (full fingerprint %v)", evt.Fingerprint[3], want, evt.Fingerprint)
	}
}

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
				fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
					rs, _ := progress.LoadRunState(workspaceDir)
					rs.RecordedLaps = tt.recordedLaps
					progress.SaveRunState(workspaceDir, rs)
					return &agent.TryResult{Completed: true, Summary: "completed wrong lap"}, nil
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
			}, map[string]agent.Executor{"opencode": exec})
			r.SetTelemetry(sink)

			res, err := r.runOne(
				context.Background(),
				&store.RelayRecord{ID: 1, TargetIterations: 1},
				0,
				agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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

func TestRunOne_OrdinaryAgentErrorEvidenceStaysTryTelemetryOnly(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{
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
	}, map[string]agent.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			// usage_limit is FailureAgent (not issue-worthy alone); the harness
			// error makes this capture issue-worthy. The evidence supplies the
			// category + quota/reset/raw-signal the capture should carry.
			return &agent.TryResult{
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
	}, map[string]agent.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	if _, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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

func TestRunOne_ResolvedModelBareAliasPropagatesToFailureDiagnosticAndTryTags(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	reset := time.Now().Add(3 * time.Hour).UTC()
	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{
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
	}, map[string]agent.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	if _, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: ""},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "did not finalize"}, nil
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
	}, map[string]agent.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	if _, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			// Real file change on disk — a dirty working tree — but no laps
			// finalization (no laps done/handoff recorded). Written at the repo
			// root so `git status --porcelain` reports the path individually.
			if err := os.WriteFile(filepath.Join(workspaceDir, changedPath), []byte("package main\n"), 0o644); err != nil {
				return nil, err
			}
			return &agent.TryResult{Completed: false, Summary: "did not finalize"}, nil
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
	}, map[string]agent.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	if _, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "partial.txt"), []byte("partial\n"), 0o644); err != nil {
				return nil, err
			}
			return &agent.TryResult{
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
	}, map[string]agent.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	if _, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: ""},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "partial.txt"), []byte("partial\n"), 0o644); err != nil {
				return nil, err
			}
			atomic.AddInt32(&attempts, 1)
			<-ctx.Done()
			return &agent.TryResult{Completed: false, Summary: "cancelled with partial work"}, ctx.Err()
		},
	}
	r, s, ws := newTimeoutTestRunner(t, exec, Config{
		RetryBudget: 3,
		LapsEnabled: true,
	})
	workspaceDir = ws
	sink := &capturingSink{}
	r.SetTelemetry(sink)

	input := installOperatorKeyboard(t)
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
	sendOperatorAction(t, input, keyboard.ActionSkip)

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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempts++
			if attempts < 2 {
				return &agent.TryResult{Completed: false, Summary: "transient agent hiccup"}, nil
			}
			f, _ := os.Create(fmt.Sprintf("%s/done-%d.txt", workspaceDir, attempts))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "done"}, nil
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
	}, map[string]agent.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: true, Summary: "unused"}, nil
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
	}, map[string]agent.Executor{"claude": exec})
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

func TestRunOne_ExecErrorWithoutEvidenceUsesClassifierEvidence(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "launcher failed"}, fmt.Errorf("launcher failed\nstderr: full command transcript")
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
	}, map[string]agent.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("starting agent\n"+matchedLine+"\nagent exited\n"), 0o644)
			}
			return &agent.TryResult{Completed: false, Summary: "upstream error"}, fmt.Errorf("agent failed")
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
	}, map[string]agent.Executor{"opencode": exec})
	r.SetTelemetry(sink)

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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

func findFailureCount(sink *capturingSink, substr string) int {
	n := 0
	for _, f := range sink.failures {
		if strings.Contains(f.msg, substr) {
			n++
		}
	}
	return n
}

func findEvent(t *testing.T, sink *capturingSink, substr string) telemetry.Event {
	t.Helper()
	var matches []telemetry.Event
	for _, e := range sink.events {
		if strings.Contains(e.msg, substr) {
			matches = append(matches, e.evt)
		}
	}
	if len(matches) != 1 {
		var msgs []string
		for _, e := range sink.events {
			msgs = append(msgs, e.msg)
		}
		t.Fatalf("want exactly 1 captured event containing %q, got %d (all: %v)", substr, len(matches), msgs)
	}
	return matches[0]
}

func wantNoContext(t *testing.T, evt telemetry.FailureEvent, name string) {
	t.Helper()
	if _, ok := evt.Contexts[name]; ok {
		t.Errorf("context %q must not be present", name)
	}
}

func wantContextKey(t *testing.T, evt telemetry.FailureEvent, block, key string, want string) {
	t.Helper()
	blk, ok := evt.Contexts[block]
	if !ok {
		t.Fatalf("context block %q missing", block)
	}
	got, _ := blk[key].(string)
	if got != want {
		t.Errorf("context[%q][%q] = %q, want %q", block, key, got, want)
	}
}

func wantContextNotContains(t *testing.T, evt telemetry.FailureEvent, block, key, substr string) {
	t.Helper()
	blk, ok := evt.Contexts[block]
	if !ok {
		return
	}
	got, _ := blk[key].(string)
	if strings.Contains(got, substr) {
		t.Errorf("context[%q][%q] = %q must not contain %q", block, key, got, substr)
	}
}

func findTryLogByOutcome(t *testing.T, sink *capturingSink, outcome string) map[string]interface{} {
	t.Helper()
	for _, fields := range sink.logs {
		if fields["event"] == "try" && fields["outcome"] == outcome {
			return fields
		}
	}
	t.Fatalf("no try log with outcome %q found in %#v", outcome, sink.logs)
	return nil
}

func findLogByEvent(t *testing.T, sink *capturingSink, event string) map[string]interface{} {
	t.Helper()
	for _, fields := range sink.logs {
		if fields["event"] == event {
			return fields
		}
	}
	t.Fatalf("no log with event %q found in %#v", event, sink.logs)
	return nil
}

func findRouteEventByEvent(t *testing.T, sink *capturingSink, event string) map[string]interface{} {
	t.Helper()
	for _, fields := range sink.routeEvents {
		if fields["event"] == event {
			return fields
		}
	}
	t.Fatalf("no route event %q found in %#v", event, sink.routeEvents)
	return nil
}

func assertNoTryLogEvent(t *testing.T, sink *capturingSink, event string) {
	t.Helper()
	for _, fields := range sink.logs {
		if fields["event"] == event {
			t.Fatalf("unexpected try log event %q in %#v", event, sink.logs)
		}
	}
}

func assertTryLogsHaveOutcome(t *testing.T, sink *capturingSink) {
	t.Helper()
	for _, fields := range sink.logs {
		if fields["event"] != "try" {
			continue
		}
		outcome, _ := fields["outcome"].(string)
		if strings.TrimSpace(outcome) == "" {
			t.Fatalf("try log missing non-empty outcome: %#v", fields)
		}
	}
}

func findTrySpanByOutcome(t *testing.T, sink *capturingSink, outcome string) *capturedSpan {
	t.Helper()
	for _, span := range sink.spans {
		if span.operation == "try" && span.tags["outcome"] == outcome {
			return span
		}
	}
	t.Fatalf("no try span with outcome %q found in %#v", outcome, sink.spans)
	return nil
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			executedModels = append(executedModels, opts.Model)
			if opts.Model == "model-a" {
				return &agent.TryResult{
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
			return &agent.TryResult{Completed: true, Summary: "done"}, nil
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
	}, map[string]agent.Executor{"opencode": exec})
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

func TestRunOneTimeoutHandoffOutcomesStaySpanLogOnly(t *testing.T) {
	var workspaceDir string
	attempt := 0
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			if attempt == 1 {
				<-ctx.Done()
				return &agent.TryResult{Completed: false, SessionID: "sess-timeout"}, ctx.Err()
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
			return &agent.TryResult{Completed: true, Summary: "handoff recorded"}, nil
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			<-ctx.Done()
			return &agent.TryResult{Completed: false}, ctx.Err()
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
				fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
					if err := os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done\n"), 0o644); err != nil {
						return nil, err
					}
					if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
						return nil, err
					}
					if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
						RunID:          "relay-1-run-1",
						Summary:        "done",
						Classification: tt.class,
					}); err != nil {
						return nil, err
					}
					return &agent.TryResult{Completed: true, Summary: "done"}, nil
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
			}, map[string]agent.Executor{"opencode": exec})
			r.SetTelemetry(sink)

			res, err := r.runOne(
				context.Background(),
				&store.RelayRecord{ID: 1, TargetIterations: 1},
				0,
				agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
	if err := s.AppendTry(store.TryRecord{ID: 1, RunID: 1, RelayID: 1, LapID: "lap-cap", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "recovery"}); err != nil {
		t.Fatalf("append try 1: %v", err)
	}
	if err := s.AppendTry(store.TryRecord{ID: 2, RunID: 2, RelayID: 1, LapID: "lap-cap", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "recovery"}); err != nil {
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done\n"), 0o644); err != nil {
				return nil, err
			}
			if err := progress.RecordLap(workspaceDir, "lap-cap"); err != nil {
				return nil, err
			}
			if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
				RunID:   "relay-1-run-1",
				Summary: "done",
			}); err != nil {
				return nil, err
			}
			return &agent.TryResult{Completed: true, Summary: "done"}, nil
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
	}, map[string]agent.Executor{"opencode": exec})
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

func setupRunnerForFailureTest(t *testing.T) (*store.Store, string, *capturingSink) {
	t.Helper()
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")
	s := newTestStore(t, rallyDir)
	sink := &capturingSink{}
	return s, workspaceDir, sink
}

func makeRunner(t *testing.T, s *store.Store, workspaceDir string, sink *capturingSink, exec agent.Executor, budget int) *Runner {
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      budget,
		LapsEnabled:      true,
		Resolver:         cheapTestResolver,
	}, map[string]agent.Executor{"opencode": exec})
	r.SetTelemetry(sink)
	return r
}

type failureTestContext struct {
	store *store.Store
	sink  *capturingSink
}

func setupRunnerForFailureTestDirty(t *testing.T) (failureTestContext, string) {
	t.Helper()
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")
	s := newTestStore(t, rallyDir)
	sink := &capturingSink{}
	return failureTestContext{store: s, sink: sink}, workspaceDir
}

func newBudgetKillRunner(t *testing.T, s *store.Store, workspaceDir string, sink *capturingSink, exec agent.Executor, cfg Config) *Runner {
	t.Helper()
	cfg.WorkspaceDir = workspaceDir
	if cfg.DataDir == "" {
		cfg.DataDir = t.TempDir()
	}
	if cfg.Resolver == nil {
		cfg.Resolver = cheapTestResolver
	}
	r := NewRunner(s, cfg, map[string]agent.Executor{"opencode": exec})
	r.SetTelemetry(sink)
	r.out = io.Discard
	return r
}

func blockTryPersistence(t *testing.T, workspaceDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(store.RallyDir(workspaceDir), "state", "tries.jsonl"), 0o755); err != nil {
		t.Fatalf("block try persistence: %v", err)
	}
}

func successfulFileChangingExecutor(t *testing.T, workspaceDir string) agent.Executor {
	t.Helper()
	return &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done\n"), 0o644); err != nil {
				return nil, err
			}
			return &agent.TryResult{Completed: true, Summary: "done"}, nil
		},
	}
}

func TestRunOne_AppendTryFailureDoesNotEmitRallyTry(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)
	r := makeRunner(t, s, workspaceDir, sink, successfulFileChangingExecutor(t, workspaceDir), 1)
	r.out = io.Discard
	blockTryPersistence(t, workspaceDir)

	_, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
	r.out = io.Discard

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "handoff not completed"}, nil
		},
	}, 1)
	r.out = io.Discard
	blockTryPersistence(t, workspaceDir)
	relay := &store.RelayRecord{ID: 1, TargetIterations: 1}

	_, _, _, _, err := r.runBoundedHandoffOnly(
		context.Background(),
		relay,
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "handoff not completed"}, nil
		},
	}, 1)
	r.out = io.Discard
	relay := &store.RelayRecord{ID: 1, TargetIterations: 1}

	outcome, _, succeeded, _, err := r.runBoundedHandoffOnly(
		context.Background(),
		relay,
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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

// TestRunOne_TerminalTryFailure_ShortRateLimit verifies a short_rate_limit
// terminal try failure captures the category, quota fields, and the bounded
// failure_evidence context with raw_signal and message.
func TestRunOne_TerminalTryFailure_ShortRateLimit(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)

	reset := time.Now().Add(2 * time.Minute).UTC()
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{
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
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
}

// TestRunOne_TerminalTryFailure_ProviderOverloaded verifies a
// provider_overloaded terminal try failure carries the evidence context.
func TestRunOne_TerminalTryFailure_ProviderOverloaded(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)

	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{
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
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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

func TestRunOne_LimitSignalDiagnostic_EmittedWithoutIssue(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)

	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{
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
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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

// TestRunOne_TerminalTryFailure_NonLimitCategory_BoundedEvidenceContext verifies
// that an issue-worthy terminal try failure classified as a non-limit category
// can carry bounded explicit failure_evidence without quota/reset fields.
func TestRunOne_TerminalTryFailure_NonLimitCategory_BoundedEvidenceContext(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)

	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{
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
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{
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
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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

// TestRunOne_UnfinalizedAgent_MultiAttemptBudget drives a laps-backed run with a
// retry budget of 3 where the agent fails without finalizing on the second
// attempt, and asserts the unfinalized capture carries the correct attempt and
// budget values plus the Priority-3 dirty_tree failure_evidence.
func TestRunOne_UnfinalizedAgent_MultiAttemptBudget(t *testing.T) {
	s, workspaceDir, sink := setupRunnerForFailureTest(t)

	attempt := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			return &agent.TryResult{
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
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: true, Summary: "unused"}, nil
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
	}, map[string]agent.Executor{"claude": exec})
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
			fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
				attempts++
				if err := os.WriteFile(filepath.Join(workspaceDir, changedPath), []byte("package main\n"), 0o644); err != nil {
					return nil, err
				}
				if attempts == 1 {
					<-ctx.Done()
					return &agent.TryResult{Completed: false}, ctx.Err()
				}
				return &agent.TryResult{Completed: true, Summary: "ok"}, nil
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
			agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
			fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
				if err := os.WriteFile(filepath.Join(workspaceDir, changedPath), []byte("package main\n"), 0o644); err != nil {
					return nil, err
				}
				<-ctx.Done()
				return &agent.TryResult{Completed: false}, ctx.Err()
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
			agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
