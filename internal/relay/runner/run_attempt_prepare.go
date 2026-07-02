package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/store"
)

func (r *Runner) setupRunBudget(state *runOneState, isHourlyRetry bool, isProbation bool) func() bool {
	maxAttempts := r.cfg.RetryBudget
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if isHourlyRetry {
		maxAttempts = relaycore.HourlyRetryMaxAttempts
	}
	if isProbation {
		maxAttempts = relaycore.HourlyRetryMaxAttempts
	}
	state.maxAttempts = maxAttempts

	// Per-run wall-clock budget across all retry attempts. Constructed ONCE here
	// (measured from run start), never inside the attempt loop, so a single timer
	// channel — passed into every runActionLoop invocation — measures cumulative
	// time across retries instead of resetting each attempt. A non-positive
	// budget leaves runBudgetCh nil, disabling the bound. The per-try cap is
	// created per attempt inside the loop (mirroring stallTicker).
	var stopRunBudget func() bool
	if r.cfg.RunTimeout > 0 {
		ch, stop := r.newBoundTimer(r.cfg.RunTimeout)
		state.runBudgetCh = ch
		state.runDeadline = time.Now().Add(r.cfg.RunTimeout)
		stopRunBudget = stop
	}
	state.tryTimeout = r.cfg.TryTimeout
	if r.cfg.RunTimeout > 0 && state.tryTimeout >= r.cfg.RunTimeout {
		// When the per-try cap is equal to or longer than the per-run budget, the
		// run budget subsumes it. Leaving both timers armed creates a select race
		// at the same deadline, which can incorrectly persist a retryable "try
		// timeout" instead of the run-budget handoff path.
		state.tryTimeout = 0
	}
	return stopRunBudget
}

func (r *Runner) prepareRunAttempt(ctx context.Context, relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, task runTask, state *runOneState, attempt int) (*runAttemptState, error) {
	if attempt > 1 {
		if state.exec != nil && state.exec.ResumeSupported() && state.sessionID != "" {
			rs, rsErr := progress.LoadRunState(r.cfg.WorkspaceDir)
			if rsErr == nil {
				rs.SessionID = state.sessionID
				_ = progress.SaveRunState(r.cfg.WorkspaceDir, rs)
			}
		} else {
			state.sessionID = ""
			_ = progress.SaveRunState(r.cfg.WorkspaceDir, newProgressRunState(state.runID, task.LapID))
		}
	}

	// Each try (attempt) is a child span of the run. NextTryID peeks the
	// id this attempt's record will be assigned at AppendTry below.
	tryID := r.store.NextTryID()
	tryCtx, trySpan := r.tel().StartSpan(ctx, "try", fmt.Sprintf("relay-%d-run-%d-try-%d", relay.ID, runIndex+1, tryID))

	opts := harnessapi.RunOptions{
		Persona:          picked.Harness,
		Model:            picked.Model,
		ReasoningEffort:  picked.ReasoningEffort,
		Role:             task.promptAssignee(),
		TaskName:         task.Name,
		TaskRequirements: task.Requirements,
		TaskPrompt:       task.Prompt,
		Instructions:     r.resolveInstructions(),
		RoleInstructions: state.roleInstructions,
		InboxMessage:     state.inbox,
		RelayMessage:     state.relayMessage,
		PreviousSummary:  state.previousSummary,
		RecentTryContext: state.recentContext,
		LapsEnabled:      r.cfg.LapsEnabled,
		LeftoverWork:     state.leftoverWork,
		ResumeSessionID:  state.sessionID,
		WorkspaceDir:     r.cfg.WorkspaceDir,
	}
	if state.lastAttemptIncomplete {
		if opts.TaskPrompt != "" {
			opts.TaskPrompt += "\n\n" + incompleteRetryGuidance
		} else {
			opts.TaskPrompt = incompleteRetryGuidance
		}
	}
	prompt := harnessapi.BuildPrompt(opts)

	taskPath := store.CurrentTaskPath(r.cfg.WorkspaceDir)
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		return nil, fmt.Errorf("create current_task.md dir: %w", err)
	}
	if err := os.WriteFile(taskPath, []byte(prompt), 0o644); err != nil {
		return nil, fmt.Errorf("write current_task.md: %w", err)
	}

	tryLogPath := filepath.Join(r.cfg.DataDir, "tries", repoKey(r.cfg.WorkspaceDir), fmt.Sprintf("try-%d.log", tryID))
	_ = os.MkdirAll(filepath.Dir(tryLogPath), 0o755)
	opts.LogPath = tryLogPath

	headBefore, _ := r.headHash()
	startedAt := time.Now().UTC()
	if state.runStartedAt.IsZero() {
		state.runStartedAt = startedAt
	}

	var lapsStarted, lapsTotal int
	if task.IsLapsBacked {
		// task.LapsRemaining is the current queue size including the claimed
		// head (claim pins but does not dequeue), so total = completed + queue.
		lapsStarted = runIndex + 1
		lapsTotal = runIndex + task.LapsRemaining
	}
	// Retries are surfaced inline as a `retry N/M` field on the live status
	// line (see mon.SetRetry below) rather than re-announcing the run with a
	// fresh header block per attempt.
	if attempt == 1 {
		displayRunIndex := runIndex
		if !task.IsLapsBacked && relay.CompletedIterations < relay.TargetIterations {
			displayRunIndex = relay.CompletedIterations
		}
		header := runtimeevent.RunHeaderReady{
			RunIndex:     displayRunIndex,
			TotalRuns:    relay.TargetIterations,
			AgentName:    picked.Harness,
			Attempt:      attempt,
			StartTime:    startedAt,
			IsLapsBacked: task.IsLapsBacked,
			LapTitle:     task.Name,
			LapsStarted:  lapsStarted,
			LapsTotal:    lapsTotal,
			Model:        picked.Model,
			RoleLabel:    task.Assignee,
		}
		r.eventSink().Emit(ctx, header)
	}

	if err := progress.SetActiveTry(r.cfg.WorkspaceDir, progress.ActiveTryMetadata{
		RelayID:   relay.ID,
		RunID:     runIndex + 1,
		TryID:     tryID,
		LogPath:   tryLogPath,
		StartedAt: startedAt,
	}); err != nil {
		trySpan.Finish()
		return nil, fmt.Errorf("set active try metadata: %w", err)
	}

	return &runAttemptState{
		attempt:    attempt,
		tryID:      tryID,
		tryCtx:     tryCtx,
		trySpan:    trySpan,
		opts:       opts,
		prompt:     prompt,
		tryLogPath: tryLogPath,
		headBefore: headBefore,
		startedAt:  startedAt,
	}, nil
}
