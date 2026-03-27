package prompt

// PromptData holds all values available to the prompt templates.
type PromptData struct {
	SessionID        int
	BatchID          int
	IterationIndex   int
	TargetIterations int
	Agent            string

	BeadsEnabled        bool
	ScoutMode           bool
	ScoutFocus          string
	ProjectInstructions string
	BatchMessages       []string
	SessionDirective    string
	RepoProgressPath    string
	TaskOutputPath      string // where scout writes tasks; empty = default
}

// HasWork reports whether the agent has been given explicit work to do
// (messages, beads, or a session directive). When false and not in scout
// mode, the base template renders a zero-config exploration fallback.
func (d PromptData) HasWork() bool {
	return len(d.BatchMessages) > 0 || d.SessionDirective != "" || d.BeadsEnabled
}
