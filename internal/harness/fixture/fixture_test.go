package fixture

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/testutil"
)

func mustExec(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func TestFixtureExecutor_RoundTrip(t *testing.T) {
	tmp := t.TempDir()

	// init git repo
	testutil.InitGitRepo(t, tmp)

	// create a file to diff
	origPath := filepath.Join(tmp, "hello.txt")
	if err := os.WriteFile(origPath, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustExec(t, tmp, "git", "add", "hello.txt")
	mustExec(t, tmp, "git", "commit", "-m", "init")

	// create diff
	if err := os.WriteFile(origPath, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(tmp, "change.diff")
	out, err := exec.Command("git", "-C", tmp, "diff", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git diff failed: %v\n%s", err, out)
	}
	if err := os.WriteFile(diffPath, out, 0644); err != nil {
		t.Fatal(err)
	}
	// reset file so diff can apply
	if err := os.WriteFile(origPath, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// create output JSON
	outputPath := filepath.Join(tmp, "output.json")
	outputData := `{"completed":true,"summary":"done","remaining_work":""}`
	if err := os.WriteFile(outputPath, []byte(outputData), 0644); err != nil {
		t.Fatal(err)
	}

	fex := &Executor{
		DiffPath:   diffPath,
		OutputPath: outputPath,
		Delay:      10 * time.Millisecond,
	}

	res, err := fex.Execute(context.Background(), harnessapi.RunOptions{})
	if err != nil {
		t.Fatalf("fixture execute failed: %v", err)
	}
	if !res.Completed {
		t.Error("expected completed")
	}
	if res.Summary != "done" {
		t.Errorf("expected summary 'done', got %q", res.Summary)
	}

	// verify file changed
	b, _ := os.ReadFile(origPath)
	if string(b) != "hello world\n" {
		t.Errorf("expected file to be patched, got %q", string(b))
	}

	// second execution should skip re-application because diff already applied
	res2, err := fex.Execute(context.Background(), harnessapi.RunOptions{})
	if err != nil {
		t.Fatalf("second execute failed: %v", err)
	}
	if !res2.Completed {
		t.Error("expected completed on second run")
	}
}

// TestExecutor_CapabilityDefaults asserts the fixture adapter reports no
// support for resume/rotate/liveness, mirroring the contract the runner relies
// on. Carved out of internal/agent/agent_test.go's TestAdapterCapabilityDefaults
// when fixture moved into its own package.
func TestExecutor_CapabilityDefaults(t *testing.T) {
	f := &Executor{}
	if f.ResumeSupported() {
		t.Error("ResumeSupported() = true, want false")
	}
	if f.RotateSupported() {
		t.Error("RotateSupported() = true, want false")
	}
	if f.LivenessProbeSupported() {
		t.Error("LivenessProbeSupported() = true, want false")
	}
	if err := f.RotateModel("new-model"); err == nil {
		t.Error("RotateModel() = nil, want error")
	}
	ok, err := f.ProbeLiveness(context.Background())
	if ok {
		t.Error("ProbeLiveness() = true, want false")
	}
	if err == nil {
		t.Error("ProbeLiveness() err = nil, want error")
	}
}
