package agent

import "context"

type RunOptions struct {
	Persona          string
	TaskName         string
	TaskRequirements string
	Instructions     string
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
}

type Executor interface {
	Execute(ctx context.Context, opts RunOptions) (*TryResult, error)
}
