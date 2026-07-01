package policy

import (
	"reflect"
	"strings"
	"testing"
)

// fi builds a FileInfo for the size tests, keeping the cases terse.
func fi(path string, lines int, isTest bool) FileInfo {
	return FileInfo{Path: path, Package: dirOf(path), IsTest: isTest, Lines: lines}
}

// dirOf is a tiny helper mirroring package main's slashDir so test FileInfos
// look realistic; the size rule does not key on Package, so it is cosmetic.
func dirOf(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i]
	}
	return "."
}

// TestSizeBudgetNewOverProductionFileFails is the spec scenario "New oversize
// file fails": a production file of 900 lines (not grandfathered) hard-fails
// naming the file, the count, and the 800-line production budget.
func TestSizeBudgetNewOverProductionFileFails(t *testing.T) {
	r := NewSizeBudget(nil)
	got := r.Check([]FileInfo{fi("internal/agent/new_big.go", 900, false)})
	want := []Violation{{
		File:     "internal/agent/new_big.go",
		Severity: Hard,
		Category: "size",
		Reason:   "900 lines exceeds the 800-line production hard budget — split it or justify a grandfather entry",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Check:\n got %+v\nwant %+v", got, want)
	}
	if !HasHard(got) {
		t.Error("HasHard = false, want true (oversize new file must fail CI)")
	}
}

// TestSizeBudgetNewOverTestFileFails confirms the test-file hard budget (1000)
// is enforced for a non-grandfathered test file, and the diagnostic names the
// test budget.
func TestSizeBudgetNewOverTestFileFails(t *testing.T) {
	r := NewSizeBudget(nil)
	got := r.Check([]FileInfo{fi("internal/x/foo_test.go", 1100, true)})
	want := []Violation{{
		File:     "internal/x/foo_test.go",
		Severity: Hard,
		Category: "size",
		Reason:   "1100 lines exceeds the 1000-line test hard budget — split it or justify a grandfather entry",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Check:\n got %+v\nwant %+v", got, want)
	}
}

// TestSizeBudgetGrandfatheredOverCapFails is the spec scenario "Grandfathered
// file may not grow": a grandfathered file grown one line above its cap
// hard-fails reporting size vs cap and the ratchet instruction.
func TestSizeBudgetGrandfatheredOverCapFails(t *testing.T) {
	r := NewSizeBudget(map[string]int{"internal/relay/runner/run_one.go": 1510})
	got := r.Check([]FileInfo{fi("internal/relay/runner/run_one.go", 1511, false)})
	want := []Violation{{
		File:     "internal/relay/runner/run_one.go",
		Severity: Hard,
		Category: "size",
		Reason:   "1511 lines exceeds grandfather cap 1510 — split before growing this file; ratchet the cap down, never up",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Check:\n got %+v\nwant %+v", got, want)
	}
	if !HasHard(got) {
		t.Error("HasHard = false, want true")
	}
}

// TestSizeBudgetGrandfatheredExemptFromStandardHard confirms the central
// grandfather mechanic: a grandfathered file above the 800 production budget but
// at-or-under its own cap does NOT hard-fail. This is what keeps the green
// baseline alive.
func TestSizeBudgetGrandfatheredExemptFromStandardHard(t *testing.T) {
	r := NewSizeBudget(map[string]int{"internal/agent/opencode.go": 801})
	// 801 lines: over the 800 production hard budget, but exactly at its cap.
	got := r.Check([]FileInfo{fi("internal/agent/opencode.go", 801, false)})
	if HasHard(got) {
		t.Errorf("grandfathered-at-cap file hard-failed: %+v", got)
	}
	// It still warns (801 > 500), because warnings always show.
	if len(got) != 1 || got[0].Severity != Warning {
		t.Errorf("want exactly one warning, got %+v", got)
	}
}

// TestSizeBudgetWarningDoesNotFail is the spec scenario "Warning does not fail
// the build": a production file between 500 and 800 (not grandfathered) warns
// but produces no hard violation, so --ci exits zero.
func TestSizeBudgetWarningDoesNotFail(t *testing.T) {
	r := NewSizeBudget(nil)
	got := r.Check([]FileInfo{fi("internal/store/store.go", 541, false)})
	if HasHard(got) {
		t.Errorf("541-line file hard-failed: %+v", got)
	}
	if len(got) != 1 || got[0].Severity != Warning || got[0].File != "internal/store/store.go" {
		t.Errorf("want one warning for store.go, got %+v", got)
	}
}

// TestSizeBudgetTestWarningBand mirrors the production warning case for the test
// warn budget (700): a 764-line test warns but does not fail.
func TestSizeBudgetTestWarningBand(t *testing.T) {
	r := NewSizeBudget(nil)
	got := r.Check([]FileInfo{fi("internal/relay/runner/task_test.go", 764, true)})
	if HasHard(got) {
		t.Errorf("764-line test hard-failed: %+v", got)
	}
	if len(got) != 1 || got[0].Severity != Warning {
		t.Errorf("want one warning, got %+v", got)
	}
}

// TestSizeBudgetCleanFileNoFinding confirms an under-budget file produces no
// finding at all.
func TestSizeBudgetCleanFileNoFinding(t *testing.T) {
	r := NewSizeBudget(nil)
	for _, c := range []struct {
		name string
		f    FileInfo
	}{
		{"prod under warn", fi("internal/agent/small.go", 499, false)},
		{"prod at warn boundary", fi("internal/agent/edge.go", 500, false)},
		{"test under warn", fi("internal/agent/small_test.go", 699, true)},
		{"test at warn boundary", fi("internal/agent/edge_test.go", 700, true)},
		{"grandfathered under warn", fi("internal/relay/runner/run_one.go", 499, false)},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := r.Check([]FileInfo{c.f})
			if len(got) != 0 {
				t.Errorf("want no finding, got %+v", got)
			}
		})
	}
}

// TestSizeBudgetExactBudgetBoundaries pins the boundary semantics: the budget
// numbers are exclusive (>, not >=), so a file at exactly the hard budget does
// not fail and a file at exactly the warn budget does not warn.
func TestSizeBudgetExactBudgetBoundaries(t *testing.T) {
	r := NewSizeBudget(nil)
	// 800 production lines: exactly the hard budget, must not hard-fail.
	got := r.Check([]FileInfo{fi("internal/agent/edge.go", 800, false)})
	if HasHard(got) {
		t.Errorf("800-line file hard-failed at the boundary: %+v", got)
	}
	if len(got) != 1 || got[0].Severity != Warning {
		t.Errorf("800 > 500 so it should still warn, got %+v", got)
	}
}

// TestSizeBudgetNewSizeBudgetDefensiveCopy confirms NewSizeBudget copies its
// input map, so a caller mutating the source afterwards cannot perturb the rule.
func TestSizeBudgetNewSizeBudgetDefensiveCopy(t *testing.T) {
	src := map[string]int{"a.go": 100}
	r := NewSizeBudget(src)
	src["a.go"] = 999
	src["b.go"] = 1
	if r.Grandfather["a.go"] != 100 {
		t.Errorf("rule was mutated by caller: a.go = %d, want 100", r.Grandfather["a.go"])
	}
	if _, ok := r.Grandfather["b.go"]; ok {
		t.Error("rule picked up a caller-added entry b.go")
	}
}

// TestSizeBudgetReportRegeneratesMap asserts the --report section lists every
// over-hard-budget file at its actual line count, sorted by path, with the
// ratchet guidance header. This is the contract the implementer uses to
// regenerate the committed baseline.
func TestSizeBudgetReportRegeneratesMap(t *testing.T) {
	r := NewSizeBudget(nil)
	files := []FileInfo{
		fi("internal/relay/runner/run_one.go", 1510, false), // over 800
		fi("internal/store/store.go", 541, false),           // warn only, not in map
		fi("internal/agent/agent_test.go", 2812, true),      // over 1000
		fi("internal/agent/tiny.go", 10, false),             // under budget
	}
	got := r.Report(files)
	for _, want := range []string{
		"`archguard --report`",
		"Ratchet the cap down, never up.",
		`"internal/agent/agent_test.go": 2812,`,
		`"internal/relay/runner/run_one.go": 1510,`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Report missing %q\ngot:\n%s", want, got)
		}
	}
	// The warn-only file (store.go, 541) must NOT appear in the grandfather map.
	if strings.Contains(got, `"internal/store/store.go"`) {
		t.Errorf("Report should not grandfather a warn-only file\ngot:\n%s", got)
	}
}

// TestSizeBudgetReportEmptyWhenNothingOverHard confirms the report section is
// empty when no file exceeds its hard budget (a tree with nothing to
// grandfather).
func TestSizeBudgetReportEmptyWhenNothingOverHard(t *testing.T) {
	r := NewSizeBudget(nil)
	files := []FileInfo{
		fi("internal/store/store.go", 541, false), // warn only
		fi("internal/agent/tiny.go", 10, false),
	}
	if got := r.Report(files); strings.TrimSpace(got) != "" {
		t.Errorf("Report = %q, want empty", got)
	}
}
