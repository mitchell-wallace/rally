package harnessapi

import (
	"fmt"
	"strings"

	"github.com/mitchell-wallace/rally/internal/agent_prompt"
)

func BuildPrompt(opts RunOptions) string {
	// An explicit prompt override wins outright: the reusable agent-prompt
	// template (general snippets, role slot, task context) is neither prepended
	// nor appended, preserving the existing executor prompt contract.
	if opts.Prompt != "" {
		return opts.Prompt
	}

	var b strings.Builder
	rolePolicy, requiredSkills := promptRolePolicy(opts)

	if opts.Persona != "" {
		fmt.Fprintf(&b, "Persona: %s\n\n", opts.Persona)
	}

	// Shared general/ guidance is reusable across every role and is always
	// included up front when rally drives the run via laps. It sits ahead of the
	// task context so the wrapup reminder is present up front (in addition to the
	// hook-triggered reminder after laps done/handoff in the exit-conditions
	// block below). The role slot itself is supplied via opts.RoleInstructions
	// (on-disk override or embedded default) and rendered with the task context.
	if opts.LapsEnabled {
		if hl := agent_prompt.Headless(); hl != "" {
			fmt.Fprintf(&b, "## Headless Operation\n%s\n\n", hl)
		}
		if fin := finalizeGuidanceForRole(rolePolicy); fin != "" {
			fmt.Fprintf(&b, "## Finalizing Your Work\n%s\n\n", fin)
		}
	}

	if opts.LeftoverWork {
		if lw := agent_prompt.LeftoverWork(); lw != "" {
			fmt.Fprintf(&b, "## Leftover Changes\n%s\n\n", lw)
		}
	}

	if opts.WorkspaceDir != "" {
		fmt.Fprintf(&b, "## Workspace\nWork in this repository: `%s`. Create and edit files in that repository, not in a scratch directory.\n\n", opts.WorkspaceDir)
	}

	if opts.TaskName != "" {
		fmt.Fprintf(&b, "Task: %s\n", opts.TaskName)
	}
	if opts.TaskRequirements != "" {
		fmt.Fprintf(&b, "Requirements:\n%s\n\n", opts.TaskRequirements)
	}

	if opts.Instructions != "" {
		fmt.Fprintf(&b, "## Project Instructions\n%s\n\n", opts.Instructions)
	}

	if opts.RoleInstructions != "" {
		fmt.Fprintf(&b, "## Role Instructions\n%s\n\n", opts.RoleInstructions)
	}

	if skillBlock := requiredSkillBlock(requiredSkills); skillBlock != "" {
		fmt.Fprintf(&b, "## Required Skills\n%s\n\n", skillBlock)
	}

	if opts.TaskPrompt != "" {
		fmt.Fprintf(&b, "## Task\n%s\n\n", opts.TaskPrompt)
	}

	if opts.RelayMessage != "" {
		fmt.Fprintf(&b, "Relay Message:\n%s\n\n", opts.RelayMessage)
	}

	if opts.InboxMessage != "" {
		fmt.Fprintf(&b, "Inbox Message:\n%s\n\n", opts.InboxMessage)
	}

	if opts.PreviousSummary != "" {
		fmt.Fprintf(&b, "Previous Summary:\n%s\n\n", opts.PreviousSummary)
	}

	if opts.RecentTryContext != "" {
		fmt.Fprintf(&b, "Recent Try Context:\n%s\n\n", opts.RecentTryContext)
	}

	if opts.LapsEnabled {
		fmt.Fprintf(&b, "## Run Exit Conditions\n")
		fmt.Fprintf(&b, "Laps is the task tracker for this outing. Rally has already claimed the current lap for you, so a bare `laps done` will mark that claimed lap complete.\n\n")
		fmt.Fprintf(&b, "These are shell commands. Invoke them via your shell/bash tool — do NOT echo the words as plain text in your response. The lap is only recorded when the command actually executes and the hook fires (you will see a follow-up instruction printed to stdout).\n\n")
		fmt.Fprintf(&b, "When you have finished the current lap, run this shell command:\n  laps done\n\n")
		switch rolePolicy {
		case RolePolicyReadOnlyGate:
			fmt.Fprintf(&b, "This is a read-only gate lap: do not use `laps handoff`. If follow-up implementation is needed, add the appropriate follow-up lap(s) and then run `laps done` for this gate lap.\n\n")
		case RolePolicyPlanOnly:
			fmt.Fprintf(&b, "This is a plan-only lap: commit planning or lap artifacts only. Source and test edits are out of scope; the revised plan is the deliverable. If implementation is needed, create or revise follow-up lap(s), then run `laps done` for this planning lap.\n\n")
		default:
			fmt.Fprintf(&b, "If you are blocked and cannot proceed, run this shell command:\n  laps handoff\n\n")
		}
		fmt.Fprintf(&b, "If laps reports that the wrong lap was claimed or completed, use the undo command it prints (`laps claim undo` or `laps done undo`) before continuing.\n\n")
		fmt.Fprintf(&b, "Follow any further instructions that command prints before ending the turn.\n\n")
		fmt.Fprintf(&b, "Do not exit the outing without actually executing the required shell command.\n")
	} else if opts.DirectRun {
		fmt.Fprintf(&b, `## Direct Run Contract
This is a standalone Rally run with no laps queue or relay progress record. The final assistant response is the only workflow output.

Do not invoke laps commands, rally progress, handoff commands, or queue bookkeeping. Any queue-oriented wording in role instructions does not apply to this direct run. Inspect the supplied input directly and return only the requested deliverable in the final response.
`)
	} else {
		fmt.Fprintf(&b, `## Run Exit Action
Before exiting, record your progress:
  rally progress --summary "<one-line summary>" --followup "<next task>"

Calling rally directly from the agent is the documented exception in no-backend mode.
`)
	}

	fmt.Fprintf(&b, "\nYou can access rally data and context via `.rally/README.md`.\n")

	return b.String()
}

// promptRolePolicy reads the role policy the caller resolved (the runner maps
// the internal/roles catalog into this contract vocabulary). BuildPrompt does
// no role-name interpretation of its own; an unset policy means plain
// implementation behaviour.
func promptRolePolicy(opts RunOptions) (RoleWritePolicy, []string) {
	policy := opts.RoleWritePolicy
	if policy == "" {
		policy = RolePolicyImplementation
	}
	return policy, append([]string(nil), opts.RoleRequiredSkills...)
}

func finalizeGuidanceForRole(rolePolicy RoleWritePolicy) string {
	switch rolePolicy {
	case RolePolicyReadOnlyGate:
		return ""
	case RolePolicyPlanOnly:
		return "When the plan is ready, commit only planning or lap artifacts. Do not edit source or tests for this lap; the revised plan is the deliverable."
	default:
		return agent_prompt.Finalize()
	}
}

func requiredSkillBlock(requiredSkills []string) string {
	if len(requiredSkills) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Load and follow these required skill(s) before starting: %s.\n", strings.Join(requiredSkills, ", "))
	fmt.Fprintf(&b, "If a required skill is unavailable, do not improvise its workflow: report the missing skill as this lap's outcome and add a follow-up lap to rerun once it is available.")
	return b.String()
}
