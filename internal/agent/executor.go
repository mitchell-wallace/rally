package agent

import (
	"context"
	"strings"

	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/textutil"
)

const executorFinalTextRuneLimit = 1000

// boundedExecutorFinalText keeps unstructured final assistant text useful
// without allowing it to become a transcript-sized summary.
func boundedExecutorFinalText(text string) string {
	return textutil.TruncateHeadTailRunes(strings.TrimSpace(text), executorFinalTextRuneLimit)
}

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
	LeftoverWork     bool   // working tree has uncommitted non-rally changes
	Prompt           string // explicit override
	LogPath          string // path to write try transcript log
	ResumeSessionID  string // session-id to resume from a previous try
	WorkspaceDir     string // working directory for the agent process
	OnStart          func(pid int)
}

type TryResult struct {
	Completed        bool
	Summary          string
	RemainingWork    string
	MessageAddressed *bool
	FilesChanged     []string
	SessionID        string
	// ToolCalls is the count of tool-use invocations observed in the harness
	// transcript. Used to distinguish "agent did real work" from "agent only
	// emitted text" — a strong signal for the laps-marker-as-text failure.
	ToolCalls int
	// Evidence carries structured failure information populated by the
	// executor where it can observe structured/bounded error output. Nil
	// when the executor cannot determine failure details (e.g. process-level
	// harness_launch failures), in which case the runner-side fallback
	// parser in ClassifyError owns classification.
	Evidence *reliability.FailureEvidence
}

type Executor interface {
	Execute(ctx context.Context, opts RunOptions) (*TryResult, error)
	ResumeSupported() bool
	RotateSupported() bool
	LivenessProbeSupported() bool
	RotateModel(newModel string) error
	ProbeLiveness(ctx context.Context) (bool, error)
}
