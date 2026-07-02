package runner

import (
	"context"
	"io"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/store"
)

func (r *Runner) runHandoffContinuation(ctx context.Context, relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, task runTask, state *runOneState, log io.Writer) error {
	// Bounded handoff-only continuation (task 4). The run budget was exhausted on
	// a resume-capable harness with a captured session: resume that session once
	// under HandoffTimeout (no stall detector, not counted against the run budget)
	// to capture a clean handoff. This continuation, not the cancelled run_timeout
	// implementation try, resolves the run.
	if state.handoffResumePending && !r.stopFlag.Load() && ctx.Err() == nil {
		contOutcome, contResult, contSucceeded, contDirtyHandoff, contErr := r.runBoundedHandoffOnly(
			ctx, relay, runIndex, picked, task, state.rc, state.roleInstructions,
			state.handoffResumeSessionID, state.handoffResumeBaseAttempt+1, state.maxAttempts,
			state.summaryEntryCountBeforeRun, state.runID, state.runStartDirtySnapshot, log,
		)
		if contErr != nil {
			return contErr
		}
		state.resolvingOutcome = contOutcome
		state.resolvingDirtyHandoff = contDirtyHandoff
		if contResult != nil {
			state.lastResult = contResult
		}
		if contSucceeded {
			state.success = true
		}
	}
	return nil
}
