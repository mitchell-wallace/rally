package harness

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
)

// Concrete adapter types are hidden behind the harnessapi.Executor contract, so
// these registry tests assert on observable properties — the map keys, each
// value's capability profile, and a resolved-model probe — rather than type
// asserting the concrete *<harness>.Executor (modularize-harness-adapters
// tasks.md 5.4). The file imports only the contract package, never the adapter
// subpackages, so it cannot reach into adapter internals.

// builtInProfile is the observable capability profile each built-in adapter
// reports through the harnessapi.Executor interface.
type builtInProfile struct {
	name                   string
	resumeSupported        bool
	rotateSupported        bool
	livenessProbeSupported bool
}

// The four built-in adapters keyed by canonical name, each tagged by its
// observable capability profile. opencode alone reports RotateSupported; codex
// alone reports LivenessProbeSupported; claude and antigravity share the
// resume-only profile and are told apart by the resolved-model probe in
// TestBuildExecutors_ModelDefaultsReachAdapter.
var builtInProfiles = []builtInProfile{
	{name: "antigravity", resumeSupported: true, rotateSupported: false, livenessProbeSupported: false},
	{name: "claude", resumeSupported: true, rotateSupported: false, livenessProbeSupported: false},
	{name: "codex", resumeSupported: true, rotateSupported: false, livenessProbeSupported: true},
	{name: "opencode", resumeSupported: true, rotateSupported: true, livenessProbeSupported: false},
}

// TestBuildExecutors_BuiltInCanonicalNames confirms the registry produces the
// four built-in adapters keyed by their canonical names, each carrying its
// observable capability profile through the harnessapi.Executor interface
// (tasks.md 5.4).
func TestBuildExecutors_BuiltInCanonicalNames(t *testing.T) {
	executors := BuildExecutors(Config{
		ClaudeModel:      "claude-default",
		CodexModel:       "codex-default",
		OpenCodeModel:    "opencode-default",
		AntigravityModel: "antigravity-default",
	})

	if want := len(builtInProfiles); len(executors) != want {
		t.Errorf("executor count = %d, want %d (built-ins only): %v", len(executors), want, sortedKeys(executors))
	}

	for _, p := range builtInProfiles {
		t.Run(p.name, func(t *testing.T) {
			ex, ok := executors[p.name]
			if !ok {
				t.Fatalf("missing built-in executor %q", p.name)
			}
			if got := ex.ResumeSupported(); got != p.resumeSupported {
				t.Errorf("%s.ResumeSupported() = %v, want %v", p.name, got, p.resumeSupported)
			}
			if got := ex.RotateSupported(); got != p.rotateSupported {
				t.Errorf("%s.RotateSupported() = %v, want %v", p.name, got, p.rotateSupported)
			}
			if got := ex.LivenessProbeSupported(); got != p.livenessProbeSupported {
				t.Errorf("%s.LivenessProbeSupported() = %v, want %v", p.name, got, p.livenessProbeSupported)
			}
		})
	}
}

// modelProbeCase drives one built-in adapter's resolved-model probe: the mock
// CLI binary name the adapter shells out to and a script body that emits a
// completed TryResult the adapter's own parser accepts. home marks adapters
// that read/write $HOME (antigravity's settings file).
type modelProbeCase struct {
	name    string // canonical adapter key
	model   string // distinct configured default used to spot cross-wiring
	binName string // mock binary name the adapter invokes
	script  string // mock binary body (without shebang)
	home    bool   // adapter touches $HOME
}

// TestBuildExecutors_ModelDefaultsReachAdapter confirms each built-in model
// default configured on Config reaches the adapter registered under its
// canonical name. It is observed through harnessapi.TryResult.ResolvedModel
// after a drive-by Execute against a mock CLI, never by inspecting the concrete
// adapter. Distinct defaults per adapter catch any cross-wiring (e.g.
// ClaudeModel handed to codex) (tasks.md 5.4).
func TestBuildExecutors_ModelDefaultsReachAdapter(t *testing.T) {
	cases := []modelProbeCase{
		{name: "claude", model: "claude-resolved-marker", binName: "claude", script: claudeProbeScript},
		{name: "codex", model: "codex-resolved-marker", binName: "codex", script: codexProbeScript},
		{name: "opencode", model: "opencode-resolved-marker", binName: "opencode", script: opencodeProbeScript},
		{name: "antigravity", model: "antigravity-resolved-marker", binName: "agy", script: antigravityProbeScript, home: true},
	}

	executors := BuildExecutors(Config{
		ClaudeModel:      "claude-resolved-marker",
		CodexModel:       "codex-resolved-marker",
		OpenCodeModel:    "opencode-resolved-marker",
		AntigravityModel: "antigravity-resolved-marker",
	})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ex, ok := executors[tc.name]
			if !ok {
				t.Fatalf("missing executor %q", tc.name)
			}
			binDir := stageMockBin(t, tc.binName, tc.script)
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			if tc.home {
				t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))
			}
			res, err := ex.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"})
			if err != nil {
				t.Fatalf("%s Execute failed: %v", tc.name, err)
			}
			if res.ResolvedModel != tc.model {
				t.Errorf("%s ResolvedModel = %q, want %q (configured model default did not reach the right adapter)",
					tc.name, res.ResolvedModel, tc.model)
			}
		})
	}
}

// TestBuildExecutors_GenericWithCommand confirms a Custom entry that declares a
// command registers a generic adapter — observed through its all-false
// capability profile, which is distinct from every built-in (all of which
// report ResumeSupported) — while a command-less Custom entry is skipped so the
// set and keys match the pre-change inline map (tasks.md 5.4).
func TestBuildExecutors_GenericWithCommand(t *testing.T) {
	executors := BuildExecutors(Config{
		Custom: map[string]GenericConfig{
			"mycli":      {Command: []string{"mycli", "run"}},
			"modelsonly": {Model: "ignored"}, // no Command: defines model aliases only
		},
	})

	ex, ok := executors["mycli"]
	if !ok {
		t.Fatal(`missing generic executor "mycli"`)
	}
	// The generic adapter reports no capability support — the observable
	// signature that distinguishes it from the built-ins.
	if ex.ResumeSupported() {
		t.Error("mycli.ResumeSupported() = true, want false (generic supports no capabilities)")
	}
	if ex.RotateSupported() {
		t.Error("mycli.RotateSupported() = true, want false")
	}
	if ex.LivenessProbeSupported() {
		t.Error("mycli.LivenessProbeSupported() = true, want false")
	}

	if _, ok := executors["modelsonly"]; ok {
		t.Error(`modelsonly (no command) should not register an executor`)
	}

	if want := len(builtInProfiles) + 1; len(executors) != want {
		t.Errorf("executor count = %d, want %d (4 built-ins + mycli): %v", len(executors), want, sortedKeys(executors))
	}
}

// Mock CLI bodies for the resolved-model probe. Each emits exactly the shape its
// adapter's parser accepts as a completed try; they mirror the proven stubs in
// each adapter's own TestExecutor_PopulateResolvedModel.
const (
	// claudeProbeScript emits the system + result JSON lines the claude parser
	// accepts as a completed try.
	claudeProbeScript = `printf '%s\n' '{"type":"system","session_id":"s"}'
printf '%s\n' '{"type":"result","result":{"completed":true,"summary":"ok"}}'
`
	// codexProbeScript emits a thread.started event and writes the result JSON
	// to the path following the -o flag, mirroring codex's --json report file.
	codexProbeScript = `printf '%s\n' '{"type":"thread.started","thread_id":"s"}'
next=0
for i in "$@"; do
  if [ "$next" = "1" ]; then printf '{"completed":true,"summary":"ok"}' > "$i"; break; fi
  if [ "$i" = "-o" ]; then next=1; fi
done
`
	// opencodeProbeScript emits the text-part event whose embedded JSON the
	// opencode parser decodes as a completed try.
	opencodeProbeScript = `printf '%s\n' '{"type":"text","part":{"type":"text","text":"{\"completed\":true,\"summary\":\"ok\"}"}}'
`
	// antigravityProbeScript emits a TryResult JSON line the antigravity parser
	// accepts in print mode.
	antigravityProbeScript = `printf '%s\n' '{"completed":true,"summary":"ok"}'
`
)

// stageMockBin writes an executable mock CLI named binName with the given script
// body into a fresh temp bin dir and returns that dir for PATH staging.
func stageMockBin(t *testing.T, binName, script string) string {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, binName), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binDir
}

func sortedKeys(m map[string]harnessapi.Executor) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
