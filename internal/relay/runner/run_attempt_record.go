package runner

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

// recordAttemptOutcome renders the attempt footer, assembles and persists the
// try record, emits the try telemetry, and updates resolving run state. It is a
// table of named sub-steps; each helper preserves the original statement order,
// error strings, and telemetry fields.
func (r *Runner) recordAttemptOutcome(relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, task runTask, state *runOneState, attempt *runAttemptState, log io.Writer) error {
	r.emitAttemptOutcomeFooter(state, attempt)
	tryRecord := buildTryRecord(relay, runIndex, picked, task, state, attempt)
	fmt.Fprintf(log, "relay %d run %d attempt %d result: completed=%v outcome=%q fail_reason=%q runtime=%s files_changed=%d tool_calls=%d commit=%q lap_id=%q assignee=%q recorded_laps=%v laps_attempted=%v handoff_state=%d\n",
		relay.ID, runIndex+1, attempt.attempt, !attempt.failed, attempt.attemptOutcome, state.failReason, attempt.runtime, attempt.filesChangedCount, tryRecord.ToolCalls, attempt.shortHash, task.LapID, task.Assignee, attempt.recordedLaps, attempt.lapsAttempted, attempt.handoffState)

	tryLogFields := r.emitAttemptTelemetry(relay, runIndex, picked, task, state, attempt, tryRecord)
	attempt.trySpan.Finish()

	state.resolvingOutcome = attempt.attemptOutcome
	state.resolvingDirtyHandoff = attempt.dirtyHandoff
	if err := r.store.AppendTry(tryRecord); err != nil {
		return err
	}
	if err := progress.ClearActiveTry(r.cfg.WorkspaceDir); err != nil {
		return fmt.Errorf("clear active try metadata: %w", err)
	}
	r.tel().EmitTryLog(attempt.tryCtx, tryLogFields)
	return nil
}

// emitAttemptOutcomeFooter renders the coloured attempt footer: an interim
// (neutral) line for an in-budget retry, or a terminal footer (green on
// success, red when the budget is exhausted or the run breaks out).
func (r *Runner) emitAttemptOutcomeFooter(state *runOneState, attempt *runAttemptState) {
	// A failing attempt that will be retried within budget is not a terminal
	// outcome: it gets the neutral, in-place retry line rather than a red
	// footer. Exactly one coloured footer prints when the run resolves —
	// green on success, red when the budget is exhausted (or the run breaks
	// out via skip/stop/lap-pin mismatch/terminal category). A single-attempt
	// run is terminal on its first failure, so it colours immediately.
	willRetry := attempt.failed && attempt.attempt < state.maxAttempts &&
		!attempt.actionTaken && !r.skipFlag.Load() && !attempt.lapPinMismatch && !r.stopFlag.Load() &&
		!attempt.terminalForRun && !attempt.runBudgetExhausted
	footerDuration := attempt.runtime
	if !willRetry {
		footerDuration = attempt.runRuntime
	}
	outcomeFooter := runtimeevent.FooterData{
		Passed:       !attempt.failed,
		Duration:     footerDuration,
		FilesChanged: attempt.filesChangedCount,
		CommitHash:   attempt.shortHash,
		CommitTitle:  attempt.commitTitle,
		FailReason:   state.failReason,
		Interim:      willRetry,
		Attempt:      attempt.attempt,
		MaxAttempts:  state.maxAttempts,
	}
	r.emitAttemptFooter(context.Background(), outcomeFooter)
}

// buildTryRecord assembles the persistent store.TryRecord for this attempt from
// the resolved attempt/run state and the agent result.
func buildTryRecord(relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, task runTask, state *runOneState, attempt *runAttemptState) store.TryRecord {
	tryRecord := store.TryRecord{
		ID:                     attempt.tryID,
		OutingID:               runIndex + 1,
		RelayID:                relay.ID,
		AgentType:              picked.Harness,
		Completed:              !attempt.failed,
		Outcome:                attempt.attemptOutcome,
		ResolvedRoute:          task.ResolvedRoute,
		DirtyHandoff:           attempt.dirtyHandoff,
		RecoveryClassification: attempt.recoveryClassification,
		Summary:                "",
		RemainingWork:          "",
		FilesChanged:           attempt.filesChangedList,
		CommitHash:             attempt.commitHash,
		CommitHistory:          attempt.commitHistory,
		StartedAt:              attempt.startedAt.Format(time.RFC3339),
		EndedAt:                attempt.endedAt.Format(time.RFC3339),
		AttemptNumber:          attempt.attempt,
		LogPath:                attempt.tryLogPath,
		FailReason:             state.failReason,
		Category:               string(state.failureCategory),
		RuntimeMs:              attempt.runtime.Milliseconds(),
		LapID:                  task.LapID,
		LapAssignee:            task.Assignee,
		HandoffCreatedLapIDs:   handoffCreatedLapIDs(attempt.handoffEntry),
		RecordedLaps:           attempt.recordedLaps,
		LapsAttempted:          attempt.lapsAttempted,
	}
	if attempt.result != nil {
		tryRecord.Summary = attempt.result.Summary
		tryRecord.RemainingWork = attempt.result.RemainingWork
		tryRecord.ToolCalls = attempt.result.ToolCalls
		if len(attempt.result.FilesChanged) > 0 {
			// Prefer the agent-reported list if it gave one.
			tryRecord.FilesChanged = attempt.result.FilesChanged
		}
	}
	return tryRecord
}

// emitAttemptTelemetry populates the try trace span and structured try-log
// fields, attaches failure/timeout/lap-pin/provider-limit/recovery evidence,
// and captures operator-worthy and diagnostic events. It returns the assembled
// try-log fields for the caller's final EmitTryLog call.
func (r *Runner) emitAttemptTelemetry(relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, task runTask, state *runOneState, attempt *runAttemptState, tryRecord store.TryRecord) map[string]interface{} {
	// Telemetry: per-try structured log + trace span tags. Only summaries
	// and byte sizes are emitted — never current_task.md contents or the
	// transcript (the scrubber is defense-in-depth on top of this).
	tryTags := telemetry.Tags(telemetry.EventInfo{
		RelayID:  relay.ID,
		RunID:    runIndex + 1,
		TryID:    tryRecord.ID,
		Role:     task.promptAssignee(),
		Harness:  picked.Harness,
		Model:    resolvedRunnerModel(attempt.result, picked),
		Repo:     state.rc.Repo,
		RepoName: state.rc.RepoName,
		LapID:    task.LapID,
	})
	applyTags(attempt.trySpan, tryTags)
	attempt.trySpan.SetTag("outcome", string(attempt.attemptOutcome))
	attempt.trySpan.SetData("completed", !attempt.failed)
	attempt.trySpan.SetData("outcome", string(attempt.attemptOutcome))
	if attempt.dirtyHandoff {
		attempt.trySpan.SetData("dirty_handoff", true)
	}
	if task.ResolvedRoute == "recovery" && attempt.recoveryClassification != "" {
		attempt.trySpan.SetTag("recovery_classification", attempt.recoveryClassification)
		attempt.trySpan.SetData("recovery_classification", attempt.recoveryClassification)
	}
	attempt.trySpan.SetData("fail_reason", state.failReason)
	tryLogFields := map[string]interface{}{
		"event":                          "try",
		"relay_id":                       relay.ID,
		"run_id":                         runIndex + 1,
		"try_id":                         tryRecord.ID,
		"attempt":                        attempt.attempt,
		"role":                           task.promptAssignee(),
		"runner":                         telemetry.RunnerLabel(picked.Harness, resolvedRunnerModel(attempt.result, picked)),
		"repo":                           state.rc.Repo,
		"repo_name":                      state.rc.RepoName,
		"lap_id":                         task.LapID,
		"completed":                      !attempt.failed,
		"outcome":                        string(attempt.attemptOutcome),
		"fail_reason":                    state.failReason,
		"failure_class":                  string(state.failureClass),
		"runtime_ms":                     attempt.runtime.Milliseconds(),
		"files_changed":                  attempt.filesChangedCount,
		"tool_calls":                     tryRecord.ToolCalls,
		"prompt_bytes":                   len(attempt.prompt),
		"prompt_recent_context_bytes":    len(attempt.opts.RecentTryContext),
		"prompt_previous_summary_bytes":  len(attempt.opts.PreviousSummary),
		"prompt_instructions_bytes":      len(attempt.opts.Instructions),
		"prompt_role_instructions_bytes": len(attempt.opts.RoleInstructions),
		"prompt_task_bytes":              len(attempt.opts.TaskPrompt),
		"prompt_inbox_bytes":             len(attempt.opts.InboxMessage),
		"prompt_relay_message_bytes":     len(attempt.opts.RelayMessage),
	}
	if attempt.dirtyHandoff {
		tryLogFields["dirty_handoff"] = true
	}
	if task.ResolvedRoute == "recovery" && attempt.recoveryClassification != "" {
		tryLogFields["recovery_classification"] = attempt.recoveryClassification
	}
	evidenceState := telemetry.FailureState{
		Attempt:                attempt.attempt,
		MaxAttempts:            state.maxAttempts,
		Outcome:                string(attempt.attemptOutcome),
		Category:               string(state.failureCategory),
		RecoveryClassification: attempt.recoveryClassification,
		AgentState:             r.agentStateName(picked),
	}
	applyEvidenceToFailureState(&evidenceState, state.resetEvidence, "executor_evidence")
	if attempt.result.Evidence == nil && attempt.decisionEvidence == nil {
		applySafeExecErrorEvidence(&evidenceState, attempt.execErr)
	}
	if attempt.failed {
		addFailureEvidenceTelemetry(attempt.trySpan, tryLogFields, evidenceState)
	}
	if attempt.timedOut {
		timeoutKind := "try_cap"
		timeoutBudget := state.tryTimeout
		if attempt.runBudgetExhausted {
			timeoutKind = "run_budget"
			timeoutBudget = r.cfg.RunTimeout
		}
		attempt.trySpan.SetTag("timeout_kind", timeoutKind)
		attempt.trySpan.SetData("timeout_kind", timeoutKind)
		tryLogFields["timeout_kind"] = timeoutKind
		if timeoutBudget > 0 {
			attempt.trySpan.SetData("timeout_budget_ms", timeoutBudget.Milliseconds())
			tryLogFields["timeout_budget_ms"] = timeoutBudget.Milliseconds()
		}
		if age, ok := lastOutputAge(attempt.tryLogPath, attempt.endedAt); ok {
			attempt.trySpan.SetData("last_output_age_ms", age.Milliseconds())
			tryLogFields["last_output_age_ms"] = age.Milliseconds()
		}
		sessionCaptured := state.sessionID != ""
		resumeSupported := state.exec != nil && state.exec.ResumeSupported()
		handoffOnlyAttempted := attempt.runBudgetExhausted && attempt.canHandoffResume
		attempt.trySpan.SetData("session_captured", sessionCaptured)
		attempt.trySpan.SetData("resume_supported", resumeSupported)
		attempt.trySpan.SetData("handoff_only_attempted", handoffOnlyAttempted)
		tryLogFields["session_captured"] = sessionCaptured
		tryLogFields["resume_supported"] = resumeSupported
		tryLogFields["handoff_only_attempted"] = handoffOnlyAttempted
		if handoffOnlyAttempted {
			handoffOnlyTryID := tryRecord.ID + 1
			attempt.trySpan.SetData("handoff_only_try_id", handoffOnlyTryID)
			tryLogFields["handoff_only_try_id"] = handoffOnlyTryID
		}
		if attempt.runBudgetExhausted && !attempt.canHandoffResume {
			blocker := noHandoffResumeReason(state.exec, state.sessionID)
			attempt.trySpan.SetData("handoff_resume_blocker", blocker)
			tryLogFields["handoff_resume_blocker"] = blocker
		}
	}
	if attempt.lapPinMismatch {
		attempt.trySpan.SetTag("event_kind", "lap_pin_mismatch")
		attempt.trySpan.SetData("mismatch_reason", state.failReason)
		tryLogFields["event_kind"] = "lap_pin_mismatch"
		tryLogFields["mismatch_reason"] = state.failReason
		tryLogFields["expected_lap_id"] = task.LapID
		tryLogFields["consumed_lap_count"] = len(attempt.recordedLaps)
		if len(attempt.recordedLaps) > 0 {
			tryLogFields["consumed_lap_ids"] = strings.Join(attempt.recordedLaps, ",")
		}
		fs := telemetry.FailureState{
			Attempt:     attempt.attempt,
			MaxAttempts: state.maxAttempts,
			Outcome:     string(attempt.attemptOutcome),
			AgentState:  r.agentStateName(picked),
		}
		r.tel().CaptureEvent(attempt.tryCtx, fmt.Sprintf("relay %d run %d try %d lap pin mismatch: %s", relay.ID, runIndex+1, tryRecord.ID, state.failReason),
			lapPinMismatchDiagnosticEvent(tryTags, state.rc, fs, state.failReason, task.LapID, attempt.recordedLaps))
	}
	// Capture provider-limit evidence as low-severity diagnostic telemetry
	// regardless of whether the failure is operator-worthy enough to become an
	// operator failure. This builds the parser-validation corpus without
	// broadening alerts.
	if attempt.failed && attempt.attemptOutcome.ShouldCaptureIssue() {
		fs := telemetry.FailureState{
			Attempt:                attempt.attempt,
			MaxAttempts:            state.maxAttempts,
			Outcome:                string(attempt.attemptOutcome),
			Category:               string(state.failureCategory),
			RecoveryClassification: attempt.recoveryClassification,
			AgentState:             r.agentStateName(picked),
		}
		if state.resetEvidence != nil {
			applyEvidenceToFailureState(&fs, state.resetEvidence, "executor_evidence")
		}
		if attempt.result.Evidence == nil && attempt.decisionEvidence == nil {
			applySafeExecErrorEvidence(&fs, attempt.execErr)
		}
		if evt, ok := limitSignalEvent(tryTags, state.rc, fs); ok {
			r.tel().CaptureEvent(attempt.tryCtx, fmt.Sprintf("relay %d run %d try %d provider limit signal: %s", relay.ID, runIndex+1, tryRecord.ID, state.failReason), evt)
		}

		// Capture operator-worthy failures. Ordinary
		// agent-class retries (recoverable agent errors, short no-ops) stay
		// spans/logs only to avoid alert noise.
		issueWorthy := !attempt.lapPinMismatch &&
			(state.failureClass == reliability.FailureInfra ||
				attempt.execErr != nil ||
				attempt.markerAsText != "" ||
				strings.Contains(strings.ToLower(state.failReason), "panic"))
		if issueWorthy {
			// Enrich the terminal-try capture with the structured failure
			// state already resolved above: attempt/budget, the resolved
			// failure category, the failing runner's resilience standing, and
			// — for provider-limit categories — the parsed quota scope/reset
			// and bounded raw provider signal from TryResult.Evidence.
			r.tel().CaptureFailure(attempt.tryCtx, fmt.Sprintf("relay %d run %d try %d failed: %s", relay.ID, runIndex+1, tryRecord.ID, state.failReason), failureStateEvent(tryTags, state.rc, fs))
		}
	}
	if task.ResolvedRoute == "recovery" && attempt.recoveryClassification == "needs_user" {
		fs := telemetry.FailureState{
			Attempt:                attempt.attempt,
			MaxAttempts:            state.maxAttempts,
			Outcome:                string(attempt.attemptOutcome),
			RecoveryClassification: attempt.recoveryClassification,
			AgentState:             r.agentStateName(picked),
		}
		r.tel().CaptureFailure(attempt.tryCtx, fmt.Sprintf("relay %d run %d try %d recovery needs_user", relay.ID, runIndex+1, tryRecord.ID), failureStateEvent(tryTags, state.rc, fs))
	}
	return tryLogFields
}
