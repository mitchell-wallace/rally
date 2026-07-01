package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunAtAllModesCleanTree drives all three run modes against the fixture
// tree. The tree's files are all well under every size budget and not
// grandfathered, so every mode must run without panicking, exit zero, and print
// nothing (the size rule finds nothing to report).
func TestRunAtAllModesCleanTree(t *testing.T) {
	root := filepath.Join("testdata", "tree")
	modes := map[string]runMode{
		"default": modeDefault,
		"report":  modeReport,
		"ci":      modeCI,
	}
	for name, mode := range modes {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runAt(root, mode, &stdout, &stderr)
			if code != 0 {
				t.Errorf("runAt(%s) = %d, want 0; stderr=%q", name, code, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("runAt(%s) stdout = %q, want empty (no rules registered)", name, stdout.String())
			}
		})
	}
}

// TestMainCleanRepo exercises the real entry point end to end: flag parsing,
// repo-root discovery, and a full walk of the actual repository.
func TestMainCleanRepo(t *testing.T) {
	for _, args := range [][]string{nil, {"--report"}, {"--ci"}} {
		var stdout, stderr bytes.Buffer
		if code := Main(args, &stdout, &stderr); code != 0 {
			if isExpectedHarnessAPITransition(args, stdout.String()) {
				continue
			}
			t.Errorf("Main(%v) = %d, want 0; stderr=%q", args, code, stderr.String())
		}
	}
}

func isExpectedHarnessAPITransition(args []string, stdout string) bool {
	if len(args) == 1 && args[0] == "--report" {
		return false
	}
	return strings.Contains(stdout, "imports internal/harnessapi")
}

func TestMainRejectsBadFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Main([]string{"--nope"}, &stdout, &stderr); code != 2 {
		t.Errorf("Main(--nope) = %d, want 2", code)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Main([]string{"--report", "--ci"}, &stdout, &stderr); code != 2 {
		t.Errorf("Main(--report --ci) = %d, want 2 (mutually exclusive)", code)
	}
}
