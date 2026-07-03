package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harness/fixture"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
	"github.com/mitchell-wallace/rally/internal/testutil"
)

type funcExecutor struct {
	fn              func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error)
	resumeSupported bool
	rotateSupported bool
	probeSupported  bool
	probeFn         func(context.Context) (bool, error)
	rotateErr       error
	rotateCalls     []string
}

func (f *funcExecutor) Execute(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
	return f.fn(ctx, opts)
}

func (f *funcExecutor) ResumeSupported() bool        { return f.resumeSupported }
func (f *funcExecutor) RotateSupported() bool        { return f.rotateSupported }
func (f *funcExecutor) LivenessProbeSupported() bool { return f.probeSupported }
func (f *funcExecutor) RotateModel(model string) error {
	f.rotateCalls = append(f.rotateCalls, model)
	if !f.rotateSupported {
		return fmt.Errorf("rotate not supported by func executor")
	}
	return f.rotateErr
}
func (f *funcExecutor) ProbeLiveness(ctx context.Context) (bool, error) {
	if f.probeFn != nil {
		return f.probeFn(ctx)
	}
	return false, fmt.Errorf("liveness probe not supported by func executor")
}

type fakeStallController struct {
	check func(context.Context) (bool, error)
}

func (f *fakeStallController) SetProcessGroupID(int) {}

func (f *fakeStallController) Check(ctx context.Context) (bool, error) {
	if f.check == nil {
		return false, nil
	}
	return f.check(ctx)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Rally Test")
	runGit(t, dir, "config", "user.email", "rally@example.com")
	// Exclude rally's local machine state from git status.
	excludePath := filepath.Join(dir, ".git", "info", "exclude")
	os.WriteFile(excludePath, []byte(".rally/state/\n"), 0o644)
}

func newTestStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testResolver(spec string) (harnessapi.ResolvedAgent, error) {
	aliases := map[string]string{
		"ag": "antigravity", "agy": "antigravity", "antigravity": "antigravity",
		"cc": "claude", "claude": "claude",
		"cx": "codex", "codex": "codex",
		"op": "opencode", "opencode": "opencode",
	}
	parts := strings.SplitN(spec, ":", 2)
	harness, ok := aliases[parts[0]]
	if !ok {
		return harnessapi.ResolvedAgent{}, fmt.Errorf("unknown agent alias %q", parts[0])
	}
	if len(parts) == 2 {
		if _, err := strconv.Atoi(parts[1]); err == nil {
			return harnessapi.ResolvedAgent{Harness: harness}, nil
		}
		return harnessapi.ResolvedAgent{Harness: harness, Model: parts[1]}, nil
	}
	return harnessapi.ResolvedAgent{Harness: harness}, nil
}

const cheapTestModel = "opencode/big-pickle"

func cheapTestResolver(spec string) (harnessapi.ResolvedAgent, error) {
	if spec == "op:dsf" {
		return harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel}, nil
	}
	return testResolver(spec)
}

func NewFixtureExecutor(t *testing.T, dir, diffPath, outputPath string, delay time.Duration) harnessapi.Executor {
	t.Helper()
	return fixture.New(diffPath, outputPath, delay, dir)
}

func CopyFixtureProject(t *testing.T, destDir string) {
	t.Helper()
	src := filepath.Join("..", "..", "..", "testdata", "fixture-project")
	if err := testutil.CopyDir(src, destDir); err != nil {
		t.Fatalf("copy fixture project: %v", err)
	}
}

func InitGitRepo(t *testing.T, dir string) {
	t.Helper()
	testutil.InitGitRepo(t, dir)
}

type testControls struct {
	ch chan runtimeevent.Press
}

func installOperatorKeyboard(t *testing.T, r *Runner) *testControls {
	t.Helper()
	controls := &testControls{ch: make(chan runtimeevent.Press, 4)}
	r.cfg.Controls = controls
	return controls
}

func (c *testControls) Start(context.Context) (<-chan runtimeevent.Press, error) {
	return c.ch, nil
}

func (c *testControls) Stop() {}

func (c *testControls) WaitResume(context.Context) error {
	return nil
}

func sendOperatorAction(t *testing.T, controls *testControls, action runtimeevent.OperatorAction) {
	t.Helper()
	if action == runtimeevent.OperatorActionNone {
		t.Fatalf("unsupported operator action %v", action)
	}
	controls.ch <- runtimeevent.Press{Action: action, Confirmed: false}
	controls.ch <- runtimeevent.Press{Action: action, Confirmed: true}
}

func awaitRunError(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

func newRouteRuntimeHarness(t *testing.T) *Resilience {
	t.Helper()

	s := newTestStore(t, t.TempDir())
	r := NewResilience(s)
	r.PauseDuration = time.Hour
	r.NowFunc = func() time.Time {
		return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	}
	return r
}

func newResolvedRouteRuntimeOrDie(t *testing.T, routeSpecs map[string][]string, noBackend bool) (*routeRuntime, *Resilience) {
	t.Helper()

	rt, err := newResolvedRouteRuntime(routeSpecs, testResolver, noBackend, nil)
	if err != nil {
		t.Fatalf("newResolvedRouteRuntime() error = %v", err)
	}
	return rt, newRouteRuntimeHarness(t)
}

func newOverrideRouteRuntimeOrDie(t *testing.T, specs []string, routeSpecs map[string][]string, noBackend bool) (*routeRuntime, *Resilience) {
	t.Helper()

	rt, _, err := newOverrideRouteRuntime(specs, routeSpecs, testResolver, noBackend)
	if err != nil {
		t.Fatalf("newOverrideRouteRuntime() error = %v", err)
	}
	return rt, newRouteRuntimeHarness(t)
}

const (
	reasoningBaseModel   = "gpt-5.5"
	reasoningVerifyModel = "gpt-5.5-extra-high"
	reasoningJuniorModel = "gpt-5.5-low"
)

func newReasoningRouteRuntimeOrDie(t *testing.T, routeSpecs map[string][]string) (*routeRuntime, *Resilience) {
	t.Helper()

	resolver := func(spec string) (harnessapi.ResolvedAgent, error) {
		if spec == "cx" || spec == "codex" {
			return harnessapi.ResolvedAgent{Harness: "codex", Model: reasoningBaseModel}, nil
		}
		return testResolver(spec)
	}
	reasoning := map[string]string{
		"verify": "g55-xh",
		"junior": "g55-l",
	}
	reasoningResolver := func(role, selectedHarness, preference string) (string, string, error) {
		if selectedHarness != "codex" {
			return "", "", nil
		}
		switch {
		case strings.EqualFold(role, "verify") && preference == "g55-xh":
			return reasoningVerifyModel, "", nil
		case strings.EqualFold(role, "junior") && preference == "g55-l":
			return reasoningJuniorModel, "", nil
		default:
			return "", "", nil
		}
	}

	rt, err := newResolvedRouteRuntimeWithReasoning(routeSpecs, resolver, reasoning, reasoningResolver, false, nil)
	if err != nil {
		t.Fatalf("newResolvedRouteRuntimeWithReasoning() error = %v", err)
	}
	return rt, newRouteRuntimeHarness(t)
}

func mustNextRouteSelection(t *testing.T, rt *routeRuntime, resilience *Resilience, assignee string, lapID ...string) routeSelection {
	t.Helper()

	task := runTask{Assignee: assignee}
	if len(lapID) > 0 {
		task.LapID = lapID[0]
	}
	selection, err := rt.next(task, resilience)
	if err != nil {
		t.Fatalf("next(%q) error = %v", assignee, err)
	}
	return selection
}

func appendEvent(t *testing.T, s *store.Store, key ResilienceKey, eventType string, relayID int) {
	t.Helper()
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: key.Harness,
		Model:     key.Model,
		EventType: eventType,
		Timestamp: "2026-01-01T12:00:00Z",
		RelayID:   relayID,
	}); err != nil {
		t.Fatalf("AppendAgentStatus(%s): %v", eventType, err)
	}
}

func setupRouteRuntimeStore(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}
	return rallyDir, s
}

func newRouteRuntimeStore(t *testing.T, records ...store.TryRecord) *store.Store {
	t.Helper()
	_, s := setupRouteRuntimeStore(t)
	for _, rec := range records {
		mustAppendRouteTry(t, s, rec)
	}
	return s
}

func mustAppendRouteTry(t *testing.T, s *store.Store, rec store.TryRecord) {
	t.Helper()
	if err := s.AppendTry(rec); err != nil {
		t.Fatalf("AppendTry(%+v): %v", rec, err)
	}
}

// ==========================================
// Failure Telemetry Shared Fixtures/Helpers
// ==========================================

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

func makeRunner(t *testing.T, s *store.Store, workspaceDir string, sink *capturingSink, exec harnessapi.Executor, budget int) *Runner {
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      budget,
		LapsEnabled:      true,
		Resolver:         cheapTestResolver,
	}, map[string]harnessapi.Executor{"opencode": exec})
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

func newBudgetKillRunner(t *testing.T, s *store.Store, workspaceDir string, sink *capturingSink, exec harnessapi.Executor, cfg Config) *Runner {
	t.Helper()
	cfg.WorkspaceDir = workspaceDir
	if cfg.DataDir == "" {
		cfg.DataDir = t.TempDir()
	}
	if cfg.Resolver == nil {
		cfg.Resolver = cheapTestResolver
	}
	r := NewRunner(s, cfg, map[string]harnessapi.Executor{"opencode": exec})
	r.SetTelemetry(sink)
	return r
}

func blockTryPersistence(t *testing.T, workspaceDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(store.RallyDir(workspaceDir), "state", "tries.jsonl"), 0o755); err != nil {
		t.Fatalf("block try persistence: %v", err)
	}
}

func successfulFileChangingExecutor(t *testing.T, workspaceDir string) harnessapi.Executor {
	t.Helper()
	return &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done\n"), 0o644); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: true, Summary: "done"}, nil
		},
	}
}
