package policy

import (
	"fmt"
	"sort"
	"strings"
)

// File-size budgets (design.md Decision 3).
//
// production `.go` warn 500 / hard 800
// _test.go             warn 700 / hard 1000
// generated            exempt (handled in the scan layer, which skips them)
//
// A file listed in the grandfather map is exempt from the standard hard budget
// but fails if it grows above its own recorded cap. A file NOT in the map fails
// if it exceeds the standard hard budget — that is how a new oversize file is
// caught. Warnings (over 500 / over 700) print but never set the exit code.
const (
	prodWarn = 500
	prodHard = 800
	testWarn = 700
	testHard = 1000
)

// SizeBudget enforces file-size budgets with a grandfathered baseline. It is
// the only v1 size rule; import-boundary, dependency-confinement, and
// testutil rules land in later laps.
//
// Grandfather is a path->cap map. A file in the map is exempt from the standard
// hard budget but must stay at or under its recorded cap. A file not in the map
// is held to the standard hard budget. Warnings never escalate to hard.
type SizeBudget struct {
	// Grandfather is the committed baseline: repo-relative path -> the line cap
	// the file was grandfathered at (its actual line count when the baseline was
	// regenerated). Treat caps as a ratchet: only ever lower them, never raise.
	Grandfather map[string]int
}

// NewSizeBudget builds a SizeBudget with a copy of the grandfather map so a
// caller mutating its map later cannot perturb the rule.
func NewSizeBudget(grandfather map[string]int) *SizeBudget {
	gf := make(map[string]int, len(grandfather))
	for k, v := range grandfather {
		gf[k] = v
	}
	return &SizeBudget{Grandfather: gf}
}

// Name identifies the rule in diagnostics and tests.
func (*SizeBudget) Name() string { return "size" }

// budget returns the (warn, hard) line thresholds for a file kind.
func budget(isTest bool) (warn, hard int) {
	if isTest {
		return testWarn, testHard
	}
	return prodWarn, prodHard
}

// Check applies the size policy to every file. Each file can produce at most
// one violation: a hard violation when a file exceeds the budget it is held to
// (the standard hard budget for non-grandfathered files, or the grandfather cap
// for grandfathered files), or a warning when it is merely over the warn line.
// A file is never both warned and hard-failed: once it is hard, that is the
// finding that matters.
func (s *SizeBudget) Check(files []FileInfo) []Violation {
	var vs []Violation
	for _, f := range files {
		if v, ok := s.checkFile(f); ok {
			vs = append(vs, v)
		}
	}
	return vs
}

// checkFile evaluates a single file. It returns ok=false when the file is under
// every applicable budget (no finding at all).
func (s *SizeBudget) checkFile(f FileInfo) (Violation, bool) {
	warn, hard := budget(f.IsTest)

	if cap, ok := s.Grandfather[f.Path]; ok {
		// Grandfathered: exempt from the standard hard budget, but held to its
		// own cap. A warning still fires if it is over the warn line but under
		// its cap — warnings never fail, but they should still be visible.
		if f.Lines > cap {
			return Violation{
				File:     f.Path,
				Severity: Hard,
				Category: "size",
				Reason: fmt.Sprintf(
					"%d lines exceeds grandfather cap %d — split before growing this file; ratchet the cap down, never up",
					f.Lines, cap,
				),
			}, true
		}
		if f.Lines > warn {
			return sizeWarn(f.Path, f.Lines), true
		}
		return Violation{}, false
	}

	// Not grandfathered: the standard hard budget applies.
	if f.Lines > hard {
		kind := "production"
		if f.IsTest {
			kind = "test"
		}
		return Violation{
			File:     f.Path,
			Severity: Hard,
			Category: "size",
			Reason: fmt.Sprintf(
				"%d lines exceeds the %d-line %s hard budget — split it or justify a grandfather entry",
				f.Lines, hard, kind,
			),
		}, true
	}
	if f.Lines > warn {
		return sizeWarn(f.Path, f.Lines), true
	}
	return Violation{}, false
}

// sizeWarn builds the advisory-only warning. The reason carries the file kind's
// warn line so the diagnostic is self-explaining without reading policy source.
func sizeWarn(path string, lines int) Violation {
	return Violation{
		File:     path,
		Severity: Warning,
		Category: "size",
		Reason:   fmt.Sprintf("%d lines is over the warn budget — split before it reaches the hard cap", lines),
	}
}

// Report renders the paste-ready grandfather map for `--report`: every scanned
// file currently over its standard hard budget, listed as `path: <cap>` with
// cap = the file's actual line count, so pasting the section straight into the
// source rebuilds a green baseline. Files are sorted by path for stability.
//
// A file that already appears in the committed Grandfather map but has since
// shrunk below the standard hard budget is dropped (it no longer needs an
// entry); that keeps the regenerated map from re-grandfathering files the tree
// has already grown out of.
func (s *SizeBudget) Report(files []FileInfo) string {
	type entry struct {
		path string
		cap  int
	}
	var over []entry
	for _, f := range files {
		_, hard := budget(f.IsTest)
		if f.Lines > hard {
			over = append(over, entry{path: f.Path, cap: f.Lines})
		}
	}
	if len(over) == 0 {
		return ""
	}
	sort.Slice(over, func(i, j int) bool { return over[i].path < over[j].path })

	var b strings.Builder
	b.WriteString("# Grandfather map (regenerated by `archguard --report`).\n")
	b.WriteString("# Each cap is the file's current actual line count; a\n")
	b.WriteString("# grandfathered file fails if it grows above its cap.\n")
	b.WriteString("# Ratchet the cap down, never up.\n")
	b.WriteString("grandfather = map[string]int{\n")
	for _, e := range over {
		b.WriteString(fmt.Sprintf("\t%q: %d,\n", e.path, e.cap))
	}
	b.WriteString("}\n")
	return b.String()
}
