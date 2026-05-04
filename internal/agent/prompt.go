package agent

import (
	"fmt"
	"strings"
)

func BuildPrompt(opts RunOptions) string {
	if opts.Prompt != "" {
		return opts.Prompt
	}

	var b strings.Builder

	if opts.Persona != "" {
		fmt.Fprintf(&b, "Persona: %s\n\n", opts.Persona)
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
		fmt.Fprintf(&b, `## Run Exit Conditions
When you have finished the current lap, mark it done:
  laps done

If you are blocked and cannot proceed, signal a handoff:
  laps handoff

Do not exit the run without calling one of the above.
`)
	} else {
		fmt.Fprintf(&b, `## Run Exit Action
Before exiting, record your progress:
  rally progress --complete --summary "<one-line summary>" --followup "<next task>"

Calling rally directly from the agent is the documented exception in no-backend mode.
`)
	}

	fmt.Fprintf(&b, "\nYou can access rally data and context via `.rally/README.md`.\n")

	return b.String()
}
