package harnessapi

import (
	"strings"
	"testing"
)

func TestBuildPrompt_LapsEnabled(t *testing.T) {
	opts := RunOptions{
		LapsEnabled: true,
	}
	p := BuildPrompt(opts)

	if !strings.Contains(p, "laps done") {
		t.Error("expected prompt to contain 'laps done'")
	}
	if !strings.Contains(p, "laps handoff") {
		t.Error("expected prompt to contain 'laps handoff'")
	}
	// The shared finalize guidance now surfaces the laps wrapup reminder up
	// front, in addition to the hook-triggered reminder after laps done/handoff.
	if !strings.Contains(p, "laps wrapup") {
		t.Error("expected prompt to contain 'laps wrapup'")
	}
	if !strings.Contains(p, "already claimed") {
		t.Error("expected prompt to explain that Rally already claimed the lap")
	}
	if !strings.Contains(p, "laps done undo") {
		t.Error("expected prompt to mention laps done undo recovery")
	}
	if strings.Contains(p, "rally progress") {
		t.Error("expected prompt NOT to contain 'rally progress'")
	}
	if strings.Contains(p, "Header Context") {
		t.Error("expected prompt NOT to contain 'Header Context'")
	}
}

func TestBuildPromptDirectRunHasOutputContractWithoutProgressAction(t *testing.T) {
	prompt := BuildPrompt(RunOptions{
		DirectRun:        true,
		RoleInstructions: "Use laps handoff if blocked.",
		TaskPrompt:       "Inspect /tmp/input.md.",
	})

	for _, want := range []string{
		"## Direct Run Contract",
		"final assistant response is the only workflow output",
		"Do not invoke laps commands",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "rally progress --summary") {
		t.Fatalf("direct prompt contains relay progress action:\n%s", prompt)
	}
}

func TestBuildPromptUsesResolvedRallyReadmePath(t *testing.T) {
	prompt := BuildPrompt(RunOptions{RallyReadmePath: ".circuit/rally/README.md"})
	if !strings.Contains(prompt, "via `.circuit/rally/README.md`") {
		t.Fatalf("prompt does not use resolved Rally README path:\n%s", prompt)
	}
}

func TestBuildPrompt_NoBackend(t *testing.T) {
	opts := RunOptions{
		LapsEnabled: false,
	}
	p := BuildPrompt(opts)

	if !strings.Contains(p, "rally progress --summary") {
		t.Error("expected prompt to contain 'rally progress --summary'")
	}
	if strings.Contains(p, "rally progress --complete") {
		t.Error("expected prompt NOT to contain 'rally progress --complete'")
	}
	if strings.Contains(p, "laps done") {
		t.Error("expected prompt NOT to contain 'laps done'")
	}
	if strings.Contains(p, "Header Context") {
		t.Error("expected prompt NOT to contain 'Header Context'")
	}
}
