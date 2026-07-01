package policy

import "fmt"

// Test-helper confinement (design.md: "Test-helper confinement" requirement).
//
// internal/testutil holds test-only helpers. Production code must not reach into
// it, or a test double leaks into the shipped binary. Unlike dependency
// confinement, this rule is PRODUCTION-ONLY: _test.go files may freely use
// testutil (that is its purpose). The rule fails any non-test file that imports
// internal/testutil.

// testutilImportPath is the import path of the internal test-helper package.
const testutilImportPath = moduleInternalPrefix + "testutil"

// TestHelperConfinement forbids non-test files from importing
// internal/testutil. Test files (_test.go) are exempt.
type TestHelperConfinement struct{}

// NewTestHelperConfinement builds the test-helper-confinement rule.
func NewTestHelperConfinement() *TestHelperConfinement { return &TestHelperConfinement{} }

// Name identifies the rule in diagnostics and tests.
func (*TestHelperConfinement) Name() string { return "test helper" }

// Check walks every non-test file and raises a Hard "test helper" violation for
// each import of internal/testutil. Test files are skipped, since testutil is
// meant for tests. Each offending import yields one violation at line 1.
func (*TestHelperConfinement) Check(files []FileInfo) []Violation {
	var vs []Violation
	for _, f := range files {
		if f.IsTest {
			continue // testutil is for tests; confinement is production-only.
		}
		for _, imp := range f.Imports {
			if !importPathHasPrefix(imp, testutilImportPath) {
				continue
			}
			vs = append(vs, Violation{
				File:     f.Path,
				Line:     1,
				Severity: Hard,
				Category: "test helper",
				Reason: fmt.Sprintf(
					"imports %s — test helpers must not leak into production code; only _test.go files may use internal/testutil",
					imp,
				),
			})
		}
	}
	return vs
}
