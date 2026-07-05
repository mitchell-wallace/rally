package runner

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

// classifyAttemptOutcome resolves whether the attempt failed, why, and how it
// should be recorded/routed. It is a table of named sub-steps; each helper
// preserves the original statement order, error strings, and telemetry fields.
func (r *Runner) classifyAttemptOutcome(relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, task runTask, state *runOneState, attempt *runAttemptState, log io.Writer) {
	r.classifyInitialFailure(task, state, attempt)
	detectLapsMarkerAsText(relay, runIndex, task, state, attempt, log)
	r.validatePinnedLapForAttempt(relay, runIndex, task, state, attempt, log)
	applyLapDoneRecovery(relay, runIndex, task, state, attempt, log)
	applyStallRecovery(relay, runIndex, task, state, attempt, log)
	r.resolveAttemptOutcomeAndResume(state, attempt)
	r.classifyErrorAndApplyStrategy(picked, state, attempt)
	reclassifyRunBudgetTimeout(state, attempt)
	reclassifyTryCapTimeout(state, attempt)
	state.lastAttemptIncomplete = attempt.failed && attempt.attemptFailureClass == reliability.FailureIncomplete
}

// classifyInitialFailure computes the raw failure flag and high-level reason
// for the attempt before any taxonomy or recovery adjustment is applied.
func (r *Runner) classifyInitialFailure(task runTask, state *runOneState, attempt *runAttemptState) {
	// Compute failed before rendering the footer so the displayed result
	// matches what gets recorded in the try record.
	attempt.failed = false
	state.failReason = ""
	attempt.attemptFailureClass = reliability.FailureAgent
	if attempt.timedOut {
		// A timeout takes precedence over the execErr/agent-error branches: the
		// cancelled attempt typically surfaces a context-cancelled execErr, but
		// that is a consequence of the timeout, not a harness fault. Classify it
		// as a non-freezing run_timeout attempt (see attemptOutcome override and
		// the classifier-bypass below) rather than "harness error".
		attempt.failed = true
		if attempt.runBudgetExhausted {
			state.failReason = "run timeout"
		} else {
			state.failReason = "try timeout"
		}
	} else if attempt.incomplete {
		attempt.failed = true
		state.failReason = reliability.CategoryDisplayLabel(reliability.CategoryIncompleteFinalization)
		attempt.attemptFailureClass = reliability.FailureIncomplete
	} else if attempt.execErr != nil {
		attempt.failed = true
		state.failReason = "harness error"
	} else if attempt.result == nil || !attempt.result.Completed {
		attempt.failed = true
		state.failReason = "agent error"
	} else {
		hasChanges := attempt.commitHash != "" || attempt.filesChangedCount > 0
		if !hasChanges {
			dirty, _ := gitx.IsWorkspaceDirty(r.cfg.WorkspaceDir)
			hasChanges = dirty
		}
		noFileChanges := !hasChanges
		exemptCompletedReadOnlyRole := roleWritePolicy(task.promptAssignee()) != harnessapi.RolePolicyImplementation && attempt.result.Completed
		if noFileChanges && attempt.runtime < 3*time.Minute && attempt.handoffEntry == nil && !exemptCompletedReadOnlyRole {
			attempt.failed = true
			state.failReason = "no changes made"
		}
	}
}

// detectLapsMarkerAsText flags agents that emit "laps done" / "laps handoff"
// as summary text instead of invoking the shell command.
func detectLapsMarkerAsText(relay *store.RelayRecord, runIndex int, task runTask, state *runOneState, attempt *runAttemptState, log io.Writer) {
	// Detect agents that emit "laps done" / "laps handoff" as text instead of
	// invoking the shell command. Symptom: the lap hooks never updated
	// RecordedLaps or HandoffState, yet the summary contains the literal marker.
	attempt.markerAsText = ""
	if task.IsLapsBacked && len(attempt.recordedLaps) == 0 && attempt.handoffState == 0 && attempt.result != nil {
		attempt.markerAsText = detectLapsMarkerInText(attempt.result.Summary)
		if attempt.markerAsText != "" {
			if !attempt.failed {
				attempt.failed = true
				state.failReason = fmt.Sprintf("%s emitted as text, hook never ran", attempt.markerAsText)
			}
			fmt.Fprintf(log, "relay %d run %d attempt %d laps-marker-as-text: agent wrote %q in summary but did not invoke the shell command (no hook fired, tool_calls=%d). Likely a model/harness output-vs-tool-call mismatch.\n", relay.ID, runIndex+1, attempt.attempt, attempt.markerAsText, attempt.result.ToolCalls)
		}
	}
}

// validatePinnedLapForAttempt records a lap-pin mismatch when the laps the
// agent recorded do not match the pinned lap for this task.
func (r *Runner) validatePinnedLapForAttempt(relay *store.RelayRecord, runIndex int, task runTask, state *runOneState, attempt *runAttemptState, log io.Writer) {
	attempt.lapPinMismatch = false
	if task.IsLapsBacked {
		if reason, mismatch := validatePinnedLap(task.LapID, attempt.recordedLaps); mismatch {
			state.failReason = reason
			attempt.lapPinMismatch = true
			state.runLapPinMismatch = true
			attempt.attemptFailureClass = reliability.FailureAgent
			state.failureClass = reliability.FailureAgent
			state.failureCategory = ""
			state.resetEvidence = nil
			attempt.failed = false
			if pinnedLapCompleteElsewhere(r.cfg.WorkspaceDir, state.runID, task.LapID, attempt.recordedLaps) {
				fmt.Fprintf(log, "relay %d run %d attempt %d lap pin mismatch warning: pinned_lap=%q consumed_laps=%v reason=%s pinned_lap_already_complete=true\n", relay.ID, runIndex+1, attempt.attempt, task.LapID, attempt.recordedLaps, reason)
			} else {
				fmt.Fprintf(log, "relay %d run %d attempt %d lap pin mismatch warning: pinned_lap=%q consumed_laps=%v reason=%s\n", relay.ID, runIndex+1, attempt.attempt, task.LapID, attempt.recordedLaps, reason)
			}
		}
	}
}

// applyLapDoneRecovery promotes a failed attempt to success when the laps hook
// recorded completion of the pinned lap during this attempt. The queue hook is
// authoritative for every role, including non-implementation roles such as
// verify/review/qa, so this intentionally does not use roleWritePolicy.
func applyLapDoneRecovery(relay *store.RelayRecord, runIndex int, task runTask, state *runOneState, attempt *runAttemptState, log io.Writer) {
	if !attempt.failed || !task.IsLapsBacked || attempt.lapPinMismatch {
		return
	}
	if !stringSliceContains(attempt.recordedLaps, task.LapID) {
		return
	}
	overriddenReason := state.failReason
	attempt.failed = false
	state.success = true
	state.failReason = ""
	fmt.Fprintf(log, "relay %d run %d attempt %d lap-done recovery: laps done hook fired for pinned lap %q; treating as success (was: %s)\n", relay.ID, runIndex+1, attempt.attempt, task.LapID, overriddenReason)
}

// applyStallRecovery promotes a stalled attempt to success when the agent
// committed files before idling. Non-implementation roles are excluded: a
// trivial commit is not evidence that gate, planning, or recovery work happened.
func applyStallRecovery(relay *store.RelayRecord, runIndex int, task runTask, state *runOneState, attempt *runAttemptState, log io.Writer) {
	// Stall recovery: if the stall detector killed the process but the agent had
	// already committed or created files (autoCommit ran), treat the try as
	// successful. This handles agents (e.g. opencode TUI) that complete the
	// task then idle in an interactive loop until the stall detector kills them.
	if attempt.failed && state.stallMarked && attempt.commitHash != "" && !attempt.lapPinMismatch {
		if roleWritePolicy(task.promptAssignee()) != harnessapi.RolePolicyImplementation {
			fmt.Fprintf(log, "relay %d run %d attempt %d stall recovery: files committed but assignee is %s, not treating as success\n", relay.ID, runIndex+1, attempt.attempt, task.Assignee)
		} else {
			attempt.failed = false
			state.success = true
			// Clear the pre-promotion failure reason so it does not persist on
			// the successful try record (the completed-with-"harness error"
			// corpus shape).
			state.failReason = ""
			fmt.Fprintf(log, "relay %d run %d attempt %d stall recovery: files committed, treating as success\n", relay.ID, runIndex+1, attempt.attempt)
		}
	}
}

// resolveAttemptOutcomeAndResume captures the resumable session id and derives
// the base attempt outcome, applying the timeout override.
func (r *Runner) resolveAttemptOutcomeAndResume(state *runOneState, attempt *runAttemptState) {
	// A run-budget exhaustion only yields a separate handoff-only continuation
	// when the harness can resume into the captured session. Capture this
	// attempt's session id (the cancelled attempt may still carry it) before
	// deciding. When no resumable session exists, the budget-cancelled
	// implementation try is itself the resolving try and is labelled
	// handoff_timeout (task 4.3) rather than run_timeout, so recovery routing
	// has a persisted resolving record even without a continuation.
	if attempt.result != nil && attempt.result.SessionID != "" {
		state.sessionID = attempt.result.SessionID
	}
	attempt.canHandoffResume = attempt.runBudgetExhausted && state.exec != nil && state.exec.ResumeSupported() && state.sessionID != ""

	hasFailureEvidence := attempt.result != nil && attempt.result.Evidence != nil && attempt.result.Evidence.Category != ""
	attempt.attemptOutcome = tryOutcomeForAttempt(attempt.failed, attempt.incomplete && !hasFailureEvidence, attempt.actionTaken && r.stopFlag.Load(), attempt.handoffEntry != nil)
	// A timed-out attempt (run budget or per-try cap) records a non-freezing
	// run_timeout outcome: it carries no FailureCategory (so the classifier
	// below is skipped), is not an Issue, and is not terminal by the outcome
	// itself. Whether the run stops (to hand off — task 4) or retries is
	// decided by runBudgetExhausted at the loop bottom, not by this outcome.
	// Guarded on `failed` so a stall-recovery success above is not relabelled.
	if attempt.timedOut && attempt.failed {
		attempt.attemptOutcome = reliability.OutcomeRunTimeout
		if attempt.runBudgetExhausted && !attempt.canHandoffResume {
			attempt.attemptOutcome = reliability.OutcomeHandoffTimeout
			state.failReason = noHandoffResumeReason(state.exec, state.sessionID)
		}
	}
}

// classifyErrorAndApplyStrategy runs reliability.ClassifyError over the
// failing attempt and applies the resulting strategy (no-op, rotate,
// wait/resume, fresh restart), terminal-category handling, and infra-failure
// accounting.
func (r *Runner) classifyErrorAndApplyStrategy(picked harnessapi.ResolvedAgent, state *runOneState, attempt *runAttemptState) {
	// Error classification and strategy dispatch.
	//
	// terminalCategory marks usage_limit / auth_or_proxy: categories whose
	// non-infra (agent-class) mapping means the freeze counter never bounds
	// them, so the attempt-loop break below is the only thing that bounds
	// them to a single attempt. On detection the loop terminates and control
	// returns to the routing dispatch loop, which benches the quota scope or
	// routes the entry away using the surfaced category + reset evidence.
	attempt.terminalForRun = attempt.attemptOutcome.IsTerminalForRun("")
	attempt.decisionEvidence = nil
	if attempt.failed && !attempt.lapPinMismatch && attempt.attemptOutcome.CarriesFailureCategory() {
		logLines := readLastNLines(attempt.tryLogPath, 50)
		decision := reliability.ClassifyError(logLines, picked.Harness, &reliability.ClassifyContext{
			HasFileChanges: attempt.incomplete,
			Finalized:      attempt.finalized,
			ChangedPaths:   attempt.filesChangedList,
		}, attempt.result.Evidence)
		attempt.decisionEvidence = decision.Evidence
		attempt.attemptFailureClass = decision.FailureClass
		state.failureClass = decision.FailureClass
		state.failureCategory = decision.Category
		state.resetEvidence = attempt.result.Evidence
		if state.resetEvidence == nil {
			state.resetEvidence = decision.Evidence
		}
		attempt.terminalForRun = attempt.attemptOutcome.IsTerminalForRun(decision.Category)
		if decision.FailureClass == reliability.FailureInfra {
			state.infraFailures++
		}
		if decision.Reason != "unknown error" && attempt.markerAsText == "" {
			state.failReason = formatCategorizedDisplay(state.failureCategory, decision.Cooldown, state.resetEvidence)
		}
		switch decision.Strategy {
		case reliability.StrategyNoOp:
			attempt.failed = false
			state.success = true
			attempt.attemptOutcome = tryOutcomeForAttempt(false, false, attempt.actionTaken && r.stopFlag.Load(), attempt.handoffEntry != nil)
			attempt.terminalForRun = false
			state.failureCategory = ""
		case reliability.StrategyRotate:
			r.skipFlag.Store(true)
		case reliability.StrategyWaitResume:
			// A terminal category never resumes within budget, so don't burn
			// the cooldown only to break the loop immediately afterward.
			if !attempt.terminalForRun && attempt.attempt < state.maxAttempts && decision.Cooldown > 0 {
				cooldown := decision.Cooldown
				if !state.runDeadline.IsZero() {
					remaining := time.Until(state.runDeadline)
					if remaining <= 0 {
						cooldown = 0
						attempt.runBudgetExhausted = true
					} else if cooldown >= remaining {
						cooldown = remaining
						attempt.runBudgetExhausted = true
					}
				}
				if cooldown > 0 {
					r.eventSink().Emit(context.Background(), runtimeevent.RateLimitWaitStarted{Wait: cooldown})
					if r.sleepFunc != nil {
						r.sleepFunc(cooldown)
					} else {
						time.Sleep(cooldown)
					}
				}
			}
		case reliability.StrategyFreshRestart:
			if attempt.attempt < state.maxAttempts {
				state.sessionID = ""
			}
		}
	} else {
		attempt.attemptFailureClass = attempt.attemptOutcome.FailureClass("")
		state.failureClass = attempt.attemptFailureClass
		if !attempt.attemptOutcome.CarriesFailureCategory() {
			state.failureCategory = ""
		}
	}
}

// reclassifyRunBudgetTimeout relabels a run-budget-exhausted failed attempt
// as a non-freezing run_timeout (or handoff_timeout when no resumable session
// exists), assigning the unidentified_issue floor when no category surfaced.
func reclassifyRunBudgetTimeout(state *runOneState, attempt *runAttemptState) {
	if attempt.failed && attempt.runBudgetExhausted {
		attempt.canHandoffResume = state.exec != nil && state.exec.ResumeSupported() && state.sessionID != ""
		attempt.attemptOutcome = reliability.OutcomeRunTimeout
		state.failReason = "run timeout"
		// A run-budget kill carries a non-empty category unless an
		// authoritative Category was already produced by the classifier
		// (decision.Category from the block above, e.g. a text-pattern
		// agent_error or dirty-tree incomplete_finalization) or by
		// executor/session/disk-log evidence. Empty = no telemetry signal,
		// so fall back to the non-freezing unidentified_issue floor.
		if attempt.runBudgetExhausted && state.failureCategory == "" && (attempt.result.Evidence == nil || attempt.result.Evidence.Category == "") {
			state.failureCategory = reliability.CategoryUnidentifiedIssue
		}
		attempt.attemptFailureClass = reliability.FailureAgent
		state.failureClass = attempt.attemptFailureClass
		attempt.terminalForRun = false
		if !attempt.canHandoffResume {
			attempt.attemptOutcome = reliability.OutcomeHandoffTimeout
			state.failReason = noHandoffResumeReason(state.exec, state.sessionID)
		}
	}
}

// reclassifyTryCapTimeout assigns the non-freezing agent-class
// unidentified_issue floor to a try-cap-only timeout that skipped the
// classifier, unless executor/session/disk-log evidence already produced a
// category.
func reclassifyTryCapTimeout(state *runOneState, attempt *runAttemptState) {
	// Try-cap-only kill (per-try deadline fired, run budget remains):
	// ClassifyError was skipped (attemptOutcome = OutcomeRunTimeout, whose
	// CarriesFailureCategory() is false), so failureCategory would
	// otherwise stay empty. Give it the non-freezing agent-class
	// unidentified_issue floor — unless executor/session/disk-log evidence
	// already produced an authoritative Category.
	if attempt.failed && attempt.timedOut && !attempt.runBudgetExhausted && state.failureCategory == "" && (attempt.result.Evidence == nil || attempt.result.Evidence.Category == "") {
		state.failureCategory = reliability.CategoryUnidentifiedIssue
		state.failReason = "try budget exhausted; no output"
		state.failureClass = reliability.FailureAgent
		attempt.attemptFailureClass = reliability.FailureAgent
	}
}
