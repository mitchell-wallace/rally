package prompt

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
