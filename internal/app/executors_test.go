package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
)

// app.BuildExecutors is a thin config -> harness.Config mapper that delegates to
// harness.BuildExecutors. These tests assert on observable properties — the
// executor set/keys parity and the generic adapter's Execute-visible behaviour
// — rather than type asserting the concrete adapter, matching the registry
// tests' approach (modularize-harness-adapters tasks.md 5.4).

// TestBuildExecutors_Parity confirms app.BuildExecutors still yields the same
// executor set and keys the pre-change inline map produced for a representative
// config: the four built-in canonical names plus one generic adapter per harness
// entry that declares a command, with command-less entries skipped (tasks.md
// 5.4). The generic adapter is identified by its all-false capability profile,
// not by its concrete type.
func TestBuildExecutors_Parity(t *testing.T) {
	emptyFlag := ""
	cfg := config.V2Config{
		ClaudeModel:      "claude-default",
		CodexModel:       "codex-default",
		OpenCodeModel:    "opencode-default",
		AntigravityModel: "antigravity-default",
		Harnesses: map[string]*config.HarnessConfig{
			"mycli": {
				Command:        []string{"mycli", "run"},
				ModelFlag:      &emptyFlag,
				OutputStrategy: "tail",
				OutputLines:    7,
				TailStream:     "stdout",
			},
			"modelsonly": {
				// No Command: defines model aliases only; no executor registered.
			},
		},
	}

	executors := BuildExecutors(cfg)

	for _, name := range []string{"antigravity", "claude", "codex", "opencode"} {
		if _, ok := executors[name]; !ok {
			t.Errorf("missing built-in executor %q", name)
		}
	}

	g, ok := executors["mycli"]
	if !ok {
		t.Fatal(`missing generic executor "mycli"`)
	}
	// The generic adapter reports no capability support — the observable
	// signature that distinguishes it from the built-ins.
	if g.ResumeSupported() || g.RotateSupported() || g.LivenessProbeSupported() {
		t.Errorf("mycli capability profile = (resume=%v, rotate=%v, liveness=%v), want all false (generic)",
			g.ResumeSupported(), g.RotateSupported(), g.LivenessProbeSupported())
	}

	if _, ok := executors["modelsonly"]; ok {
		t.Error(`modelsonly (no command) should not register an executor`)
	}

	if want := 5; len(executors) != want {
		t.Errorf("executor count = %d, want %d (4 built-ins + mycli): %v", len(executors), want, executorKeys(executors))
	}
}

// TestBuildExecutors_PreservesModelFlagSemantics confirms the config ->
// harness.Config mapper preserves ModelFlag absent (nil) versus empty-string
// pointer semantics end to end. The difference is observable in the generic
// adapter's argv: an empty-string flag appends the resolved model positionally,
// while a nil flag passes no model at all (tasks.md 5.4).
func TestBuildExecutors_PreservesModelFlagSemantics(t *testing.T) {
	cfg := config.V2Config{
		Harnesses: map[string]*config.HarnessConfig{
			"absent": {Command: []string{"absent"}},                       // ModelFlag nil
			"empty":  {Command: []string{"empty"}, ModelFlag: strPtr("")}, // ModelFlag = empty string
		},
	}

	executors := BuildExecutors(cfg)
	binDir := stageEchoBin(t, "absent", "empty")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// A per-try model is resolved via opts.Model (GenericConfig.Model is empty
	// through the mapper), so the flag's nil-vs-empty distinction is visible.
	absentRes, err := executors["absent"].Execute(context.Background(), harnessapi.RunOptions{Model: "droid-v1"})
	if err != nil {
		t.Fatalf("absent Execute failed: %v", err)
	}
	if strings.Contains(absentRes.Summary, "droid-v1") {
		t.Errorf("absent (nil ModelFlag): model leaked into argv %q, want no model appended", absentRes.Summary)
	}

	emptyRes, err := executors["empty"].Execute(context.Background(), harnessapi.RunOptions{Model: "droid-v1"})
	if err != nil {
		t.Fatalf("empty Execute failed: %v", err)
	}
	if !strings.Contains(emptyRes.Summary, "<droid-v1>") {
		t.Errorf("empty (empty-string ModelFlag): expected positional model in argv, got %q", emptyRes.Summary)
	}
}

// TestBuildExecutors_GenericModelStaysEmpty confirms the mapper leaves
// GenericConfig.Model empty because the current config shape has no
// generic-harness default-model field. The observable consequence: with no
// per-try model and a non-empty ModelFlag, no model and no flag token reach the
// argv (had the mapper injected a default model, the flag + default would
// appear) (tasks.md 5.4).
func TestBuildExecutors_GenericModelStaysEmpty(t *testing.T) {
	modelFlag := "--model"
	cfg := config.V2Config{
		Harnesses: map[string]*config.HarnessConfig{
			"flagged": {Command: []string{"flagged", "run"}, ModelFlag: &modelFlag},
		},
	}

	executors := BuildExecutors(cfg)
	binDir := stageEchoBin(t, "flagged")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	res, err := executors["flagged"].Execute(context.Background(), harnessapi.RunOptions{})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	// No per-try model and no injected default: neither the flag nor any model
	// token reaches the argv. Had the mapper injected a GenericConfig.Model
	// default, "<--model>" plus that default would appear here.
	if strings.Contains(res.Summary, "<--model>") {
		t.Errorf("expected no model flag in argv (no model resolved), got %q", res.Summary)
	}
}

func strPtr(s string) *string { return &s }

func executorKeys(m map[string]harnessapi.Executor) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// stageEchoBin writes executable mock CLIs that echo each argv element wrapped
// in <...> into a fresh temp bin dir and returns that dir for PATH staging. The
// echoed tokens land in the generic adapter's tailed summary, making the
// constructed argv observable through the interface.
func stageEchoBin(t *testing.T, binNames ...string) string {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const script = `for arg in "$@"; do printf '<%s>\n' "$arg"; done`
	for _, name := range binNames {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return binDir
}
