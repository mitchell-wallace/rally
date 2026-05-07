package agent

import "context"

type ResolvedAgent struct {
	Harness string
	Model   string
}

type RunOptions struct {
	Persona          string
	Model            string
	TaskName         string
	TaskRequirements string
	Instructions     string
	RoleInstructions string
	TaskPrompt       string
	InboxMessage     string
	RelayMessage     string
	PreviousSummary  string
	RecentTryContext string
	LapsEnabled      bool
	Prompt           string // explicit override
	LogPath          string // path to write try transcript log
	OnStart          func(pid int)
}

type TryResult struct {
	Completed        bool
	Summary          string
	RemainingWork    string
	MessageAddressed *bool
	FilesChanged     []string
	SessionID        string
}

type Executor interface {
	Execute(ctx context.Context, opts RunOptions) (*TryResult, error)
	ResumeSupported() bool
	RotateSupported() bool
	LivenessProbeSupported() bool
	CharsPerToken() float64
	RotateModel(newModel string) error
	ProbeLiveness(ctx context.Context) (bool, error)
}
