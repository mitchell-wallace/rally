package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/tools/archguard/policy"
)

// TestImportIntegrationBoundaryCleanTree runs the real boundary rule against
// the small fixture tree (which imports nothing internal) and confirms every
// mode exits zero with no output.
func TestImportIntegrationBoundaryCleanTree(t *testing.T) {
	root := filepath.Join("testdata", "tree")
	rs := []policy.Rule{policy.NewImportBoundary()}
	for name, mode := range map[string]runMode{"default": modeDefault, "report": modeReport, "ci": modeCI} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runWithRules(root, mode, &stdout, &stderr, rs)
			if code != 0 {
				t.Errorf("runWithRules(%s) = %d, want 0; stderr=%q", name, code, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("runWithRules(%s) stdout = %q, want empty", name, stdout.String())
			}
		})
	}
}

// TestImportIntegrationBoundaryFixture proves the flagship relay -> relay/runner
// edge against the on-disk testdata/boundary fixture: the real walk parses the
// fixture's import, the boundary rule flags it Hard, and --ci exits 1 with the
// exact diagnostic from design.md.
func TestImportIntegrationBoundaryFixture(t *testing.T) {
	root := filepath.Join("testdata", "boundary")
	rs := []policy.Rule{policy.NewImportBoundary()}

	var stdout, stderr bytes.Buffer
	code := runWithRules(root, modeCI, &stdout, &stderr, rs)
	if code != 1 {
		t.Errorf("--ci = %d, want 1 (flagship relay->runner edge must fail); stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"internal/relay/relay.go:1: import boundary:",
		"imports " + "github.com/mitchell-wallace/rally/internal/relay/runner",
		"the relay primitives must not depend on the orchestrator; keep the runner to relay edge one-way",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--ci stdout missing %q\ngot:\n%s", want, out)
		}
	}
}

// TestImportIntegrationReportPrintsHardBoundaryViolation confirms report mode
// stays useful for regeneration audits: it exits zero, but still prints hard
// non-size violations after any reporter sections.
func TestImportIntegrationReportPrintsHardBoundaryViolation(t *testing.T) {
	root := filepath.Join("testdata", "boundary")
	rs := []policy.Rule{policy.NewImportBoundary()}

	var stdout, stderr bytes.Buffer
	code := runWithRules(root, modeReport, &stdout, &stderr, rs)
	if code != 0 {
		t.Errorf("--report = %d, want 0; stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"internal/relay/relay.go:1: import boundary:",
		"imports github.com/mitchell-wallace/rally/internal/relay/runner",
		"keep the runner to relay edge one-way",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--report stdout missing %q\ngot:\n%s", want, out)
		}
	}
}

// TestImportIntegrationBoundaryWarningsDontFailCI confirms a boundary rule
// alongside the size rule keeps --ci green on the clean repo fixture (only size
// warnings, no hard violations), mirroring the real repo acceptance.
func TestImportIntegrationBoundaryWarningsDontFailCI(t *testing.T) {
	root := filepath.Join("testdata", "tree")
	rs := rules() // size + boundary
	var stdout, stderr bytes.Buffer
	code := runWithRules(root, modeCI, &stdout, &stderr, rs)
	if code != 0 {
		t.Errorf("--ci = %d, want 0; stderr=%q", code, stderr.String())
	}
}
