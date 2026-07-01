package policy

import (
	"reflect"
	"testing"
)

// stubRule is a test double: it returns a fixed set of violations so the engine
// seam can be exercised before any real rule class exists.
type stubRule struct {
	name       string
	violations []Violation
}

func (r stubRule) Name() string { return r.name }

func (r stubRule) Check([]FileInfo) []Violation { return r.violations }

// stubReporter also implements Reporter by embedding stubRule (so it satisfies
// Rule via promotion) and adding its own report payload.
type stubReporter struct {
	stubRule
	report string
}

func (r stubReporter) Report([]FileInfo) string { return r.report }

func TestViolationString(t *testing.T) {
	withLine := Violation{
		File:     "internal/relay/relay.go",
		Line:     1,
		Category: "import boundary",
		Reason:   "imports internal/relay/runner — keep the runner → relay edge one-way",
	}
	want := "internal/relay/relay.go:1: import boundary: imports internal/relay/runner — keep the runner → relay edge one-way"
	if got := withLine.String(); got != want {
		t.Errorf("with line:\n got %q\nwant %q", got, want)
	}

	wholeFile := Violation{
		File:     "internal/relay/runner/run_one.go",
		Category: "size",
		Reason:   "1620 lines exceeds grandfather cap 1510",
	}
	want = "internal/relay/runner/run_one.go: size: 1620 lines exceeds grandfather cap 1510"
	if got := wholeFile.String(); got != want {
		t.Errorf("whole file:\n got %q\nwant %q", got, want)
	}
}

func TestNewEngineNoRulesIsClean(t *testing.T) {
	e := NewEngine()
	if got := e.Check(nil); got != nil {
		t.Errorf("Check with no rules = %v, want nil", got)
	}
	if got := e.Reports(nil); got != nil {
		t.Errorf("Reports with no rules = %v, want nil", got)
	}
}

// TestEngineCheckAggregatesAndSorts confirms the engine collects violations
// from every rule and returns them in the deterministic (file, line, category)
// order, regardless of rule or emission order.
func TestEngineCheckAggregatesAndSorts(t *testing.T) {
	e := NewEngine(
		stubRule{name: "b", violations: []Violation{
			{File: "z.go", Line: 5, Category: "size", Severity: Hard},
			{File: "a.go", Line: 2, Category: "import boundary", Severity: Warning},
		}},
		stubRule{name: "a", violations: []Violation{
			{File: "a.go", Line: 1, Category: "size", Severity: Warning},
			{File: "a.go", Line: 2, Category: "dependency confinement", Severity: Hard},
		}},
	)
	got := e.Check(nil)
	want := []Violation{
		{File: "a.go", Line: 1, Category: "size", Severity: Warning},
		{File: "a.go", Line: 2, Category: "dependency confinement", Severity: Hard},
		{File: "a.go", Line: 2, Category: "import boundary", Severity: Warning},
		{File: "z.go", Line: 5, Category: "size", Severity: Hard},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Check order:\n got %v\nwant %v", got, want)
	}
}

func TestHasHard(t *testing.T) {
	warns := []Violation{{Severity: Warning}, {Severity: Warning}}
	if HasHard(warns) {
		t.Error("HasHard(all warnings) = true, want false")
	}
	mixed := []Violation{{Severity: Warning}, {Severity: Hard}}
	if !HasHard(mixed) {
		t.Error("HasHard(mixed) = false, want true")
	}
	if HasHard(nil) {
		t.Error("HasHard(nil) = true, want false")
	}
}

// TestEngineReportsCollectsReporterSections confirms only rules implementing
// Reporter contribute report sections, in rule order, skipping empties.
func TestEngineReportsCollectsReporterSections(t *testing.T) {
	e := NewEngine(
		stubReporter{stubRule: stubRule{name: "grandfather"}, report: "map A\n"},
		stubRule{name: "plain-rule"}, // not a Reporter
		stubReporter{stubRule: stubRule{name: "empty"}, report: "   \n"},
		stubReporter{stubRule: stubRule{name: "deps"}, report: "map B"},
	)
	got := e.Reports(nil)
	want := []string{"map A", "map B"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Reports = %v, want %v", got, want)
	}
}
