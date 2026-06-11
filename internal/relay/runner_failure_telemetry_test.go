package relay

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
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

// capturingSink is a telemetry sink that records CaptureFailure calls. Span and
// log methods inherit the no-op behavior so only the failure-capture path is
// observed.
type capturingSink struct {
	telemetry.NoopSink
	failures []capturedFailure
}

func (c *capturingSink) CaptureFailure(_ context.Context, msg string, evt telemetry.FailureEvent) {
	c.failures = append(c.failures, capturedFailure{msg: msg, evt: evt})
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
	wantTag(t, evt.Tags, "attempt", "1")
	wantTag(t, evt.Tags, "max_attempts", "1")
	wantTag(t, evt.Tags, "agent_state", "active")
	// run/runner correlation rides on the base tags.
	wantTag(t, evt.Tags, "run_id", "1")
	// Provider-limit-only fields must not appear for incomplete_finalization.
	for _, k := range []string{"quota_scope", "reset_at", "reset_after"} {
		wantNoTag(t, evt.Tags, k)
	}
	if _, ok := evt.Contexts["failure_evidence"]; ok {
		t.Error("incomplete_finalization capture must not carry a failure_evidence context")
	}
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
	if err := resilience.FreezeAgent(ResilienceKey{Harness: "claude"}, 1, "test freeze"); err != nil {
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
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      1,
		Resolver:         testResolver,
	}, map[string]agent.Executor{"claude": exec})
	r.SetTelemetry(sink)

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

func findFailureCount(sink *capturingSink, substr string) int {
	n := 0
	for _, f := range sink.failures {
		if strings.Contains(f.msg, substr) {
			n++
		}
	}
	return n
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

// TestRunOne_TerminalTryFailure_NonLimitCategory_NoEvidenceContext verifies
// that a terminal try failure classified as a non-limit category (e.g.
// invalid_model) does NOT attach the failure_evidence context, even when the
// TryResult carries a FailureEvidence with RawSignal and Message.
func TestRunOne_TerminalTryFailure_NonLimitCategory_NoEvidenceContext(t *testing.T) {
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
	wantNoContext(t, evt, "failure_evidence")
}

// TestRunOne_TerminalTryFailure_ScrubsHomePathInRawSignal drives a usage-limit
// failure whose raw_signal and message contain real home-directory paths and
// prompt/transcript-looking content, and asserts the scrubber collapses paths
// and the evidence context contains only raw_signal and message keys.
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

	allowed := map[string]struct{}{"raw_signal": {}, "message": {}}
	for k := range ev {
		if _, ok := allowed[k]; !ok {
			t.Errorf("unexpected key %q in failure_evidence context", k)
		}
	}
}

// TestRunOne_UnfinalizedAgent_MultiAttemptBudget drives a laps-backed run with a
// retry budget of 3 where the agent fails without finalizing on the second
// attempt, and asserts the unfinalized capture carries the correct attempt and
// budget values and no failure_evidence context.
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
	wantNoContext(t, evt, "failure_evidence")
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
	if err := resilience.FreezeAgent(ResilienceKey{Harness: "claude"}, 1, "test freeze"); err != nil {
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
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      1,
		Resolver:         testResolver,
	}, map[string]agent.Executor{"claude": exec})
	r.SetTelemetry(sink)

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
