package agent

import "context"

type RunOptions struct {
	Persona          string
	TaskName         string
	TaskRequirements string
	InboxMessage     string
	PreviousSummary  string
	RecentTryContext string
	Prompt           string // explicit override
}

type TryResult struct {
	Completed        bool
	Summary          string
	RemainingWork    string
	MessageAddressed *bool
	FilesChanged     []string
}

type Executor interface {
	Execute(ctx context.Context, opts RunOptions) (*TryResult, error)
}
