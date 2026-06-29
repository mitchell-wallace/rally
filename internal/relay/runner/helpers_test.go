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

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/testutil"
)

type funcExecutor struct {
	fn              func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error)
	resumeSupported bool
	rotateSupported bool
	probeSupported  bool
	probeFn         func(context.Context) (bool, error)
	rotateErr       error
	rotateCalls     []string
}

func (f *funcExecutor) Execute(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
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

func testResolver(spec string) (agent.ResolvedAgent, error) {
	aliases := map[string]string{
		"ag": "antigravity", "agy": "antigravity", "antigravity": "antigravity",
		"cc": "claude", "claude": "claude",
		"cx": "codex", "codex": "codex",
		"op": "opencode", "opencode": "opencode",
	}
	parts := strings.SplitN(spec, ":", 2)
	harness, ok := aliases[parts[0]]
	if !ok {
		return agent.ResolvedAgent{}, fmt.Errorf("unknown agent alias %q", parts[0])
	}
	if len(parts) == 2 {
		if _, err := strconv.Atoi(parts[1]); err == nil {
			return agent.ResolvedAgent{Harness: harness}, nil
		}
		return agent.ResolvedAgent{Harness: harness, Model: parts[1]}, nil
	}
	return agent.ResolvedAgent{Harness: harness}, nil
}

const cheapTestModel = "opencode/big-pickle"

func cheapTestResolver(spec string) (agent.ResolvedAgent, error) {
	if spec == "op:dsf" {
		return agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel}, nil
	}
	return testResolver(spec)
}

func NewFixtureExecutor(t *testing.T, dir, diffPath, outputPath string, delay time.Duration) agent.Executor {
	t.Helper()
	return &agent.FixtureExecutor{
		DiffPath:   diffPath,
		OutputPath: outputPath,
		Delay:      delay,
		Dir:        dir,
	}
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
