package policy

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// depFile builds a production FileInfo in package pkg importing the given
// (full) import paths. Keeps the dependency-confinement cases terse.
func depFile(pkg string, imps ...string) FileInfo {
	return FileInfo{
		Path:    pkg + "/x.go",
		Package: pkg,
		IsTest:  false,
		Imports: imps,
	}
}

// TestDepConfinementNewRelicOutsideTelemetryFails is the spec scenario "New
// Relic leak outside telemetry fails": a production file outside
// internal/telemetry importing New Relic hard-fails with the ownership reason,
// naming the file, the offending import, and the owning package.
func TestDepConfinementNewRelicOutsideTelemetryFails(t *testing.T) {
	r := NewDependencyConfinement()
	got := r.Check([]FileInfo{depFile("internal/agent", "github.com/newrelic/go-agent/v3/newrelic")})
	const imp = "github.com/newrelic/go-agent/v3/newrelic"
	want := []Violation{{
		File:     "internal/agent/x.go",
		Line:     1,
		Severity: Hard,
		Category: "dependency confinement",
		Reason: "imports " + imp +
			" — New Relic is owned by internal/telemetry; adapters return typed evidence and let relay/runtime emit telemetry",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Check:\n got %+v\nwant %+v", got, want)
	}
	// The rendered diagnostic must name the offending file, import, category,
	// owner, and architectural reason.
	rendered := got[0].String()
	for _, want := range []string{
		"internal/agent/x.go:1: dependency confinement:",
		"imports " + imp,
		"New Relic is owned by internal/telemetry",
		"adapters return typed evidence",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered diagnostic missing %q\n got %q", want, rendered)
		}
	}
	if !HasHard(got) {
		t.Error("HasHard = false, want true (dep leak must fail CI)")
	}
}

// TestDepConfinementTestInclusive confirms dependency confinement applies to
// _test.go files the same as production: a New Relic import in a non-telemetry
// test file is still a leak (the spec scopes confinement to production AND test
// alike).
func TestDepConfinementTestInclusive(t *testing.T) {
	r := NewDependencyConfinement()
	testFile := FileInfo{
		Path:    "internal/agent/agent_test.go",
		Package: "internal/agent",
		IsTest:  true,
		Imports: []string{"github.com/newrelic/go-agent/v3/newrelic"},
	}
	got := r.Check([]FileInfo{testFile})
	if len(got) != 1 || got[0].Severity != Hard {
		t.Fatalf("want one hard violation from a test file, got %+v", got)
	}
	if got[0].Category != "dependency confinement" {
		t.Errorf("Category = %q, want dependency confinement", got[0].Category)
	}
}

// TestDepConfinementCobraCommandShapedPasses is the spec scenario "Command-
// shaped Cobra usage is allowed": cobra imports in cmd/rally, internal/cli, and
// internal/progress (the command-shaped packages) must pass.
func TestDepConfinementCobraCommandShapedPasses(t *testing.T) {
	r := NewDependencyConfinement()
	for _, pkg := range []string{"cmd/rally", "internal/cli", "internal/progress"} {
		got := r.Check([]FileInfo{depFile(pkg, "github.com/spf13/cobra")})
		if len(got) != 0 {
			t.Errorf("%s importing cobra must be allowed, got %+v", pkg, got)
		}
	}
}

// TestDepConfinementEachDepOwnerPasses walks the confinement table and confirms
// every owner of every dep may import its dep, while a non-owner (internal/agent)
// is flagged for each dep. This is the encoding-vs-reality contract for the
// whole Decision 5 table.
func TestDepConfinementEachDepOwnerPasses(t *testing.T) {
	r := NewDependencyConfinement()
	for _, dep := range confinedDepsForTest() {
		t.Run(dep.name+" owner passes", func(t *testing.T) {
			for _, owner := range dep.owners {
				got := r.Check([]FileInfo{depFile(owner, dep.prefix)})
				if len(got) != 0 {
					t.Errorf("owner %s importing %s must pass, got %+v", owner, dep.prefix, got)
				}
			}
		})
		t.Run(dep.name+" non-owner fails", func(t *testing.T) {
			got := r.Check([]FileInfo{depFile("internal/agent", dep.prefix)})
			if len(got) != 1 || got[0].Severity != Hard {
				t.Fatalf("%s in non-owner internal/agent: want 1 hard violation, got %+v", dep.name, got)
			}
			if !strings.Contains(got[0].Reason, dep.name+" is owned by") {
				t.Errorf("%s: Reason should name the owner, got %q", dep.name, got[0].Reason)
			}
		})
	}
}

func TestDepConfinementEachDepDiagnosticText(t *testing.T) {
	cases := []struct {
		name string
		imp  string
		want string
	}{
		{
			name: "New Relic",
			imp:  "github.com/newrelic/go-agent/v3/newrelic",
			want: "imports github.com/newrelic/go-agent/v3/newrelic — New Relic is owned by internal/telemetry; adapters return typed evidence and let relay/runtime emit telemetry",
		},
		{
			name: "go-toml",
			imp:  "github.com/pelletier/go-toml/v2",
			want: "imports github.com/pelletier/go-toml/v2 — go-toml is owned by internal/config; keep TOML decoding in the config layer",
		},
		{
			name: "cobra",
			imp:  "github.com/spf13/cobra",
			want: "imports github.com/spf13/cobra — cobra is owned by cmd/rally, internal/cli, internal/progress; it is the CLI framework; only command-shaped packages may depend on it",
		},
		{
			name: "huh",
			imp:  "github.com/charmbracelet/huh",
			want: "imports github.com/charmbracelet/huh — huh is owned by internal/cli, internal/user_prompt; it is the interactive-prompt library; only prompt packages may depend on it",
		},
		{
			name: "lipgloss",
			imp:  "github.com/charmbracelet/lipgloss",
			want: "imports github.com/charmbracelet/lipgloss — lipgloss is owned by internal/cli, internal/style; keep presentation logic in the presentation layer",
		},
	}
	r := NewDependencyConfinement()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.Check([]FileInfo{depFile("internal/agent", c.imp)})
			if len(got) != 1 {
				t.Fatalf("want 1 violation, got %d: %+v", len(got), got)
			}
			if got[0].Reason != c.want {
				t.Errorf("Reason:\n got %q\nwant %q", got[0].Reason, c.want)
			}
		})
	}
}

// TestDepConfinementMajorVersionSubpathsMatched confirms the module-path prefix
// matching covers the major-version subpaths actually used in the tree (New
// Relic v3, go-toml v2), so an aliased/major-version import is still confined.
func TestDepConfinementMajorVersionSubpathsMatched(t *testing.T) {
	r := NewDependencyConfinement()
	cases := []struct {
		name string
		imp  string
	}{
		{"newrelic v3 newrelic", "github.com/newrelic/go-agent/v3/newrelic"},
		{"newrelic v3 integrations", "github.com/newrelic/go-agent/v3/integrations/x"},
		{"go-toml v2", "github.com/pelletier/go-toml/v2"},
		{"cobra bare", "github.com/spf13/cobra"},
		{"huh bare", "github.com/charmbracelet/huh"},
		{"lipgloss bare", "github.com/charmbracelet/lipgloss"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.Check([]FileInfo{depFile("internal/agent", c.imp)})
			if len(got) != 1 {
				t.Fatalf("want %s confined in non-owner, got %d violations: %+v", c.imp, len(got), got)
			}
		})
	}
}

// TestDepConfinementDisjointPrefixNoFalseMatch confirms the prefix match is
// path-segment aware: a path that merely starts with a dep's module characters
// (e.g. "github.com/newrelic/go-agentfoo") is NOT treated as the confined dep,
// and unconfined third-party imports are ignored entirely.
func TestDepConfinementDisjointPrefixNoFalseMatch(t *testing.T) {
	r := NewDependencyConfinement()
	files := []FileInfo{depFile("internal/agent",
		"github.com/newrelic/go-agentfoo", // looks like go-agent but is not
		"github.com/spf13/cobrax",         // looks like cobra but is not
		"github.com/example/other",        // unconfined third-party
		"fmt",                             // stdlib
		"github.com/mitchell-wallace/rally/internal/store", // internal, not a dep
	)}
	if got := r.Check(files); len(got) != 0 {
		t.Errorf("disjoint/unconfined imports must be ignored, got %+v", got)
	}
}

// TestDepConfinementMultipleLeaksEachReported confirms a file leaking two
// confined deps yields two violations (one per offending import), each with its
// own ownership reason.
func TestDepConfinementMultipleLeaksEachReported(t *testing.T) {
	r := NewDependencyConfinement()
	got := r.Check([]FileInfo{depFile("internal/agent",
		"github.com/newrelic/go-agent/v3/newrelic",
		"github.com/charmbracelet/lipgloss",
	)})
	if len(got) != 2 {
		t.Fatalf("want 2 violations, got %d: %+v", len(got), got)
	}
	for _, v := range got {
		if v.Severity != Hard || v.Category != "dependency confinement" {
			t.Errorf("violation malformed: %+v", v)
		}
	}
}

// TestDepConfinementTableMatchesDecision5 pins the shipped table to exactly the
// five Decision 5 deps with their owning packages, so a row is never silently
// dropped or an owner quietly changed.
func TestDepConfinementTableMatchesDecision5(t *testing.T) {
	want := []struct {
		prefix string
		name   string
		owners []string
		why    string
	}{
		{"github.com/newrelic/go-agent", "New Relic", []string{"internal/telemetry"}, "adapters return typed evidence and let relay/runtime emit telemetry"},
		{"github.com/pelletier/go-toml", "go-toml", []string{"internal/config"}, "keep TOML decoding in the config layer"},
		{"github.com/spf13/cobra", "cobra", []string{"cmd/rally", "internal/cli", "internal/progress"}, "it is the CLI framework; only command-shaped packages may depend on it"},
		{"github.com/charmbracelet/huh", "huh", []string{"internal/cli", "internal/user_prompt"}, "it is the interactive-prompt library; only prompt packages may depend on it"},
		{"github.com/charmbracelet/lipgloss", "lipgloss", []string{"internal/style", "internal/cli"}, "keep presentation logic in the presentation layer"},
	}
	got := confinedDepsForTest()
	if len(got) != len(want) {
		t.Fatalf("table size = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.prefix != w.prefix || g.name != w.name {
			t.Errorf("row %d: prefix/name = %q/%q, want %q/%q", i, g.prefix, g.name, w.prefix, w.name)
		}
		gotOwners := append([]string(nil), g.owners...)
		wantOwners := append([]string(nil), w.owners...)
		sort.Strings(gotOwners)
		sort.Strings(wantOwners)
		if !reflect.DeepEqual(gotOwners, wantOwners) {
			t.Errorf("row %d (%s) owners:\n got %v\nwant %v", i, w.name, gotOwners, wantOwners)
		}
		if g.why != w.why {
			t.Errorf("row %d (%s) why:\n got %q\nwant %q", i, w.name, g.why, w.why)
		}
	}
}
