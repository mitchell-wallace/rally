// Command archguard is a dependency-free architecture checker for the rally
// repository. It walks the repo, parses each `.go` file's imports, counts its
// physical lines, and runs a policy engine that enforces file-size budgets,
// internal import boundaries, and third-party dependency confinement.
//
// It is a dev tool, not part of the rally binary: it lives in the main module
// (so `go vet`, `go test`, and gofmt already cover it) but is never built into
// cmd/rally or shipped by GoReleaser, and it imports only the Go standard
// library so `go mod tidy` stays a no-op.
//
// Run modes:
//
//	archguard            default: print warnings and hard violations; exit
//	                     non-zero if any hard violation exists.
//	archguard --report   print the paste-ready grandfather baseline plus any
//	                     import/dependency violations; always exit zero (it is
//	                     used to regenerate the committed baseline).
//	archguard --ci       print warnings and hard violations, but exit non-zero
//	                     only on hard violations (warnings never fail CI).
//
// The size-budget rule (with its grandfathered baseline) is registered; the
// import-boundary, dependency-confinement, and testutil rules land in later
// laps. Until then, `--report` prints the size grandfather map and the other
// sections remain empty.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mitchell-wallace/rally/tools/archguard/policy"
)

// rules returns the policy rules the engine enforces, in evaluation order. The
// size-budget rule is the first concrete rule: later laps append the import-
// boundary, dependency-confinement, and testutil rules.
func rules() []policy.Rule {
	return []policy.Rule{
		policy.NewSizeBudget(grandfather),
		policy.NewImportBoundary(),
	}
}

// runMode selects the output/exit behaviour.
type runMode int

const (
	modeDefault runMode = iota
	modeReport
	modeCI
)

func main() {
	os.Exit(Main(os.Args[1:], os.Stdout, os.Stderr))
}

// Main is the testable entry point: it parses flags, locates the repo root, and
// dispatches to the selected mode. It returns the process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("archguard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	report := fs.Bool("report", false, "print the paste-ready grandfather baseline and any import/dep violations; always exits zero")
	ci := fs.Bool("ci", false, "exit non-zero only on hard violations; warnings print but never fail")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *report && *ci {
		fmt.Fprintln(stderr, "archguard: --report and --ci are mutually exclusive")
		return 2
	}

	mode := modeDefault
	switch {
	case *report:
		mode = modeReport
	case *ci:
		mode = modeCI
	}

	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(stderr, "archguard: %v\n", err)
		return 2
	}
	return runAt(root, mode, stdout, stderr)
}

// runAt scans root, runs the policy engine, prints per the mode, and returns
// the exit code. Separated from Main so tests can drive it against a fixture
// tree without depending on the working directory.
func runAt(root string, mode runMode, stdout, stderr io.Writer) int {
	return runWithRules(root, mode, stdout, stderr, rules())
}

// runWithRules is runAt with an explicit rule set, so tests can drive the full
// scan->policy->print->exit flow with a size rule carrying a custom grandfather
// map (the real `runAt` always uses the committed baseline).
func runWithRules(root string, mode runMode, stdout, stderr io.Writer, rs []policy.Rule) int {
	files, err := scanRepo(root)
	if err != nil {
		fmt.Fprintf(stderr, "archguard: scan: %v\n", err)
		return 2
	}

	engine := policy.NewEngine(rs...)
	violations := engine.Check(files)

	if mode == modeReport {
		printReport(stdout, engine, files, violations)
		return 0
	}

	printViolations(stdout, violations)
	// Default and --ci both fail only on hard violations; warnings are advisory.
	if policy.HasHard(violations) {
		return 1
	}
	return 0
}

// printViolations writes each violation on its own line. With no violations it
// prints nothing, keeping a clean tree quiet.
func printViolations(w io.Writer, vs []policy.Violation) {
	for _, v := range vs {
		fmt.Fprintln(w, v.String())
	}
}

// printReport writes the paste-ready reporter sections (e.g. the regeneratable
// grandfather map) followed by any violations, for use in regenerating the
// committed baseline.
func printReport(w io.Writer, engine *policy.Engine, files []policy.FileInfo, vs []policy.Violation) {
	for _, section := range engine.Reports(files) {
		fmt.Fprintln(w, section)
	}
	printViolations(w, vs)
}

// findRepoRoot walks up from the working directory to the module root (the
// directory containing go.mod), so archguard scans the whole repo regardless of
// where it is invoked from.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from working directory")
		}
		dir = parent
	}
}
