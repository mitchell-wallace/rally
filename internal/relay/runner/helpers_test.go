package runner

import (
	"context"
	"fmt"
	"github.com/mitchell-wallace/rally/internal/harness/fixture"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/store"
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
