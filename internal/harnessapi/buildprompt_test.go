package harnessapi

import (
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent_prompt"
)

func TestBuildPrompt_AllFields(t *testing.T) {
	opts := RunOptions{
		Persona:          "Expert Go developer",
		WorkspaceDir:     "/tmp/rally-workspace",
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
		"## Workspace",
		"Work in this repository: `/tmp/rally-workspace`",
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
		RoleWritePolicy:  RolePolicyReadOnlyGate,
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

func TestBuildPrompt_VerifyPromptParity(t *testing.T) {
	opts := RunOptions{
		Persona:          "codex",
		WorkspaceDir:     "/repo",
		Role:             "verify",
		RoleWritePolicy:  RolePolicyReadOnlyGate,
		TaskName:         "Validate feature",
		TaskRequirements: "Run the acceptance checks.",
		Instructions:     "Project rules.",
		RoleInstructions: "Verify role rules.",
		TaskPrompt:       "Confirm behavior.",
		LapsEnabled:      true,
	}

	want := "Persona: codex\n\n" +
		"## Headless Operation\n" + agent_prompt.Headless() + "\n\n" +
		"## Workspace\nWork in this repository: `/repo`. Create and edit files in that repository, not in a scratch directory.\n\n" +
		"Task: Validate feature\n" +
		"Requirements:\nRun the acceptance checks.\n\n" +
		"## Project Instructions\nProject rules.\n\n" +
		"## Role Instructions\nVerify role rules.\n\n" +
		"## Task\nConfirm behavior.\n\n" +
		"## Run Exit Conditions\n" +
		"Laps is the task tracker for this run. Rally has already claimed the current lap for you, so a bare `laps done` will mark that claimed lap complete.\n\n" +
		"These are shell commands. Invoke them via your shell/bash tool — do NOT echo the words as plain text in your response. The lap is only recorded when the command actually executes and the hook fires (you will see a follow-up instruction printed to stdout).\n\n" +
		"When you have finished the current lap, run this shell command:\n  laps done\n\n" +
		"For VERIFY work, do not use `laps handoff`. If follow-up implementation is needed, add the appropriate follow-up lap(s) and then run `laps done` for this verification lap.\n\n" +
		"If laps reports that the wrong lap was claimed or completed, use the undo command it prints (`laps claim undo` or `laps done undo`) before continuing.\n\n" +
		"Follow any further instructions that command prints before ending the turn.\n\n" +
		"Do not exit the run without actually executing the required shell command.\n" +
		"\nYou can access rally data and context via `.rally/README.md`.\n"

	if got := BuildPrompt(opts); got != want {
		t.Fatalf("verify prompt changed.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestBuildPrompt_RolePolicyGuidance(t *testing.T) {
	tests := []struct {
		role            string
		policy          RoleWritePolicy
		requiredSkills  []string
		wantContains    []string
		wantNotContains []string
	}{
		{
			role:   "qa",
			policy: RolePolicyReadOnlyGate,
			wantContains: []string{
				"For VERIFY work, do not use `laps handoff`",
			},
			wantNotContains: []string{
				agent_prompt.Finalize(),
				"If you are blocked and cannot proceed, run this shell command:\n  laps handoff",
			},
		},
		{
			role:           "review",
			policy:         RolePolicyReadOnlyGate,
			requiredSkills: []string{"auto-code-review"},
			wantContains: []string{
				"For VERIFY work, do not use `laps handoff`",
				"Load and follow these required skill(s) before starting: auto-code-review.",
			},
			wantNotContains: []string{
				agent_prompt.Finalize(),
				"If you are blocked and cannot proceed, run this shell command:\n  laps handoff",
			},
		},
		{
			role:   "architect",
			policy: RolePolicyPlanOnly,
			wantContains: []string{
				"commit only planning or lap artifacts",
				"Source and test edits are out of scope",
				"the revised plan is the deliverable",
			},
			wantNotContains: []string{
				agent_prompt.Finalize(),
				"If you are blocked and cannot proceed, run this shell command:\n  laps handoff",
			},
		},
		{
			role:   "junior",
			policy: RolePolicyImplementation,
			wantContains: []string{
				agent_prompt.Finalize(),
				"If you are blocked and cannot proceed, run this shell command:\n  laps handoff",
			},
			wantNotContains: []string{
				"For VERIFY work, do not use `laps handoff`",
				"Required Skills",
				"the revised plan is the deliverable",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			p := BuildPrompt(RunOptions{
				Role:               tt.role,
				RoleWritePolicy:    tt.policy,
				RoleRequiredSkills: tt.requiredSkills,
				LapsEnabled:        true,
			})
			for _, want := range tt.wantContains {
				if !strings.Contains(p, want) {
					t.Fatalf("%s prompt missing %q:\n%s", tt.role, want, p)
				}
			}
			for _, forbidden := range tt.wantNotContains {
				if strings.Contains(p, forbidden) {
					t.Fatalf("%s prompt unexpectedly contains %q:\n%s", tt.role, forbidden, p)
				}
			}
		})
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

	for _, role := range []string{"junior", "senior", "verify"} {
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
