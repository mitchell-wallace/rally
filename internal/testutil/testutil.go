package testutil

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func RepoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func CopyFixtureProject(t *testing.T, destDir string) {
	t.Helper()
	src := filepath.Join(RepoRoot(), "testdata", "fixture-project")
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

func RequireLapsBinary(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("laps")
	if err != nil {
		t.Skip("laps binary not found on PATH")
	}
	return path
}

func RunCommand(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func SetupLapsFixtureProject(t *testing.T) string {
	t.Helper()
	RequireLapsBinary(t)

	workspaceDir := t.TempDir()
	CopyFixtureProject(t, workspaceDir)
	InitGitRepo(t, workspaceDir)
	RunCommand(t, workspaceDir, "laps", "init")
	return workspaceDir
}

func BuildRallyBinary(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "rally")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/rally")
	cmd.Dir = RepoRoot()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build rally failed: %v\n%s", err, out)
	}
	return binPath
}
