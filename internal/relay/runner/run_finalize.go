package runner

import (
	"context"
	"fmt"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

func (r *Runner) finalizeRunProgress(ctx context.Context, relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, task runTask, state *runOneState) {
	// Write stub entry if the agent did not finalize the run.
	stubSummary := ""
	if state.lastResult != nil {
		stubSummary = state.lastResult.Summary
	}
	wroteUnfinalized := false
	if state.resolvingOutcome != reliability.OutcomeCancelled {
		wroteUnfinalized, _ = r.maybeWriteStubAndClearState(stubSummary)
	}
	designedHandoffOutcome := state.resolvingOutcome == reliability.OutcomeRunTimeout ||
		state.resolvingOutcome == reliability.OutcomeHandoffTimeout ||
		state.resolvingOutcome == reliability.OutcomeHandoffRequested ||
		state.resolvingOutcome == reliability.OutcomeCancelled
	if wroteUnfinalized && !state.success && !designedHandoffOutcome && !state.runLapPinMismatch {
		// "agent exited without finalizing" is an operator-worthy recognized
		// failure — the agent process ended without `laps done`/`laps handoff`.
		// Categorize it as incomplete_finalization and carry run/runner/budget and
		// the last known attempt; this is not a provider-limit failure, so no
		// quota/reset fields attach. Apply the Priority-3 dirty_tree
		// FailureEvidence so the RallyFailure carries failure_evidence.source=
		// dirty_tree with a bounded raw_signal (changed paths) and message. Prefer
		// the classifier-produced evidence from the last attempt when it already
		// resolved to dirty_tree; otherwise build equivalent bounded changed-path
		// evidence from the dirty working tree.
		fs := telemetry.FailureState{
			Outcome:     string(reliability.OutcomeFailed),
			Category:    string(reliability.CategoryIncompleteFinalization),
			Attempt:     state.lastAttempt,
			MaxAttempts: state.maxAttempts,
			AgentState:  r.agentStateName(picked),
		}
		dirtyTreeEv := reliability.DirtyTreeEvidence(r.filesChangedList(nil, "", "", ""))
		if state.resetEvidence != nil && state.resetEvidence.Source == "dirty_tree" {
			dirtyTreeEv = state.resetEvidence
		}
		applyEvidenceToFailureState(&fs, dirtyTreeEv, "dirty_tree")
		r.tel().CaptureFailure(ctx, fmt.Sprintf("relay %d run %d: agent exited without finalizing", relay.ID, runIndex+1),
			failureStateEvent(telemetry.Tags(telemetry.EventInfo{
				RelayID:  relay.ID,
				RunID:    runIndex + 1,
				Role:     task.promptAssignee(),
				Harness:  picked.Harness,
				Model:    resolvedRunnerModel(state.lastResult, picked),
				Repo:     state.rc.Repo,
				RepoName: state.rc.RepoName,
				LapID:    task.LapID,
			}), state.rc, fs))
	}
}
