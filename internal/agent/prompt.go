package agent

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
		if fin := agent_prompt.Finalize(); fin != "" {
			fmt.Fprintf(&b, "## Finalizing Your Work\n%s\n\n", fin)
		}
	}

	if opts.LeftoverWork {
		if lw := agent_prompt.LeftoverWork(); lw != "" {
			fmt.Fprintf(&b, "## Leftover Changes\n%s\n\n", lw)
		}
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
		fmt.Fprintf(&b, "Laps is the task tracker for this run. Rally has already claimed the current lap for you, so a bare `laps done` will mark that claimed lap complete.\n\n")
		fmt.Fprintf(&b, "These are shell commands. Invoke them via your shell/bash tool — do NOT echo the words \"laps done\" or \"laps handoff\" as plain text in your response. The lap is only recorded when the command actually executes and the hook fires (you will see a follow-up instruction printed to stdout).\n\n")
		fmt.Fprintf(&b, "When you have finished the current lap, run this shell command:\n  laps done\n\n")
		fmt.Fprintf(&b, "If you are blocked and cannot proceed, run this shell command:\n  laps handoff\n\n")
		fmt.Fprintf(&b, "If laps reports that the wrong lap was claimed or completed, use the undo command it prints (`laps claim undo` or `laps done undo`) before continuing.\n\n")
		fmt.Fprintf(&b, "Follow any further instructions that command prints before ending the turn.\n\n")
		fmt.Fprintf(&b, "Do not exit the run without actually executing one of the above as a shell command.\n")
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
