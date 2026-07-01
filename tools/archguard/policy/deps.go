package policy

import (
	"fmt"
	"sort"
	"strings"
)

// Dependency-confinement rules (design.md Decision 5).
//
// Third-party dependencies are confined to their owning packages so an
// architectural boundary cannot be silently crossed by reaching for a dep the
// owning package was set up to absorb. Unlike the internal import-boundary
// rules (Decision 4), dependency confinement is TEST-INCLUSIVE: a New Relic
// import in a non-telemetry _test.go is still a leak, because the confinement
// intent is package-level. The first pass confines the obvious owners only and
// deliberately does not encode broader terminal dependencies such as
// golang.org/x/term.

// confinedDep describes a third-party dependency confined to a set of owning
// packages.
type confinedDep struct {
	// prefix is the import-path prefix that identifies the dependency. It is a
	// module path, so it matches the dependency and its major-version subpath
	// (e.g. "github.com/newrelic/go-agent" matches ".../go-agent/v3/newrelic").
	// Matching is path-segment aware: "github.com/a/b" matches "github.com/a/b"
	// and "github.com/a/b/..." but not "github.com/a/bear".
	prefix string
	// name is the human-friendly dependency name used in the diagnostic.
	name string
	// owners are the repo-relative package directories allowed to import the
	// dependency (exact matches against FileInfo.Package), e.g.
	// "internal/telemetry". A file importing the dep whose package is not an
	// owner is a leak.
	owners []string
	// why is the one-line architectural reason appended to the diagnostic after
	// the owner list, explaining the intent the confinement protects.
	why string
}

// confinedDeps is the dependency-confinement table (Decision 5), verified
// against HEAD via task 1.4: every current import of each dep lives under one
// of its owners. Regenerate the scope with a repo-wide import search before
// widening a row; never widen silently (surface the new owner instead).
var confinedDeps = []confinedDep{
	{
		prefix: "github.com/newrelic/go-agent",
		name:   "New Relic",
		owners: []string{"internal/telemetry"},
		why:    "adapters return typed evidence and let relay/runtime emit telemetry",
	},
	{
		prefix: "github.com/pelletier/go-toml",
		name:   "go-toml",
		owners: []string{"internal/config"},
		why:    "keep TOML decoding in the config layer",
	},
	{
		prefix: "github.com/spf13/cobra",
		name:   "cobra",
		owners: []string{"cmd/rally", "internal/cli", "internal/progress"},
		why:    "it is the CLI framework; only command-shaped packages may depend on it",
	},
	{
		prefix: "github.com/charmbracelet/huh",
		name:   "huh",
		owners: []string{"internal/cli", "internal/user_prompt"},
		why:    "it is the interactive-prompt library; only prompt packages may depend on it",
	},
	{
		prefix: "github.com/charmbracelet/lipgloss",
		name:   "lipgloss",
		owners: []string{"internal/style", "internal/cli"},
		why:    "keep presentation logic in the presentation layer",
	},
}

// importPathHasPrefix reports whether import path imp is exactly prefix or a
// sub-path of it (prefix+"/..."), so a module prefix never matches an unrelated
// path that merely starts with the same characters (e.g. a dep "github.com/a/b"
// does not match "github.com/a/bear").
func importPathHasPrefix(imp, prefix string) bool {
	return imp == prefix || strings.HasPrefix(imp, prefix+"/")
}

// depForImport returns the confinedDep whose prefix the import matches, or nil.
// The five prefixes are disjoint, so an import matches at most one.
func depForImport(imp string) *confinedDep {
	for i := range confinedDeps {
		if importPathHasPrefix(imp, confinedDeps[i].prefix) {
			return &confinedDeps[i]
		}
	}
	return nil
}

// isDepOwner reports whether pkg is one of dep's owning package directories.
func (dep confinedDep) isOwner(pkg string) bool {
	for _, o := range dep.owners {
		if o == pkg {
			return true
		}
	}
	return false
}

// joinOwners renders the owner package list for the diagnostic. A single owner
// is shown bare; multiple are comma-separated and sorted for stable output
// regardless of table order.
func joinOwners(owners []string) string {
	if len(owners) == 1 {
		return owners[0]
	}
	sorted := append([]string(nil), owners...)
	sort.Strings(sorted)
	return strings.Join(sorted, ", ")
}

// DependencyConfinement confines the third-party dependencies in confinedDeps
// to their owning packages. It is test-inclusive: production and _test.go files
// are checked alike, because the confinement intent is package-level.
type DependencyConfinement struct{}

// NewDependencyConfinement builds the dependency-confinement rule. It is
// stateless today; the constructor exists so rules() reads uniformly and so
// future laps can extend it (e.g. a regeneratable scope report) without
// changing call sites.
func NewDependencyConfinement() *DependencyConfinement { return &DependencyConfinement{} }

// Name identifies the rule in diagnostics and tests.
func (*DependencyConfinement) Name() string { return "dependency confinement" }

// Check walks every file (production and test — confinement is test-inclusive)
// and raises a Hard "dependency confinement" violation for each confined-dep
// import whose file is not under one of the dep's owning packages. Each
// offending import yields exactly one violation, anchored at line 1.
func (*DependencyConfinement) Check(files []FileInfo) []Violation {
	var vs []Violation
	for _, f := range files {
		for _, imp := range f.Imports {
			dep := depForImport(imp)
			if dep == nil {
				continue // not a confined dependency.
			}
			if dep.isOwner(f.Package) {
				continue // the owning package may use its dep.
			}
			vs = append(vs, depViolation(f.Path, imp, dep))
		}
	}
	return vs
}

// depViolation builds the hard dependency-confinement finding for a leaked
// import. The reason leads with the offending import and appends the dep name,
// its owning packages, and the architectural intent, so the diagnostic is
// self-explaining without reading the policy source. It is anchored at line 1
// (the package clause), matching the import-boundary rule's format.
func depViolation(file, imp string, dep *confinedDep) Violation {
	return Violation{
		File:     file,
		Line:     1,
		Severity: Hard,
		Category: "dependency confinement",
		Reason: fmt.Sprintf(
			"imports %s — %s is owned by %s; %s",
			imp, dep.name, joinOwners(dep.owners), dep.why,
		),
	}
}

// confinedDepsForTest exposes the confinement table for tests (a defensive
// copy, so a test cannot mutate the package-level table). It is only used by
// tests in this package.
func confinedDepsForTest() []confinedDep {
	return append([]confinedDep(nil), confinedDeps...)
}
