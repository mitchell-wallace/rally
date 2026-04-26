package relay

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/testutil"
)

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
	src := filepath.Join("..", "..", "testdata", "fixture-project")
	if err := testutil.CopyDir(src, destDir); err != nil {
		t.Fatalf("copy fixture project: %v", err)
	}
}

func InitGitRepo(t *testing.T, dir string) {
	t.Helper()
	testutil.InitGitRepo(t, dir)
}
