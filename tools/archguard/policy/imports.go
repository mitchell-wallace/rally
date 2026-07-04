package policy

import (
	"fmt"
	"sort"
	"strings"
)

// Import-boundary rules (design.md Decision 4).
//
// v1 internal import-boundary rules are PRODUCTION-ONLY: `_test.go` files are
// skipped, so their current helper imports never become boundary violations.
// The discipline is layered:
//
//  1. Flagship deny edges — the architectural invariants from changes #1
//     (decompose-relay-runner) and #2 (slim-cli-composition-root) that this
//     checker exists to keep one-way. Each carries a specific, actionable
//     reason.
//  2. The cli deny-direction — nothing under internal/ may import internal/cli;
//     only cmd/rally (the process composition root) may.
//  3. Per-package production allow-lists — each internal package may import
//     only the internal packages in its allow-list (encoded from the current
//     graph). A package not listed is a leaf and may import no internal package.
//
// The allow-lists already cover the flagship edges (e.g. relay's allow-list is
// {harnessapi, store}, so runner/config/cli are all out); the flagship check
// exists only to raise a clearer reason for those invariants, so a given
// offending import produces exactly one violation — the most specific one.
//
// The harness-layer allow-lists (the contract, the process support package,
// each adapter, and the registry) come from modularize-harness-adapters
// Decision 8 and are deliberately tight: a harness package that reaches for
// relay/runtime/presentation is a confinement breach and raises the
// adapter-confinement diagnostic rather than the generic allow-list reason.
//
// The presentation boundary (separate-runtime-presentation-boundary Decision 7)
// splits the CLI rendering out of the runner: the runner emits events/controls
// through relay/runner/runtimeevent and concrete presentation packages render
// them. That adds three pieces of policy: (1) the runner drops style/keyboard
// from its allow-list (it now imports runtimeevent instead); (2) a new
// presentation/terminal allow-list row; and (3) two diagnostics that make the
// one-way intent explicit — a presentation deny-direction (nothing under
// internal/ may import a concrete presentation package except cli, which wires
// the adapter; shared presentation code such as tuicore is imported by concrete
// presentation adapters) and a presentation-confinement reason (a presentation
// package may not import runner internals, harness, config, or store).
// runtimeevent is a stdlib-only leaf, so it gets no allow-list row (absence =
// leaf, enforced).

// moduleInternalPrefix is the import-path prefix of every internal package.
// archguard is stdlib-only and dependency-free, so the module path is a compile
// constant rather than a runtime discovery.
const moduleInternalPrefix = "github.com/mitchell-wallace/rally/internal/"

// cmdRallyPackage is the process composition root (cmd/rally): it may import
// any internal package and is imported by nothing.
const cmdRallyPackage = "cmd/rally"

// internalName maps a repo-relative package directory (FileInfo.Package, e.g.
// "internal/relay/runner" or "cmd/rally") to its boundary-rule key: the part
// after "internal/" for internal packages, cmdRallyPackage for the composition
// root, and "" for anything the rule does not govern (e.g. tools/...). A ""
// result means "no boundary rule applies to this file".
func internalName(pkg string) string {
	if pkg == cmdRallyPackage {
		return cmdRallyPackage
	}
	if strings.HasPrefix(pkg, "internal/") {
		return strings.TrimPrefix(pkg, "internal/")
	}
	return ""
}

// importInternalName extracts the internal-package key from an import path
// (e.g. "github.com/mitchell-wallace/rally/internal/relay/runner" -> "relay/runner"),
// or "" if the import is not a rally internal package.
func importInternalName(imp string) string {
	if strings.HasPrefix(imp, moduleInternalPrefix) {
		return strings.TrimPrefix(imp, moduleInternalPrefix)
	}
	return ""
}

// denyEdge is a flagship architectural deny rule: a package (from) must not
// import another (to), with the given one-line reason. Matched on exact
// internal-name keys (so "relay" matches internal/relay, and "relay/runner"
// matches internal/relay/runner).
type denyEdge struct {
	from, to, reason string
}

// flagshipDeny lists the composition-root / relay invariants (Decision 4). The
// cli deny-direction is encoded separately because it applies to every internal
// package as the importer (a deny-by-target rule), not a single from->to edge.
var flagshipDeny = []denyEdge{
	{
		from:   "relay",
		to:     "relay/runner",
		reason: "the relay primitives must not depend on the orchestrator; keep the runner to relay edge one-way",
	},
	// relay/relay-runner must-not config/cli: two edges each.
	{from: "relay", to: "config", reason: "relay primitives must not depend on config; config belongs above the relay layer"},
	{from: "relay", to: "cli", reason: "relay primitives must not depend on the cli presentation layer"},
	{from: "relay/runner", to: "config", reason: "the runner must not depend on config; config is wired in at the app/cli composition root"},
	{from: "relay/runner", to: "cli", reason: "the runner must not depend on the cli presentation layer"},
	// release must-not app: breaks the metadata cycle.
	{from: "release", to: "app", reason: "release must not import app; keep runner to laps to release one-way so metadata does not cycle back into app"},
	// app must-not cli/user_prompt/laps: the presentation-neutral seam.
	{from: "app", to: "cli", reason: "app is presentation-neutral; it must not import the cli layer"},
	{from: "app", to: "user_prompt", reason: "app is presentation-neutral; it reaches user_prompt only transitively"},
	{from: "app", to: "laps", reason: "app is presentation-neutral; it reaches laps only transitively through runner"},
}

// allowList maps an internal package key to the internal packages it may
// import. A package absent from the map is a leaf and may import no internal
// package. cmd/rally is handled specially (it may import anything) rather than
// listed, since listing every package under "any" would be noise. internal/cli
// is intentionally absent: as the broad presentation layer it may import any
// internal package; the discipline on it is the reverse direction (the cli
// deny-by-target rule below).
//
// The map is encoded from the production graph at the design baseline and
// verified against HEAD (go list .Imports): it matches the current tree's
// direct internal imports, so the committed baseline is green by construction.
// The harness-layer rows (harnessapi, harness/process, the adapters, and the
// registry) follow modularize-harness-adapters Decision 8; the remaining rows
// are the original add-architecture-guardrails Decision 4 baseline with `agent`
// swapped to `harnessapi` in the consumers that now depend on the contract.
var allowList = map[string]map[string]bool{
	// Harness layer (Decision 8): the contract, process support, each adapter,
	// and the registry. These tight sets are what the adapter-confinement
	// invariant rests on — see isHarnessLayer / allowListDisallow.
	"harnessapi":          {"agent_prompt": true, "reliability": true, "textutil": true},
	"harness/process":     {"reliability": true},
	"harness/claude":      {"harnessapi": true, "harness/process": true, "reliability": true},
	"harness/codex":       {"harnessapi": true, "harness/process": true, "reliability": true},
	"harness/opencode":    {"harnessapi": true, "harness/process": true, "reliability": true},
	"harness/antigravity": {"harnessapi": true, "harness/process": true, "reliability": true},
	"harness/generic":     {"harnessapi": true, "harness/process": true, "reliability": true},
	"harness/fixture":     {"harnessapi": true},
	"harness":             {"harnessapi": true, "harness/antigravity": true, "harness/claude": true, "harness/codex": true, "harness/generic": true, "harness/opencode": true},
	// Consumers (agent -> harnessapi per Decision 8).
	"config":      {"harnessapi": true, "routing": true, "store": true},
	"routing":     {"harnessapi": true},
	"store":       {"reliability": true, "textutil": true},
	"reliability": {"monitor": true},
	"laps":        {"release": true},
	"progress":    {"laps": true, "store": true},
	"telemetry":   {"buildinfo": true},
	"release":     {"buildinfo": true},
	"relay":       {"harnessapi": true, "store": true},
	// Presentation boundary (Decision 7): the runner emits events/controls
	// through runtimeevent and no longer renders directly, so style/keyboard
	// leave its allow-list (monitor stays — Decision 8 residual).
	"relay/runner": {"harnessapi": true, "agent_prompt": true, "gitx": true, "laps": true, "monitor": true, "progress": true, "relay": true, "relay/runner/runtimeevent": true, "reliability": true, "routing": true, "store": true, "telemetry": true, "textutil": true, "user_prompt/roleloader": true},
	// Concrete presentation adapter (Decision 7): renders runtimeevent events
	// and translates keyboard presses to runtimeevent controls — nothing else
	// internal (notably not relay/runner, harness*, config, store, telemetry).
	"presentation/terminal": {"keyboard": true, "relay/runner/runtimeevent": true, "style": true},
	// TUI-prototype rows (build-new-tui prototypes); may be pruned when one
	// prototype is accepted.
	"presentation/tuicore":   {"relay/runner/runtimeevent": true, "style": true},
	"presentation/tuisafe":   {"presentation/tuicore": true, "relay/runner/runtimeevent": true, "style": true, "keyboard": true},
	"presentation/tuipanels": {"presentation/tuicore": true, "relay/runner/runtimeevent": true, "style": true, "keyboard": true},
	"presentation/tuitabs":   {"presentation/tuicore": true, "presentation/tuipanels": true, "relay/runner/runtimeevent": true, "style": true, "keyboard": true},
	"app":                    {"harnessapi": true, "harness": true, "config": true, "relay": true, "relay/runner": true, "relay/runner/runtimeevent": true, "routing": true, "store": true, "telemetry": true},
	"user_prompt/roleloader": {"store": true},
}

// ImportBoundary enforces the production internal import rules: flagship deny
// edges, the cli and presentation deny-directions, and per-package allow-lists.
// It skips test files (v1 boundaries are production-only).
type ImportBoundary struct{}

// NewImportBoundary builds the import-boundary rule. It is stateless today;
// the constructor exists so rules() reads uniformly and so future laps can
// extend it (e.g. with a regeneratable allow-list report) without changing the
// call sites.
func NewImportBoundary() *ImportBoundary { return &ImportBoundary{} }

// Name identifies the rule in diagnostics and tests.
func (*ImportBoundary) Name() string { return "import boundary" }

// Check walks every production file and raises a Hard "import boundary"
// violation for each internal import that violates a flagship edge, the cli
// deny-direction, the presentation deny-direction, or the importing package's
// allow-list. An offending import yields exactly one violation (the most
// specific reason), anchored at line 1 of the offending file.
//
// _test.go files are skipped deliberately: v1 internal boundaries are
// production-only. This exemption is load-bearing for the harness layer, whose
// tests legitimately reach across the production boundary to shared test
// fixtures and parser helpers (e.g. an adapter's _test.go importing
// internal/reliability's parsers or internal/harness/fixture), so the
// confinement invariant is enforced on the shipped code, not the test wiring.
func (*ImportBoundary) Check(files []FileInfo) []Violation {
	var vs []Violation
	for _, f := range files {
		if f.IsTest {
			continue // v1 internal boundaries are production-only (see above).
		}
		from := internalName(f.Package)
		if from == "" {
			continue // tools/..., fixtures, etc. — not governed.
		}
		for _, imp := range f.Imports {
			to := importInternalName(imp)
			if to == "" {
				continue // not a rally internal import.
			}
			if reason, ok := flagshipReason(from, to); ok {
				vs = append(vs, boundaryViolation(f.Path, imp, reason))
				continue
			}
			if reason, ok := cliDenyReason(from, to); ok {
				vs = append(vs, boundaryViolation(f.Path, imp, reason))
				continue
			}
			if reason, ok := presentationDenyReason(from, to); ok {
				vs = append(vs, boundaryViolation(f.Path, imp, reason))
				continue
			}
			if reason, ok := allowListReason(from, to); ok {
				vs = append(vs, boundaryViolation(f.Path, imp, reason))
				continue
			}
		}
	}
	return vs
}

// flagshipReason returns the reason if (from -> to) is a flagship deny edge.
func flagshipReason(from, to string) (string, bool) {
	for _, e := range flagshipDeny {
		if e.from == from && e.to == to {
			return e.reason, true
		}
	}
	return "", false
}

// cliDenyReason returns the reason if an internal package imports internal/cli.
// cmd/rally is exempt (it is the process composition root, not internal/).
func cliDenyReason(from, to string) (string, bool) {
	if to == "cli" && from != cmdRallyPackage {
		return "internal packages must not import internal/cli; only cmd/rally may depend on the presentation layer", true
	}
	return "", false
}

// presentationDenyReason returns the reason if an internal package outside the
// presentation layer imports a concrete presentation package (presentation/*).
// internal/cli is exempt: it wires adapters into the composition root.
// Presentation packages are exempt from the deny-direction so concrete
// adapters may import shared presentation code such as tuicore; their
// per-package allow-lists still govern those imports. The runner emits events
// and controls through runtimeevent and adapters render them, so other internal
// packages may not reach a concrete presentation package — that would re-couple
// the runner (or any peer) to a renderer and undo the boundary Decision 7 draws.
func presentationDenyReason(from, to string) (string, bool) {
	if !isPresentationLayer(to) {
		return "", false
	}
	if from == cmdRallyPackage || from == "cli" || isPresentationLayer(from) {
		return "", false
	}
	return "concrete presentation packages (presentation/*) may only be imported by internal/cli; the runner emits events and controls through relay/runner/runtimeevent and adapters render them", true
}

// allowListReason returns the reason if `to` is not permitted for `from`.
// cmd/rally (composition root) and internal/cli (broad presentation layer) may
// import any internal package, so they never fail this check.
func allowListReason(from, to string) (string, bool) {
	switch from {
	case cmdRallyPackage, "cli":
		return "", false // broad composition layers — allow any internal import.
	}
	if allow, ok := allowList[from]; ok {
		if allow[to] {
			return "", false
		}
		return allowListDisallow(from, to), true
	}
	// Leaf package (not in the map): it may import no internal package.
	return allowListDisallow(from, to), true
}

// isHarnessLayer reports whether the internal-name key is part of the harness
// layer: the contract (harnessapi), the process support package, any adapter
// (harness/<name>), or the top-level registry. These packages carry the
// adapter-confinement invariant (Decision 8): they must not depend on
// relay/runtime/presentation, so a disallowed import from one of them raises
// the architectural confinement reason instead of the generic allow-list one.
func isHarnessLayer(key string) bool {
	return key == "harnessapi" || key == "harness" || strings.HasPrefix(key, "harness/")
}

// isPresentationLayer reports whether the internal-name key is a concrete
// presentation adapter (presentation or presentation/<surface>). These packages
// carry the renderer-confinement invariant (Decision 7): they consume
// runtimeevent events/controls and render them, so they must not reach back
// into runner internals, harness, config, or store. A disallowed import from
// one of them raises the presentation-confinement reason instead of the
// generic allow-list one.
func isPresentationLayer(key string) bool {
	return key == "presentation" || strings.HasPrefix(key, "presentation/")
}

// allowListDisallow renders the allow-list violation reason for an import that
// the importing package's allow-list does not permit. A harness-layer package
// gets the architectural adapter-confinement reason (it must stay isolated from
// relay/runtime/presentation); a presentation package gets the renderer-
// confinement reason (it must stay isolated from runner internals/harness/
// config/store); every other package gets the generic reason that points at the
// exhaustive allow-list.
func allowListDisallow(from, to string) string {
	if isHarnessLayer(from) {
		return fmt.Sprintf(
			"imports internal/%s but internal/%s is confined to its tight harness-layer allow-list; harness adapters (and their contract/registry) must not depend on relay/runtime/presentation — they execute and return typed evidence",
			to, from,
		)
	}
	if isPresentationLayer(from) {
		return fmt.Sprintf(
			"imports internal/%s but internal/%s is confined to its tight presentation allow-list; presentation adapters must not import runner internals, harness, config, or store — the runner emits events and controls through relay/runner/runtimeevent and adapters render them",
			to, from,
		)
	}
	return fmt.Sprintf(
		"imports internal/%s but internal/%s may not depend on it; the per-package internal allow-list in design.md Decision 4 is exhaustive",
		to, from,
	)
}

// boundaryViolation builds the hard import-boundary finding for an offending
// import. The reason leads with the offending import path and appends the
// architectural explanation, so the diagnostic is self-explaining. It is
// anchored at line 1 (the package clause), matching the design's diagnostic
// format and the existing Violation.String rendering for boundary findings.
func boundaryViolation(file, imp, reason string) Violation {
	return Violation{
		File:     file,
		Line:     1,
		Severity: Hard,
		Category: "import boundary",
		Reason:   fmt.Sprintf("imports %s — %s", imp, reason),
	}
}

// denyEdgesForTest exposes the flagship deny edges for tests (sorted for stable
// iteration). It is only used by the test in this package.
func denyEdgesForTest() []denyEdge {
	out := append([]denyEdge(nil), flagshipDeny...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].from != out[j].from {
			return out[i].from < out[j].from
		}
		return out[i].to < out[j].to
	})
	return out
}
