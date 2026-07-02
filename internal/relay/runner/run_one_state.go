package runner

import (
	"fmt"

	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func (r *Runner) newRunOneState(relay *store.RelayRecord, runIndex int, task runTask, consumedMsg *store.MessageRecord, relayMsg *store.MessageRecord) *runOneState {
	// Initialize run-state for this run.
	runID := fmt.Sprintf("relay-%d-run-%d", relay.ID, runIndex+1)
	rc := r.rallyContext(relay)
	summaryEntryCountBeforeRun := progressSummaryEntryCount(r.cfg.WorkspaceDir)
	_ = progress.SaveRunState(r.cfg.WorkspaceDir, newProgressRunState(runID, task.LapID))

	inbox := ""
	if consumedMsg != nil {
		inbox = consumedMsg.Body
	}
	relayMessage := ""
	if relayMsg != nil {
		relayMessage = relayMsg.Body
	}

	recentTryCount := r.cfg.RecentTryCount
	if recentTryCount <= 0 {
		recentTryCount = 5
	}
	recentTries := r.store.RecentTries(recentTryCount, relay.ID)
	recentContext := buildRecentContext(recentTries, r.cfg.RecentTryCharLimit, r.cfg.RecentContextCharLimit)

	return &runOneState{
		runID:                      runID,
		rc:                         rc,
		summaryEntryCountBeforeRun: summaryEntryCountBeforeRun,
		inbox:                      inbox,
		relayMessage:               relayMessage,
		recentContext:              recentContext,
		failureClass:               reliability.FailureAgent,
	}
}

func (r *Runner) captureRunStartWorkspaceState(state *runOneState, picked harnessapi.ResolvedAgent) {
	// Check for uncommitted non-rally changes at run start. Errors are
	// tolerated (treat as clean) so a broken git setup never crashes the run.
	state.leftoverWork, _ = gitx.IsWorkspaceDirty(r.cfg.WorkspaceDir)
	state.runStartDirtySnapshot, _ = gitx.WorkspaceDirtyPaths(r.cfg.WorkspaceDir)
	state.exec = r.executors[picked.Harness]
}
