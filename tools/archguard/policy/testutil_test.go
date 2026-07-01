package policy

import (
	"reflect"
	"strings"
	"testing"
)

// prodFile builds a non-test FileInfo in package pkg importing the given paths.
func prodFile(pkg string, imps ...string) FileInfo {
	return FileInfo{Path: pkg + "/helper.go", Package: pkg, IsTest: false, Imports: imps}
}

// testFile builds a _test.go FileInfo in package pkg importing the given paths.
func testFile(pkg string, imps ...string) FileInfo {
	return FileInfo{Path: pkg + "/helper_test.go", Package: pkg, IsTest: true, Imports: imps}
}

const testutilPath = "github.com/mitchell-wallace/rally/internal/testutil"

// TestTestutilProductionImportFails is the spec scenario "Production import of
// testutil fails": a non-test file importing internal/testutil hard-fails,
// naming the offending file, the import, and the reason.
func TestTestutilProductionImportFails(t *testing.T) {
	r := NewTestHelperConfinement()
	got := r.Check([]FileInfo{prodFile("internal/app", testutilPath)})
	want := []Violation{{
		File:     "internal/app/helper.go",
		Line:     1,
		Severity: Hard,
		Category: "test helper",
		Reason: "imports " + testutilPath +
			" — test helpers must not leak into production code; only _test.go files may use internal/testutil",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Check:\n got %+v\nwant %+v", got, want)
	}
	// The rendered diagnostic must name the offending file.
	rendered := got[0].String()
	for _, want := range []string{
		"internal/app/helper.go:1: test helper:",
		"imports " + testutilPath,
		"test helpers must not leak into production code",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered diagnostic missing %q\n got %q", want, rendered)
		}
	}
	if !HasHard(got) {
		t.Error("HasHard = false, want true (production testutil import must fail CI)")
	}
}

// TestTestutilTestFileAllowed confirms the rule is production-only: a _test.go
// file importing internal/testutil is NOT flagged (testutil is meant for tests).
func TestTestutilTestFileAllowed(t *testing.T) {
	r := NewTestHelperConfinement()
	got := r.Check([]FileInfo{testFile("internal/app", testutilPath)})
	if len(got) != 0 {
		t.Errorf("_test.go importing testutil must be allowed, got %+v", got)
	}
}

// TestTestutilSubpackageImportFlagged confirms an import of a testutil
// sub-package (internal/testutil/foo) is still flagged as a production leak.
func TestTestutilSubpackageImportFlagged(t *testing.T) {
	r := NewTestHelperConfinement()
	got := r.Check([]FileInfo{prodFile("internal/app", testutilPath+"/fixtures")})
	if len(got) != 1 || got[0].Severity != Hard {
		t.Fatalf("want one hard violation for a testutil sub-package import, got %+v", got)
	}
	if !strings.Contains(got[0].Reason, testutilPath+"/fixtures") {
		t.Errorf("Reason should name the offending sub-package import, got %q", got[0].Reason)
	}
}

// TestTestutilCleanFileNoFinding confirms a production file that does not import
// testutil produces no finding.
func TestTestutilCleanFileNoFinding(t *testing.T) {
	r := NewTestHelperConfinement()
	files := []FileInfo{
		prodFile("internal/app", "fmt", "github.com/mitchell-wallace/rally/internal/store"),
		prodFile("internal/agent"),
		testFile("internal/app", testutilPath), // test files never flagged
	}
	if got := r.Check(files); len(got) != 0 {
		t.Errorf("clean files must produce no finding, got %+v", got)
	}
}

// TestTestutilImportPathConstant pins the testutil import path the rule keys
// on, so the confinement target cannot drift from internal/testutil.
func TestTestutilImportPathConstant(t *testing.T) {
	if testutilImportPath != "github.com/mitchell-wallace/rally/internal/testutil" {
		t.Errorf("testutilImportPath = %q, want the internal/testutil path", testutilImportPath)
	}
}
