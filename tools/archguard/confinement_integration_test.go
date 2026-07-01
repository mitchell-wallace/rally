package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/tools/archguard/policy"
)

// TestDepsIntegrationNewRelicLeakFixture proves the dependency-confinement rule
// against the on-disk testdata/deps fixture: the real walk parses the leak's New
// Relic import, the rule flags it Hard, --ci exits 1 with the exact ownership
// diagnostic, and the legitimate internal/telemetry owner next to it is NOT
// reported.
func TestDepsIntegrationNewRelicLeakFixture(t *testing.T) {
	root := filepath.Join("testdata", "deps")
	rs := []policy.Rule{policy.NewDependencyConfinement()}

	var stdout, stderr bytes.Buffer
	code := runWithRules(root, modeCI, &stdout, &stderr, rs)
	if code != 1 {
		t.Errorf("--ci = %d, want 1 (New Relic leak outside telemetry must fail); stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	const imp = "github.com/newrelic/go-agent/v3/newrelic"
	for _, want := range []string{
		"internal/agent/leak.go:1: dependency confinement:",
		"imports " + imp,
		"New Relic is owned by internal/telemetry",
		"adapters return typed evidence",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--ci stdout missing %q\ngot:\n%s", want, out)
		}
	}
	// The legitimate owner file must not be reported.
	if strings.Contains(out, "internal/telemetry/owner.go") {
		t.Errorf("internal/telemetry owns New Relic and must not be flagged:\n%s", out)
	}
}

// TestDepsIntegrationCleanTree runs the dependency-confinement rule against the
// dependency-free fixture tree and confirms every mode exits zero with no
// output.
func TestDepsIntegrationCleanTree(t *testing.T) {
	root := filepath.Join("testdata", "tree")
	rs := []policy.Rule{policy.NewDependencyConfinement()}
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

// TestTestutilIntegrationProductionImportFixture proves the testutil-confinement
// rule against the on-disk testdata/testutil fixture: the production leak file
// is flagged Hard with the exact diagnostic, --ci exits 1, and the _test.go
// sibling importing the same package is NOT reported (the rule is
// production-only).
func TestTestutilIntegrationProductionImportFixture(t *testing.T) {
	root := filepath.Join("testdata", "testutil")
	rs := []policy.Rule{policy.NewTestHelperConfinement()}

	var stdout, stderr bytes.Buffer
	code := runWithRules(root, modeCI, &stdout, &stderr, rs)
	if code != 1 {
		t.Errorf("--ci = %d, want 1 (production testutil import must fail); stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	const tpath = "github.com/mitchell-wallace/rally/internal/testutil"
	for _, want := range []string{
		"internal/agent/leak.go:1: test helper:",
		"imports " + tpath,
		"test helpers must not leak into production code",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--ci stdout missing %q\ngot:\n%s", want, out)
		}
	}
	// The _test.go sibling must not be reported (production-only rule).
	if strings.Contains(out, "leak_test.go") {
		t.Errorf("_test.go importing testutil must be exempt:\n%s", out)
	}
}

// TestTestutilIntegrationCleanTree runs the testutil rule against the fixture
// tree (no testutil imports) and confirms every mode exits zero with no output.
func TestTestutilIntegrationCleanTree(t *testing.T) {
	root := filepath.Join("testdata", "tree")
	rs := []policy.Rule{policy.NewTestHelperConfinement()}
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

// TestConfinementRulesAllGreenOnCleanFixture runs the full registered rule set
// (size + boundary + deps + testutil) against the clean fixture tree and
// confirms --ci stays green, mirroring the real-repo acceptance.
func TestConfinementRulesAllGreenOnCleanFixture(t *testing.T) {
	root := filepath.Join("testdata", "tree")
	var stdout, stderr bytes.Buffer
	code := runWithRules(root, modeCI, &stdout, &stderr, rules())
	if code != 0 {
		t.Errorf("--ci = %d, want 0; stderr=%q", code, stderr.String())
	}
}
