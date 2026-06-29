package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mitchell-wallace/rally/internal/agent_prompt"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/textutil"
	"github.com/mitchell-wallace/rally/internal/user_prompt/roleloader"
)

var headPullLap = func(ctx context.Context, workspaceDir string) (laps.Lap, error) {
	return (&laps.Adapter{WorkspaceDir: workspaceDir}).ClaimHead(ctx)
}

var queueSize = func(ctx context.Context, workspaceDir string) (int, error) {
	return (&laps.Adapter{WorkspaceDir: workspaceDir}).QueueSize(ctx)
}

const builtInDefaultFreeRunPrompt = "Continue the relay run. Review the current state of the codebase and continue making progress on the project."

const incompleteRetryGuidance = "The last run was incomplete. Check any current git changes, finish anything not done, verify correctness, commit when good, then run `laps done` for the claimed lap."

type runTask struct {
	Name              string
	Requirements      string
	Prompt            string
	Assignee          string
	EffectiveAssignee string
	ResolvedRoute     string
	LapID             string
	IsLapsBacked      bool
	LapsRemaining     int
}

func (t runTask) promptAssignee() string {
	if strings.TrimSpace(t.EffectiveAssignee) != "" {
		return t.EffectiveAssignee
	}
	return t.Assignee
}

func (r *Runner) resolveInstructions() string {
	if !r.cfg.LapsEnabled {
		return r.cfg.Instructions
	}
	if r.cfg.LapsInstructionsFile == "" {
		return r.cfg.Instructions
	}
	if r.lapsInstructionsCache != "" {
		return r.lapsInstructionsCache
	}
	data, err := os.ReadFile(r.cfg.LapsInstructionsFile)
	if err != nil {
		if !r.lapsWarned {
			fmt.Fprintf(os.Stderr, "warning: laps instructions file %q not readable: %v; using default\n", r.cfg.LapsInstructionsFile, err)
			r.lapsWarned = true
		}
		return r.cfg.Instructions
	}
	r.lapsInstructionsCache = string(data)
	return r.lapsInstructionsCache
}

func (r *Runner) loadFreeRunPrompt() string {
	if r.freeRunPromptCache != "" {
		return r.freeRunPromptCache
	}
	if r.cfg.FreeRunPromptFile != "" {
		data, err := os.ReadFile(r.cfg.FreeRunPromptFile)
		if err != nil {
			if !r.freeRunWarned {
				fmt.Fprintf(os.Stderr, "warning: free-run prompt file %q not readable: %v; using built-in default\n", r.cfg.FreeRunPromptFile, err)
				r.freeRunWarned = true
			}
			return builtInDefaultFreeRunPrompt
		}
		r.freeRunPromptCache = string(data)
		return r.freeRunPromptCache
	}
	return builtInDefaultFreeRunPrompt
}

func buildRecentContext(tries []store.TryRecord, perSummaryLimit, overallLimit int) string {
	var buf strings.Builder
	for _, t := range tries {
		summary := t.Summary
		if perSummaryLimit > 0 && len(summary) > perSummaryLimit {
			headSize := perSummaryLimit / 2
			tailSize := perSummaryLimit - headSize
			summary = summary[:headSize] + textutil.HeadTailTruncationMarker + summary[len(summary)-tailSize:]
		}
		fmt.Fprintf(&buf, "Run %d (%s): %s summary=%s\n", t.RunID, t.AgentType, recentContextStatus(t), summary)
	}
	if overallLimit > 0 && buf.Len() > overallLimit {
		result := buf.String()
		headSize := overallLimit / 2
		tailSize := overallLimit - headSize
		return result[:headSize] + textutil.HeadTailTruncationMarker + result[len(result)-tailSize:]
	}
	return buf.String()
}

func recentContextStatus(t store.TryRecord) string {
	if t.Outcome == "" {
		return fmt.Sprintf("completed=%v", t.Completed)
	}
	status := "outcome=" + string(t.Outcome)
	if t.Outcome == reliability.OutcomeCancelled && strings.TrimSpace(t.CancellationSource) != "" {
		status += " source=" + strings.TrimSpace(t.CancellationSource)
	}
	return status
}

var errQueueEmpty = errors.New("laps queue empty")

func (r *Runner) resolveRunTask(ctx context.Context) (runTask, error) {
	task := runTask{
		Name:   "relay run",
		Prompt: r.cfg.TaskPrompt,
	}

	if !r.cfg.LapsEnabled {
		if task.Prompt == "" {
			task.Prompt = r.loadFreeRunPrompt()
		}
		return task, nil
	}

	lap, err := headPullLap(ctx, r.cfg.WorkspaceDir)
	if err != nil {
		return runTask{}, fmt.Errorf("claim head lap: %w", err)
	}
	if lap == laps.NoLap {
		return runTask{}, errQueueEmpty
	}

	task.Name = lap.Title
	task.LapID = lap.ID
	task.IsLapsBacked = true
	if qs, err := queueSize(ctx, r.cfg.WorkspaceDir); err == nil {
		task.LapsRemaining = qs
	}
	if strings.TrimSpace(lap.Description) != "" {
		task.Prompt = lap.Description
	} else {
		task.Prompt = lap.Title
	}
	task.Assignee = lap.Assignee

	var details []string
	if lap.ID != "" {
		details = append(details, "Lap ID: "+lap.ID)
	}
	if lap.Assignee != "" {
		details = append(details, "Assignee: "+lap.Assignee)
	}
	task.Requirements = strings.Join(details, "\n")

	return task, nil
}

// resolveRoleInstructions fills the role slot of the composed agent prompt.
// An on-disk .rally/agents/<role>.md file overrides only this slot; when none
// exists, the embedded roles/<role>.md default is used. Either way the shared
// general/ finalize and headless guidance is added separately by BuildPrompt
// and is never suppressed by an on-disk override.
func (r *Runner) resolveRoleInstructions(assignee string) (string, error) {
	if !r.cfg.LapsEnabled || strings.TrimSpace(assignee) == "" {
		return "", nil
	}

	onDisk, err := roleloader.Loader{WorkspaceDir: r.cfg.WorkspaceDir}.Load(assignee)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(onDisk) != "" {
		return onDisk, nil
	}

	// No operator override — fall back to the embedded role default.
	if embedded, ok := agent_prompt.Role(assignee); ok {
		return embedded, nil
	}
	return "", nil
}
