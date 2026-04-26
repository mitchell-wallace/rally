package testutil

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func CopyFixtureProject(t *testing.T, destDir string) {
	t.Helper()
	src := filepath.Join("..", "..", "testdata", "fixture-project")
	if err := CopyDir(src, destDir); err != nil {
		t.Fatalf("copy fixture project: %v", err)
	}
}

func CopyDir(src, dst string) error {
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

func RunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func InitGitRepo(t *testing.T, dir string) {
	t.Helper()
	RunGit(t, dir, "init")
	RunGit(t, dir, "config", "user.name", "Rally Test")
	RunGit(t, dir, "config", "user.email", "rally@example.com")
}
