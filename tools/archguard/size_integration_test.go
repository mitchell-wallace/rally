package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/tools/archguard/policy"
)

// writeLinesFile writes a `.go` file at rel (under dir) with exactly n physical
// lines: a package clause followed by comment padding lines, so countLines
// reports n and the parser can read its imports. It returns the on-disk path.
func writeLinesFile(t *testing.T, dir, rel string, n int, pkg string) string {
	t.Helper()
	if n < 5 {
		t.Fatalf("writeLinesFile %s: n=%d too small for a valid go file", rel, n)
	}
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	var b strings.Builder
	// 5 fixed lines: package clause, blank, import, blank, and a `var _` line
	// that uses the import so the parser accepts the file; pad the rest with
	// comment lines to reach exactly n physical lines.
	lines := []string{
		"package " + pkg,
		"",
		`import "fmt"`,
		"",
		"var _ = fmt.Sprint",
	}
	for len(lines) < n {
		lines = append(lines, "// padding line")
	}
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	src := []byte(b.String())
	if c := countLines(src); c != n {
		t.Fatalf("writeLinesFile %s: wrote %d newlines, want %d", rel, c, n)
	}
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// runSize runs the size rule (the one registered in rules()) against a fixture
// dir via runAt, returning the exit code and stdout. It is the integration seam
// for the acceptance criteria: real walk + real policy + real exit code.
func runSize(t *testing.T, dir string, mode runMode) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runAt(dir, mode, &stdout, &stderr)
	return code, stdout.String()
}

// runSizeWithGrandfather is runSize but with an explicit size rule carrying a
// custom grandfather map, so a test can assert grandfather-over-cap semantics
// without touching the committed baseline.
func runSizeWithGrandfather(t *testing.T, dir string, mode runMode, gf map[string]int) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runWithRules(dir, mode, &stdout, &stderr, []policy.Rule{policy.NewSizeBudget(gf)})
	return code, stdout.String()
}

// TestSizeIntegrationCleanBaselinePasses builds a tiny tree whose single
// over-budget file is grandfathered at its current size, and asserts --ci exits
// zero (the green-by-construction contract). The temp path is not in the
// committed baseline, so we drive a size rule carrying a matching grandfather
// map — this is exactly what the committed baseline does for the real tree.
func TestSizeIntegrationCleanBaselinePasses(t *testing.T) {
	dir := t.TempDir()
	writeLinesFile(t, dir, "internal/x/big.go", 900, "x") // 900 > 800

	code, _ := runSizeWithGrandfather(t, dir, modeCI, map[string]int{"internal/x/big.go": 900})
	if code != 0 {
		t.Errorf("--ci on grandfathered-at-cap tree = %d, want 0", code)
	}
}

// TestSizeIntegrationNewOversizeFileFails is the spec scenario "New oversize
// file fails": a new 900-line production file NOT in the real repo grandfather
// map fails --ci with file + count + 800-line budget.
//
// We use a path that is deliberately NOT in the global grandfather map (a temp
// dir under the system temp root), so the committed baseline cannot accidentally
// exempt it.
func TestSizeIntegrationNewOversizeFileFails(t *testing.T) {
	dir := t.TempDir()
	writeLinesFile(t, dir, "internal/feature/new_big.go", 900, "feature")

	code, out := runSize(t, dir, modeCI)
	if code != 1 {
		t.Errorf("--ci = %d, want 1", code)
	}
	want := "internal/feature/new_big.go: size: 900 lines exceeds the 800-line production hard budget — split it or justify a grandfather entry"
	if !strings.Contains(out, want) {
		t.Errorf("--ci stdout missing diagnostic:\nwant %q\ngot  %q", want, out)
	}
}

// TestSizeIntegrationGrandfatheredOverCapFails proves a scratch one-line bump to
// a grandfathered file makes --ci fail reporting size vs cap. It does not mutate
// the real repo: it points the checker at a temp tree and registers a fresh
// grandfather map by exercising runAt through a helper that swaps the rule set.
func TestSizeIntegrationGrandfatheredOverCapFails(t *testing.T) {
	dir := t.TempDir()
	// Write a file at 900 lines but give it a 800 cap so it is over its cap and
	// also over the 800 production budget. We assert the diagnostic reports the
	// grandfather cap (size vs cap), not the standard budget — which is the
	// observable that distinguishes grandfather semantics.
	writeLinesFile(t, dir, "internal/relay/runner/sim.go", 901, "runner")

	code, out := runSizeWithGrandfather(t, dir, modeCI, map[string]int{
		"internal/relay/runner/sim.go": 900,
	})
	if code != 1 {
		t.Errorf("--ci = %d, want 1 (grandfathered file grew past its cap)", code)
	}
	want := "internal/relay/runner/sim.go: size: 901 lines exceeds grandfather cap 900 — split before growing this file; ratchet the cap down, never up"
	if !strings.Contains(out, want) {
		t.Errorf("--ci stdout missing size-vs-cap diagnostic:\nwant %q\ngot  %q", want, out)
	}
}

// TestSizeIntegrationWarningBandDoesNotFailCI is the spec scenario "Warning does
// not fail the build": a production file between 500 and 800 (not grandfathered)
// warns in default mode but --ci exits zero for it.
func TestSizeIntegrationWarningBandDoesNotFailCI(t *testing.T) {
	dir := t.TempDir()
	writeLinesFile(t, dir, "internal/store/medium.go", 600, "store")

	// Default mode prints the warning.
	defCode, defOut := runSize(t, dir, modeDefault)
	if defCode != 0 {
		t.Errorf("default mode = %d, want 0 (warning must not fail)", defCode)
	}
	if !strings.Contains(defOut, "internal/store/medium.go: size: 600 lines is over the warn budget") {
		t.Errorf("default stdout missing warning:\ngot %q", defOut)
	}
	// --ci also exits zero for a warning-only finding.
	ciCode, _ := runSize(t, dir, modeCI)
	if ciCode != 0 {
		t.Errorf("--ci = %d, want 0 (warnings never fail CI)", ciCode)
	}
}

// TestSizeIntegrationReportRegeneratesMap drives --report against an oversized
// temp file and confirms the paste-ready grandfather map appears with the
// file's actual line count, and that --report always exits zero.
func TestSizeIntegrationReportRegeneratesMap(t *testing.T) {
	dir := t.TempDir()
	writeLinesFile(t, dir, "internal/feature/huge.go", 950, "feature")

	code, out := runSize(t, dir, modeReport)
	if code != 0 {
		t.Errorf("--report = %d, want 0 (report always exits zero)", code)
	}
	if !strings.Contains(out, `"internal/feature/huge.go": 950,`) {
		t.Errorf("--report missing paste-ready map entry:\ngot %q", out)
	}
	if !strings.Contains(out, "Ratchet the cap down, never up.") {
		t.Errorf("--report missing ratchet header:\ngot %q", out)
	}
	if !strings.Contains(out, "var grandfather = map[string]int{") {
		t.Errorf("--report missing paste-ready var declaration:\ngot %q", out)
	}
	if strings.Contains(out, ": size:") {
		t.Errorf("--report should not duplicate over-hard size diagnostics outside the map:\n%s", out)
	}
}

// TestSizeIntegrationReportOmitsSizeWarnings confirms --report stays focused on
// baseline regeneration: warning-only size findings do not get appended after
// the grandfather map section.
func TestSizeIntegrationReportOmitsSizeWarnings(t *testing.T) {
	dir := t.TempDir()
	writeLinesFile(t, dir, "internal/store/medium.go", 600, "store")

	code, out := runSize(t, dir, modeReport)
	if code != 0 {
		t.Errorf("--report = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("--report should omit size warnings, got:\n%s", out)
	}
}

// TestSizeIntegrationGeneratedExempt confirms a generated file over the budget
// is skipped by the walk (exempt) and does not appear in --report or fail --ci.
func TestSizeIntegrationGeneratedExempt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "internal", "gen", "gen.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A generated file carrying the marker; make it well over the hard budget.
	var b strings.Builder
	b.WriteString("// Code generated by test. DO NOT EDIT.\n\npackage gen\n\nimport \"fmt\"\n\nvar _ = fmt.Sprint\n")
	for i := 0; i < 1000; i++ {
		b.WriteString("// padding\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	ciCode, ciOut := runSize(t, dir, modeCI)
	if ciCode != 0 {
		t.Errorf("--ci = %d, want 0 (generated is exempt)", ciCode)
	}
	if strings.Contains(ciOut, "internal/gen/gen.go") {
		t.Errorf("generated file should be exempt but appeared in output:\n%s", ciOut)
	}
}
