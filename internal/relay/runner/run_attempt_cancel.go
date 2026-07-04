package runner

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

func (r *Runner) recordCancelledAttempt(relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, task runTask, state *runOneState, attempt *runAttemptState, log io.Writer) (bool, error) {
	// Operator cancellation short-circuit: when the action loop recorded
	// a cancellation source (skip / graceful_stop / quit_now), the attempt
	// is classified as OutcomeCancelled without entering the normal failure
	// taxonomy, retry scheduling, or resilience counter updates. The
	// cancelled try is persisted with its source and the attempt loop breaks
	// immediately; route semantics (skip advances, stop/quit halts) are
	// preserved by the existing skipFlag/stopFlag handling after the break.
	if attempt.cancellationSource == CancellationSourceNone {
		return false, nil
	}
	cancellationSourceValue := attempt.cancellationSource.String()
	attemptOutcome := reliability.OutcomeCancelled
	state.failReason = "cancelled (" + cancellationSourceValue + ")"

	// Capture session id before it goes out of scope — cancelled
	// attempts may still carry a resumable session that downstream
	// handling (bounded handoff continuation) needs.
	if attempt.result != nil && attempt.result.SessionID != "" {
		state.sessionID = attempt.result.SessionID
	}

	// Render a terminal footer for the cancelled attempt. The persisted
	// outcome/source below are the source of truth; the style layer owns
	// the muted cancelled presentation.
	cancelledFooter := runtimeevent.FooterData{
		Cancelled:          true,
		Duration:           attempt.runRuntime,
		FilesChanged:       attempt.filesChangedCount,
		CommitHash:         attempt.shortHash,
		CommitTitle:        attempt.commitTitle,
		FailReason:         state.failReason,
		CancellationSource: cancellationSourceValue,
		Interim:            false,
		Attempt:            attempt.attempt,
		MaxAttempts:        state.maxAttempts,
	}
	r.eventSink().Emit(context.Background(), runtimeevent.AttemptCancelled{FooterData: cancelledFooter})

	tryRecord := store.TryRecord{
		ID:                     attempt.tryID,
		OutingID:               runIndex + 1,
		RelayID:                relay.ID,
		AgentType:              picked.Harness,
		Completed:              false,
		Outcome:                attemptOutcome,
		CancellationSource:     cancellationSourceValue,
		ResolvedRoute:          task.ResolvedRoute,
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
		RuntimeMs:              attempt.runtime.Milliseconds(),
		LapID:                  task.LapID,
		LapAssignee:            task.Assignee,
		RecordedLaps:           attempt.recordedLaps,
		LapsAttempted:          attempt.lapsAttempted,
	}
	if attempt.result != nil {
		tryRecord.Summary = attempt.result.Summary
		tryRecord.RemainingWork = attempt.result.RemainingWork
		tryRecord.ToolCalls = attempt.result.ToolCalls
		if len(attempt.result.FilesChanged) > 0 {
			tryRecord.FilesChanged = attempt.result.FilesChanged
		}
	}
	fmt.Fprintf(log, "relay %d run %d attempt %d cancelled: source=%q runtime=%s files_changed=%d commit=%q lap_id=%q assignee=%q\n",
		relay.ID, runIndex+1, attempt.attempt, cancellationSourceValue, attempt.runtime, attempt.filesChangedCount, attempt.shortHash, task.LapID, task.Assignee)

	// Telemetry: span tags + structured log, but NO failure capture and
	// NO issue-worthy classification — this is a deliberate operator
	// action, not a fault.
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
	attempt.trySpan.SetTag("outcome", string(attemptOutcome))
	attempt.trySpan.SetTag("cancellation_source", cancellationSourceValue)
	attempt.trySpan.SetData("completed", false)
	attempt.trySpan.SetData("outcome", string(attemptOutcome))
	attempt.trySpan.SetData("cancellation_source", cancellationSourceValue)
	attempt.trySpan.Finish()

	state.resolvingOutcome = attemptOutcome
	if err := r.store.AppendTry(tryRecord); err != nil {
		return true, err
	}
	if err := progress.ClearActiveTry(r.cfg.WorkspaceDir); err != nil {
		return true, fmt.Errorf("clear active try metadata: %w", err)
	}
	r.tel().EmitTryLog(attempt.tryCtx, map[string]interface{}{
		"event":               "try",
		"relay_id":            relay.ID,
		"run_id":              runIndex + 1,
		"try_id":              tryRecord.ID,
		"attempt":             attempt.attempt,
		"role":                task.promptAssignee(),
		"runner":              telemetry.RunnerLabel(picked.Harness, resolvedRunnerModel(attempt.result, picked)),
		"repo":                state.rc.Repo,
		"repo_name":           state.rc.RepoName,
		"lap_id":              task.LapID,
		"completed":           false,
		"outcome":             string(attemptOutcome),
		"cancellation_source": cancellationSourceValue,
		"runtime_ms":          attempt.runtime.Milliseconds(),
		"files_changed":       attempt.filesChangedCount,
		"tool_calls":          tryRecord.ToolCalls,
	})

	// Cancelled is terminal for the attempt loop. The route semantics
	// (skip → advance, stop/quit → halt relay) are preserved because
	// skipFlag and stopFlag were set by the action loop and are checked
	// by the actionTaken handling below and the Run() dispatch loop.
	state.lastResult = attempt.result
	return true, nil
}
