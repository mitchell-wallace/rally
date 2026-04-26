package relay

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
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
	if err := copyDir(src, destDir); err != nil {
		t.Fatalf("copy fixture project: %v", err)
	}
}

func InitGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Rally Test")
	runGit(t, dir, "config", "user.email", "rally@example.com")
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()
		dstFile, err := os.Create(dstPath)
		if err != nil {
			return err
		}
		defer dstFile.Close()
		_, err = io.Copy(dstFile, srcFile)
		return err
	})
}
