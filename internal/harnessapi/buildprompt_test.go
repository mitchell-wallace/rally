package harnessapi

import (
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent_prompt"
)

func TestBuildPrompt_AllFields(t *testing.T) {
	opts := RunOptions{
		Persona:          "Expert Go developer",
		TaskName:         "Refactor store layer",
		TaskRequirements: "Use generics for JSONL records.",
		Instructions:     "Always write tests first.",
		TaskPrompt:       "Fix the caching bug.",
		InboxMessage:     "Urgent: fix race condition.",
		PreviousSummary:  "Added basic cache.",
		RecentTryContext: "Try #5 failed with timeout.",
	}
	p := BuildPrompt(opts)
	if p == "" {
		t.Fatal("expected non-empty prompt")
	}
	checks := []string{
		"Expert Go developer",
		"Refactor store layer",
		"Use generics for JSONL records.",
		"Always write tests first.",
		"## Project Instructions",
		"Fix the caching bug.",
		"## Task",
		"Urgent: fix race condition.",
		"Added basic cache.",
		"Try #5 failed with timeout.",
		".rally/README.md",
	}
	for _, c := range checks {
		if !strings.Contains(p, c) {
			t.Errorf("prompt missing %q", c)
		}
	}
}

func TestBuildPrompt_ExplicitOverride(t *testing.T) {
	opts := RunOptions{
		Prompt:  "CUSTOM PROMPT",
		Persona: "ignored",
	}
	p := BuildPrompt(opts)
	if p != "CUSTOM PROMPT" {
		t.Fatalf("expected explicit prompt, got %q", p)
	}
}

func TestBuildPrompt_PreviousSummary(t *testing.T) {
	opts := RunOptions{
		TaskName:        "Foo",
		PreviousSummary: "Bar",
	}
	p := BuildPrompt(opts)
	if !strings.Contains(p, "Previous Summary:") {
		t.Error("expected Previous Summary section")
	}
	if !strings.Contains(p, "Bar") {
		t.Error("expected summary text")
	}
}

func TestBuildPrompt_Instructions(t *testing.T) {
	opts := RunOptions{
		Instructions: "Always use TDD.",
	}
	p := BuildPrompt(opts)
	if !strings.Contains(p, "## Project Instructions") {
		t.Error("expected ## Project Instructions section")
	}
	if !strings.Contains(p, "Always use TDD.") {
		t.Error("expected instructions text")
	}
}

func TestBuildPrompt_RoleInstructionsBetweenProjectInstructionsAndTask(t *testing.T) {
	opts := RunOptions{
		Instructions:     "Base instructions.",
		RoleInstructions: "Role instructions.",
		TaskPrompt:       "Task body.",
	}
	p := BuildPrompt(opts)

	projectIndex := strings.Index(p, "## Project Instructions\nBase instructions.")
	roleIndex := strings.Index(p, "## Role Instructions\nRole instructions.")
	taskIndex := strings.Index(p, "## Task\nTask body.")
	if projectIndex == -1 || roleIndex == -1 || taskIndex == -1 {
		t.Fatalf("prompt missing expected sections:\n%s", p)
	}
	if !(projectIndex < roleIndex && roleIndex < taskIndex) {
		t.Fatalf("expected project instructions before role instructions before task, got:\n%s", p)
	}
}

func TestBuildPrompt_TaskPrompt(t *testing.T) {
	opts := RunOptions{
		TaskPrompt: "Fix the race condition.",
	}
	p := BuildPrompt(opts)
	if !strings.Contains(p, "## Task") {
		t.Error("expected ## Task section")
	}
	if !strings.Contains(p, "Fix the race condition.") {
		t.Error("expected task prompt text")
	}
}

func TestBuildPrompt_SharedGuidanceIncludedWhenLapsEnabled(t *testing.T) {
	opts := RunOptions{
		TaskName:         "Do the thing",
		RoleInstructions: "Role instructions.",
		LapsEnabled:      true,
	}
	p := BuildPrompt(opts)

	// The shared general/ snippets must always be composed into a laps-driven
	// agent prompt, sourced verbatim from the embedded agent_prompt package.
	if !strings.Contains(p, agent_prompt.Headless()) {
		t.Errorf("prompt missing shared headless guidance:\n%s", p)
	}
	if !strings.Contains(p, agent_prompt.Finalize()) {
		t.Errorf("prompt missing shared finalize guidance:\n%s", p)
	}
	// The role slot and existing task context survive alongside the snippets.
	if !strings.Contains(p, "## Role Instructions\nRole instructions.") {
		t.Errorf("prompt missing role slot:\n%s", p)
	}
	if !strings.Contains(p, "## Run Exit Conditions") {
		t.Errorf("prompt missing existing exit-conditions section:\n%s", p)
	}
}

func TestBuildPrompt_VerifyExitGuidanceOmitsHandoff(t *testing.T) {
	p := BuildPrompt(RunOptions{
		Role:             "verify",
		RoleInstructions: "Do not call `laps handoff`.",
		LapsEnabled:      true,
	})

	if strings.Contains(p, agent_prompt.Finalize()) {
		t.Fatalf("verify prompt should not include generic finalize handoff guidance:\n%s", p)
	}
	if strings.Contains(p, "If you are blocked and cannot proceed, run this shell command:\n  laps handoff") {
		t.Fatalf("verify prompt should not instruct blocked verify agents to hand off:\n%s", p)
	}
	if !strings.Contains(p, "For VERIFY work, do not use `laps handoff`") {
		t.Fatalf("verify prompt missing role-aware no-handoff guidance:\n%s", p)
	}
	if !strings.Contains(p, "laps done") {
		t.Fatalf("verify prompt still needs completion guidance:\n%s", p)
	}
}

func TestBuildPrompt_SharedGuidanceOmittedInNoBackendMode(t *testing.T) {
	opts := RunOptions{
		TaskName:    "Do the thing",
		LapsEnabled: false,
	}
	p := BuildPrompt(opts)

	// No-backend behavior is preserved: the laps-specific shared snippets are
	// not injected, and the documented `rally progress` exit action remains.
	if strings.Contains(p, agent_prompt.Finalize()) {
		t.Errorf("no-backend prompt should not include finalize guidance:\n%s", p)
	}
	if strings.Contains(p, agent_prompt.Headless()) {
		t.Errorf("no-backend prompt should not include headless guidance:\n%s", p)
	}
	if !strings.Contains(p, "rally progress --summary") {
		t.Errorf("no-backend prompt missing rally progress exit action:\n%s", p)
	}
}

func TestBuildPrompt_ExplicitOverrideSkipsSharedGuidance(t *testing.T) {
	opts := RunOptions{
		Prompt:      "CUSTOM PROMPT",
		LapsEnabled: true,
	}
	p := BuildPrompt(opts)
	if p != "CUSTOM PROMPT" {
		t.Fatalf("explicit override not preserved verbatim, got %q", p)
	}
}

func TestBuildPrompt_SharedGuidanceOrdering(t *testing.T) {
	opts := RunOptions{
		Persona:          "claude",
		TaskName:         "Do the thing",
		RoleInstructions: "Role instructions.",
		TaskPrompt:       "Task body.",
		LapsEnabled:      true,
	}
	p := BuildPrompt(opts)

	headlessIndex := strings.Index(p, agent_prompt.Headless())
	finalizeIndex := strings.Index(p, agent_prompt.Finalize())
	taskNameIndex := strings.Index(p, "Task: Do the thing")
	taskBodyIndex := strings.Index(p, "## Task\nTask body.")
	exitIndex := strings.Index(p, "## Run Exit Conditions")
	if headlessIndex == -1 || finalizeIndex == -1 || taskNameIndex == -1 || taskBodyIndex == -1 || exitIndex == -1 {
		t.Fatalf("prompt missing expected sections:\n%s", p)
	}

	// Reusable general snippets are appended ahead of the task context, and the
	// up-front finalize guidance precedes the exit-conditions block.
	if !(headlessIndex < taskNameIndex && finalizeIndex < taskNameIndex) {
		t.Fatalf("expected shared general snippets before task context:\n%s", p)
	}
	if !(finalizeIndex < exitIndex) {
		t.Fatalf("expected finalize wrapup guidance up front, before exit conditions:\n%s", p)
	}
}

func TestBuildPrompt_RecoveryClassificationOnlyFromRecoveryRole(t *testing.T) {
	recoveryRole, ok := agent_prompt.Role("recovery")
	if !ok {
		t.Fatal("missing recovery role")
	}
	recoveryPrompt := BuildPrompt(RunOptions{
		RoleInstructions: recoveryRole,
		LapsEnabled:      true,
	})
	if !strings.Contains(recoveryPrompt, "laps wrapup --classification <value>") {
		t.Fatalf("recovery prompt missing classification instruction:\n%s", recoveryPrompt)
	}

	for _, role := range []string{"junior", "senior", "ui", "verify"} {
		roleInstructions, ok := agent_prompt.Role(role)
		if !ok {
			t.Fatalf("missing %s role", role)
		}
		prompt := BuildPrompt(RunOptions{
			RoleInstructions: roleInstructions,
			LapsEnabled:      true,
		})
		for _, forbidden := range []string{"laps wrapup --classification", "course_correct", "repair_plan", "needs_user"} {
			if strings.Contains(prompt, forbidden) {
				t.Fatalf("%s prompt unexpectedly contains recovery classification marker %q:\n%s", role, forbidden, prompt)
			}
		}
	}
}

func TestTryResultSessionIDField(t *testing.T) {
	tr := &TryResult{Completed: true, Summary: "test", SessionID: "sess-123"}
	if tr.SessionID != "sess-123" {
		t.Errorf("SessionID = %q, want %q", tr.SessionID, "sess-123")
	}

	trZero := &TryResult{Completed: true}
	if trZero.SessionID != "" {
		t.Errorf("SessionID = %q, want empty string", trZero.SessionID)
	}
}
