package laps

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runHandoffHook execs the handoff hook against a PATH-stub rally that appends
// each invocation to a call log, and returns rc, output and the recorded calls.
func runHandoffHook(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	tmp := t.TempDir()
	scriptAbs, err := filepath.Abs("laps-handoff-hook.sh")
	if err != nil {
		t.Fatal(err)
	}
	mockDir := filepath.Join(tmp, "mockbin")
	if err := os.MkdirAll(mockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\necho \"$@\" >> \"$MOCK_LOG\"\n"
	if err := os.WriteFile(filepath.Join(mockDir, "rally"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(tmp, "calls.log")
	cmd := exec.Command("/bin/sh", append([]string{scriptAbs}, args...)...)
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(), "PATH="+mockDir+string(os.PathListSeparator)+os.Getenv("PATH"), "MOCK_LOG="+logFile)
	out, err := cmd.CombinedOutput()
	rc := 0
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("exec: %v", err)
	}
	calls, _ := os.ReadFile(logFile)
	return rc, string(out), string(calls)
}

func TestHandoffHookHelpIsNoOp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"x", "--help"}} {
		rc, out, calls := runHandoffHook(t, args...)
		if rc != 0 {
			t.Fatalf("%v: rc = %d, want 0", args, rc)
		}
		if calls != "" {
			t.Fatalf("%v: rally was called: %q, want zero calls", args, calls)
		}
		if !strings.Contains(out, "usage: laps handoff") {
			t.Fatalf("%v: missing usage line: %q", args, out)
		}
	}
}

func TestHandoffHookNoArgsSignalsOnce(t *testing.T) {
	rc, _, calls := runHandoffHook(t)
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if strings.TrimSpace(calls) != "progress --set-handoff" && strings.TrimSpace(calls) != "--set-handoff" {
		t.Fatalf("calls = %q, want exactly one set-handoff call", calls)
	}
	if n := strings.Count(calls, "\n"); n != 1 {
		t.Fatalf("call count = %d, want 1: %q", n, calls)
	}
}
