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
