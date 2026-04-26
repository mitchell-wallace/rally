package agent

import (
	"fmt"
	"strings"
)

const beadsTemplate = `## Task Source (Beads)
Use the beads CLI to find and manage your work.

1. Find work:    bd ready
2. Claim a task:  bd update <id> --claim
3. Do the work — make focused, well-tested changes.
4. Mark closed:   bd update <id> --status closed
5. Follow-ups:    bd create "description" for any remaining or discovered work.

Claim exactly one task per session. Complete it thoroughly before exiting.
If no tasks are ready, check if blocked tasks can be unblocked or look at
the batch context below for guidance.`

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

	if opts.BeadsEnabled {
		fmt.Fprintf(&b, "%s\n\n", beadsTemplate)
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

	fmt.Fprintf(&b, "You can access rally data and context via `.rally/README.md`.\n")

	return b.String()
}
