package prompt

const scoutTemplate = `## Scout Mode
You are in scout mode. Explore the codebase and create tasks for future
agent sessions. Do NOT make code changes — your job is reconnaissance.
{{if .ScoutFocus}}
Focus area: {{.ScoutFocus}}
{{end}}
Look for:
- Bugs or error-handling gaps
- Missing or inadequate tests
- Naming inconsistencies or code organization issues
- Refactoring opportunities
- Small non-breaking features or improvements
{{if .BeadsEnabled}}
Create beads for each discovered task:
  bd create "short description of the task"
Set priority with -p (0 = highest):
  bd create -p 1 "less urgent task"
{{else}}
Record each discovered task as a markdown file under the task output path.
Use incrementing filenames (001-short-title.md, 002-short-title.md, etc.)
starting from the next unused number.

Each task file should contain:
- A one-line title
- A description of what needs to change and why
- Acceptance criteria as tickbox items: - [ ] criterion

Task output path: {{if .TaskOutputPath}}{{.TaskOutputPath}}{{else}}.rally/tasks/{{end}}
{{end}}`
