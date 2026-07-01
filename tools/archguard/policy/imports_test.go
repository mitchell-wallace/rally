package policy

import (
	"reflect"
	"strings"
	"testing"
)

// importFileInfo builds a production FileInfo for an importing package: path in
// pkg, Package set to "internal/"+pkg, and Imports set to the full import paths
// for the given internal sub-packages.
func importFileInfo(pkg string, imps ...string) FileInfo {
	full := make([]string, len(imps))
	for i, sub := range imps {
		full[i] = "github.com/mitchell-wallace/rally/internal/" + sub
	}
	return FileInfo{
		Path:    "internal/" + pkg + "/" + pkg + ".go",
		Package: "internal/" + pkg,
		IsTest:  false,
		Imports: full,
	}
}

// importPath renders the full import path for an internal sub-package, keeping
// the expected-reason strings in the tests readable.
func importPath(sub string) string {
	return "github.com/mitchell-wallace/rally/internal/" + sub
}

// TestImportBoundaryFlagshipRelayToRunner is the flagship acceptance: a
// production file in internal/relay importing internal/relay/runner hard-fails
// with the exact reason from design.md Decision 4 / the Diagnostics format.
func TestImportBoundaryFlagshipRelayToRunner(t *testing.T) {
	r := NewImportBoundary()
	got := r.Check([]FileInfo{importFileInfo("relay", "relay/runner")})
	want := []Violation{{
		File:     "internal/relay/relay.go",
		Line:     1,
		Severity: Hard,
		Category: "import boundary",
		Reason: "imports " + importPath("relay/runner") +
			" — the relay primitives must not depend on the orchestrator; keep the runner to relay edge one-way",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Check:\n got %+v\nwant %+v", got, want)
	}
	// The rendered diagnostic must name the offending file, the offending import,
	// the category, and the architectural reason.
	rendered := got[0].String()
	for _, want := range []string{
		"internal/relay/relay.go:1: import boundary:",
		"imports " + importPath("relay/runner"),
		"keep the runner to relay edge one-way",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered diagnostic missing %q\n got %q", want, rendered)
		}
	}
	if !HasHard(got) {
		t.Error("HasHard = false, want true (flagship edge must fail CI)")
	}
}

// TestImportBoundaryFlagshipEdges asserts every flagship deny edge hard-fails
// with its specific reason, so the invariants from changes #1 and #2 each have
// their own actionable diagnostic.
func TestImportBoundaryFlagshipEdges(t *testing.T) {
	cases := []struct {
		name   string
		from   string
		to     string
		reason string
	}{
		{"relay->runner", "relay", "relay/runner", "the relay primitives must not depend on the orchestrator; keep the runner to relay edge one-way"},
		{"relay->config", "relay", "config", "relay primitives must not depend on config; config belongs above the relay layer"},
		{"relay->cli", "relay", "cli", "relay primitives must not depend on the cli presentation layer"},
		{"runner->config", "relay/runner", "config", "the runner must not depend on config; config is wired in at the app/cli composition root"},
		{"runner->cli", "relay/runner", "cli", "the runner must not depend on the cli presentation layer"},
		{"release->app", "release", "app", "release must not import app; keep runner to laps to release one-way so metadata does not cycle back into app"},
		{"app->cli", "app", "cli", "app is presentation-neutral; it must not import the cli layer"},
		{"app->user_prompt", "app", "user_prompt", "app is presentation-neutral; it reaches user_prompt only transitively"},
		{"app->laps", "app", "laps", "app is presentation-neutral; it reaches laps only transitively through runner"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewImportBoundary()
			got := r.Check([]FileInfo{importFileInfo(c.from, c.to)})
			if len(got) != 1 {
				t.Fatalf("want 1 violation, got %d: %+v", len(got), got)
			}
			if got[0].Severity != Hard {
				t.Errorf("Severity = %v, want Hard", got[0].Severity)
			}
			if got[0].Category != "import boundary" {
				t.Errorf("Category = %q, want %q", got[0].Category, "import boundary")
			}
			if got[0].Line != 1 {
				t.Errorf("Line = %d, want 1", got[0].Line)
			}
			wantReason := "imports " + importPath(c.to) + " — " + c.reason
			if got[0].Reason != wantReason {
				t.Errorf("Reason:\n got %q\nwant %q", got[0].Reason, wantReason)
			}
		})
	}
}

// TestImportBoundaryFlagshipEdgesComplete pins that the table the rule ships
// with is exactly the flagship edge set from design.md Decision 4 — no edge
// silently dropped or duplicated, and no extra rule snuck in.
func TestImportBoundaryFlagshipEdgesComplete(t *testing.T) {
	want := []denyEdge{
		{"app", "cli", "app is presentation-neutral; it must not import the cli layer"},
		{"app", "laps", "app is presentation-neutral; it reaches laps only transitively through runner"},
		{"app", "user_prompt", "app is presentation-neutral; it reaches user_prompt only transitively"},
		{"relay", "cli", "relay primitives must not depend on the cli presentation layer"},
		{"relay", "config", "relay primitives must not depend on config; config belongs above the relay layer"},
		{"relay", "relay/runner", "the relay primitives must not depend on the orchestrator; keep the runner to relay edge one-way"},
		{"relay/runner", "cli", "the runner must not depend on the cli presentation layer"},
		{"relay/runner", "config", "the runner must not depend on config; config is wired in at the app/cli composition root"},
		{"release", "app", "release must not import app; keep runner to laps to release one-way so metadata does not cycle back into app"},
	}
	if got := denyEdgesForTest(); !reflect.DeepEqual(got, want) {
		t.Errorf("flagshipDeny table:\n got %+v\nwant %+v", got, want)
	}
}

// TestImportBoundaryAllowListMatchesDecision4 pins the per-package production
// allow-list to the OpenSpec Decision 4 table. This guards against accidentally
// broadening the current-graph baseline while the self-check below only proves
// that the encoded edges pass.
func TestImportBoundaryAllowListMatchesDecision4(t *testing.T) {
	want := map[string]map[string]bool{
		"agent":                  {"agent_prompt": true, "reliability": true, "textutil": true},
		"config":                 {"agent": true, "routing": true, "store": true},
		"routing":                {"agent": true},
		"store":                  {"reliability": true, "textutil": true},
		"reliability":            {"monitor": true},
		"laps":                   {"release": true},
		"progress":               {"laps": true, "store": true},
		"telemetry":              {"buildinfo": true},
		"release":                {"buildinfo": true},
		"user_prompt/roleloader": {"store": true},
		"relay":                  {"agent": true, "store": true},
		"relay/runner":           {"agent": true, "agent_prompt": true, "gitx": true, "keyboard": true, "laps": true, "monitor": true, "progress": true, "relay": true, "reliability": true, "routing": true, "store": true, "style": true, "telemetry": true, "textutil": true, "user_prompt/roleloader": true},
		"app":                    {"agent": true, "config": true, "relay": true, "relay/runner": true, "routing": true, "store": true, "telemetry": true},
	}
	if !reflect.DeepEqual(allowList, want) {
		t.Errorf("allowList:\n got %+v\nwant %+v", allowList, want)
	}
}

// TestImportBoundaryCLIDenyDirection confirms the no-internal-imports-cli rule:
// any internal package importing internal/cli hard-fails, but cmd/rally (the
// process composition root) is exempt.
func TestImportBoundaryCLIDenyDirection(t *testing.T) {
	r := NewImportBoundary()
	// An internal package importing cli fails with the cli-specific reason.
	got := r.Check([]FileInfo{importFileInfo("store", "cli")})
	if len(got) != 1 || got[0].Severity != Hard {
		t.Fatalf("want one hard violation, got %+v", got)
	}
	wantReason := "imports " + importPath("cli") +
		" — internal packages must not import internal/cli; only cmd/rally may depend on the presentation layer"
	if got[0].Reason != wantReason {
		t.Errorf("Reason:\n got %q\nwant %q", got[0].Reason, wantReason)
	}

	// cmd/rally importing cli is fine: it is the composition root.
	cmdFile := FileInfo{
		Path:    "cmd/rally/main.go",
		Package: "cmd/rally",
		Imports: []string{importPath("cli"), importPath("release")},
	}
	if got := r.Check([]FileInfo{cmdFile}); len(got) != 0 {
		t.Errorf("cmd/rally importing cli must be allowed, got %+v", got)
	}
}

// TestImportBoundaryAllowListRejectsDisallowedImport confirms a non-flagship,
// non-cli import that the package's allow-list does not permit still fails with
// the generic allow-list reason.
func TestImportBoundaryAllowListRejectsDisallowedImport(t *testing.T) {
	r := NewImportBoundary()
	// store may import {reliability, textutil}; importing agent is disallowed.
	got := r.Check([]FileInfo{importFileInfo("store", "agent")})
	if len(got) != 1 || got[0].Severity != Hard {
		t.Fatalf("want one hard violation, got %+v", got)
	}
	if got[0].Category != "import boundary" {
		t.Errorf("Category = %q, want import boundary", got[0].Category)
	}
	wantReason := "imports " + importPath("agent") +
		" — imports internal/agent but internal/store may not depend on it; the per-package internal allow-list in design.md Decision 4 is exhaustive"
	if got[0].Reason != wantReason {
		t.Errorf("Reason:\n got %q\nwant %q", got[0].Reason, wantReason)
	}
}

// TestImportBoundaryLeafPackageRejectsAnyInternal confirms a leaf package (one
// with no internal imports in the current graph, e.g. textutil) may not import
// any internal package.
func TestImportBoundaryLeafPackageRejectsAnyInternal(t *testing.T) {
	r := NewImportBoundary()
	got := r.Check([]FileInfo{importFileInfo("textutil", "store")})
	if len(got) != 1 || got[0].Severity != Hard {
		t.Fatalf("want one hard violation, got %+v", got)
	}
	if !strings.Contains(got[0].Reason, importPath("store")) {
		t.Errorf("Reason should name the offending import, got %q", got[0].Reason)
	}
}

// TestImportBoundaryAllowedImportsPass walks the full current production graph
// and confirms every recorded allow-list edge passes (the green-by-construction
// contract). This is the encoding-vs-reality check that the allow-list matches
// the tree the rule was generated from.
func TestImportBoundaryAllowedImportsPass(t *testing.T) {
	r := NewImportBoundary()
	var files []FileInfo
	for from, tos := range allowList {
		var imps []string
		for to := range tos {
			imps = append(imps, to)
		}
		files = append(files, importFileInfo(from, imps...))
	}
	// Also exercise the broad composition layers importing anything they really
	// import today, to confirm they are exempt from the allow-list.
	files = append(files, FileInfo{
		Path: "cmd/rally/main.go", Package: "cmd/rally",
		Imports: []string{importPath("cli"), importPath("release")},
	})
	files = append(files, FileInfo{
		Path: "internal/cli/root.go", Package: "internal/cli",
		Imports: []string{importPath("app"), importPath("laps"), importPath("store")},
	})
	if got := r.Check(files); len(got) != 0 {
		t.Errorf("every allow-listed edge must pass, got violations: %+v", got)
	}
}

// TestImportBoundarySkipsTestFiles confirms the v1 rule is production-only: a
// _test.go file's internal helper imports never become boundary violations,
// even when they would be disallowed in production.
func TestImportBoundarySkipsTestFiles(t *testing.T) {
	r := NewImportBoundary()
	testFile := FileInfo{
		Path:    "internal/relay/relay_test.go",
		Package: "internal/relay",
		IsTest:  true,
		Imports: []string{importPath("relay/runner"), importPath("cli"), importPath("config")},
	}
	if got := r.Check([]FileInfo{testFile}); len(got) != 0 {
		t.Errorf("test files must be skipped by v1 boundary rule, got %+v", got)
	}
}

// TestImportBoundaryIgnoresUngovernedAndExternal confirms the rule only flags
// rally-internal imports: external imports and files outside internal/ + cmd
// (e.g. the archguard tool itself) produce no findings.
func TestImportBoundaryIgnoresUngovernedAndExternal(t *testing.T) {
	r := NewImportBoundary()
	files := []FileInfo{
		// A production internal package that imports only stdlib + its own
		// allow-listed internal peer.
		importFileInfo("store", "reliability"),
		// A tools/ file (not governed) importing the policy package.
		{Path: "tools/archguard/main.go", Package: "tools/archguard", Imports: []string{
			"fmt", "github.com/mitchell-wallace/rally/tools/archguard/policy",
		}},
	}
	if got := r.Check(files); len(got) != 0 {
		t.Errorf("external and ungoverned imports must be ignored, got %+v", got)
	}
}

// TestImportBoundaryOneViolationPerImport confirms an offending import yields
// exactly one violation even when multiple rules could match (flagship takes
// precedence over the cli deny-direction and the allow-list, so relay->cli is
// reported once with the flagship reason).
func TestImportBoundaryOneViolationPerImport(t *testing.T) {
	r := NewImportBoundary()
	// relay importing cli matches BOTH a flagship edge and the cli deny-
	// direction; only the flagship reason should fire.
	got := r.Check([]FileInfo{importFileInfo("relay", "cli")})
	if len(got) != 1 {
		t.Fatalf("want 1 violation, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Reason, "relay primitives must not depend on the cli") {
		t.Errorf("want the flagship reason, got %q", got[0].Reason)
	}
}
