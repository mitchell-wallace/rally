// Package policy holds archguard's architecture policy engine: the data types
// that describe a scanned Go file, the violations a rule can raise, and the
// Engine that runs a set of rules over the scanned files.
//
// The engine is deliberately independent of how files are discovered, parsed,
// or printed (that lives in package main). This separation keeps the policy
// layer a small, unit-testable unit: a test can construct FileInfo values and
// rules directly, without touching the filesystem walk or the CLI.
//
// Concrete rules live in this package: SizeBudget (file-size budgets with a
// grandfathered baseline) is here now; the import-boundary,
// dependency-confinement, and testutil-confinement rules land in later laps.
package policy

import (
	"fmt"
	"sort"
	"strings"
)

// Severity distinguishes advisory warnings from hard, build-failing violations.
type Severity int

const (
	// Warning is advisory: it is printed but never sets a non-zero exit code.
	Warning Severity = iota
	// Hard is a build-failing violation: it sets a non-zero exit code in
	// default and --ci modes.
	Hard
)

// FileInfo is the parsed, policy-relevant view of a single Go source file. It
// carries only what the rules need, so rules never re-read the filesystem.
type FileInfo struct {
	// Path is the repo-relative, slash-separated path to the file, e.g.
	// "internal/relay/relay.go".
	Path string
	// Package is the repo-relative, slash-separated directory the file lives
	// in, e.g. "internal/relay". This is the unit import-boundary and
	// dependency-confinement rules key on.
	Package string
	// IsTest reports whether the file is a Go test file (name ends _test.go).
	IsTest bool
	// Lines is the physical line count (newline count, matching `wc -l`).
	Lines int
	// Imports is the list of import paths the file declares, with any alias or
	// dot prefix stripped (e.g. `"go/ast"`, `github.com/spf13/cobra`).
	Imports []string
}

// Violation is a single policy finding against a file. The zero Line value is
// omitted from the rendered diagnostic (used for whole-file findings such as
// size); a positive Line renders as `path:line`.
type Violation struct {
	// File is the repo-relative path the finding is anchored to.
	File string
	// Line is the 1-indexed line, or 0 to render a whole-file finding.
	Line int
	// Severity is Warning or Hard.
	Severity Severity
	// Category is the short rule family, e.g. "size", "import boundary",
	// "dependency confinement", "test helper".
	Category string
	// Reason is the human-readable architectural explanation, including the
	// offending detail. It should read as a full clause after the category.
	Reason string
}

// String renders the diagnostic in the format documented in design.md:
//
//	path:line: category: reason      (Line > 0)
//	path: category: reason           (Line == 0)
func (v Violation) String() string {
	if v.Line > 0 {
		return fmt.Sprintf("%s:%d: %s: %s", v.File, v.Line, v.Category, v.Reason)
	}
	return fmt.Sprintf("%s: %s: %s", v.File, v.Category, v.Reason)
}

// Rule inspects the full set of scanned files and returns any violations. A
// rule receives every file at once (rather than one at a time) so that
// whole-graph rules — such as import-boundary checks that compare a package's
// edges against an allow-list — can be expressed naturally.
type Rule interface {
	// Name is a short identifier for the rule, used in diagnostics and tests.
	Name() string
	// Check returns the violations the rule finds across all files.
	Check(files []FileInfo) []Violation
}

// Reporter is an optional interface a Rule may implement to contribute a
// paste-ready section to --report output (for example, a size rule emitting a
// regeneratable grandfather map). Rules that only raise violations need not
// implement it.
type Reporter interface {
	// Report returns a paste-ready section for --report, or "" for nothing.
	Report(files []FileInfo) string
}

// Engine runs a fixed set of rules over the scanned files.
type Engine struct {
	rules []Rule
}

// NewEngine builds an Engine from the given rules, preserving their order.
func NewEngine(rules ...Rule) *Engine {
	return &Engine{rules: rules}
}

// Check runs every rule over the files and returns the aggregated violations,
// sorted deterministically (by file, then line, then category) so output and
// tests are stable regardless of rule or walk order.
func (e *Engine) Check(files []FileInfo) []Violation {
	var all []Violation
	for _, r := range e.rules {
		all = append(all, r.Check(files)...)
	}
	sortViolations(all)
	return all
}

// Reports gathers the paste-ready sections from any rules implementing
// Reporter, in rule order, skipping empty sections.
func (e *Engine) Reports(files []FileInfo) []string {
	var out []string
	for _, r := range e.rules {
		rep, ok := r.(Reporter)
		if !ok {
			continue
		}
		s := rep.Report(files)
		if strings.TrimSpace(s) == "" {
			continue
		}
		out = append(out, strings.TrimRight(s, "\n"))
	}
	return out
}

// HasHard reports whether any violation is Hard — the signal default and --ci
// modes use to decide the exit code.
func HasHard(vs []Violation) bool {
	for _, v := range vs {
		if v.Severity == Hard {
			return true
		}
	}
	return false
}

func sortViolations(vs []Violation) {
	sort.SliceStable(vs, func(i, j int) bool {
		a, b := vs[i], vs[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Category < b.Category
	})
}
