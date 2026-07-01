package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent_prompt"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/style"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

// noHandoffResumeReason explains, for a budget-cancelled implementation try that
// could not start a bounded handoff-only continuation, which resume precondition
// was missing. The reason is persisted on the resolving handoff_timeout try so
// recovery routing and operator triage can tell a no-resume harness from a
// no-session attempt (task 4.3).
func noHandoffResumeReason(exec harnessapi.Executor, sessionID string) string {
	if exec == nil || !exec.ResumeSupported() {
		return "run timeout; harness cannot resume for handoff"
	}
	if sessionID == "" {
		return "run timeout; no session captured for handoff"
	}
	return "run timeout"
}

func buildHandoffOnlyPrompt(opts harnessapi.RunOptions) string {
	var b strings.Builder
	if opts.Persona != "" {
		fmt.Fprintf(&b, "Persona: %s\n\n", opts.Persona)
	}
	if opts.TaskName != "" {
		fmt.Fprintf(&b, "Task: %s\n", opts.TaskName)
	}
	if opts.TaskRequirements != "" {
		fmt.Fprintf(&b, "Requirements:\n%s\n\n", opts.TaskRequirements)
	}
	if opts.Instructions != "" {
		fmt.Fprintf(&b, "## Project Instructions\n%s\n\n", opts.Instructions)
	}
	if opts.RoleInstructions != "" {
		fmt.Fprintf(&b, "## Role Instructions\n%s\n\n", opts.RoleInstructions)
	}
	fmt.Fprintf(&b, "## Handoff-Only Continuation\n")
	fmt.Fprintf(&b, "%s\n", agent_prompt.HandoffOnly())
	return b.String()
}

// runBoundedHandoffOnly performs the single bounded, handoff-only continuation
// after the run budget is exhausted on a resume-capable harness (task 4.1). It
// resumes the captured session with a handoff-only prompt under a fresh context
// bounded by HandoffTimeout — no stall detector, not counted against the run
// budget — then persists the continuation as a separate HandoffOnly try under
// the same RunID with attemptNumber (allowed to exceed maxAttempts). The outcome
// is handoff_requested ONLY when a durable current-run handoff entry exists
// (proving both laps handoff and laps wrapup completed); otherwise
// handoff_timeout (task 4.2). It returns the resolving outcome, the continuation
// result, and whether the continuation succeeded.
func (r *Runner) runBoundedHandoffOnly(
	ctx context.Context,
	relay *store.RelayRecord,
	runIndex int,
	picked harnessapi.ResolvedAgent,
	task runTask,
	rc telemetry.RallyContext,
	roleInstructions string,
	sessionID string,
	attemptNumber int,
	maxAttempts int,
	summaryEntryCountBeforeRun int,
	runID string,
	runStartDirtySnapshot map[string]string,
	log io.Writer,
) (reliability.TryOutcome, *harnessapi.TryResult, bool, bool, error) {
	startedAt := time.Now().UTC()

	// Persist the captured session id so the resume-capable harness re-attaches to
	// the same conversation (mirrors the in-loop retry resume wiring).
	if rs, err := progress.LoadRunState(r.cfg.WorkspaceDir); err == nil && rs != nil {
		rs.SessionID = sessionID
		_ = progress.SaveRunState(r.cfg.WorkspaceDir, rs)
	}

	opts := harnessapi.RunOptions{
		Persona:          picked.Harness,
		Model:            picked.Model,
		ReasoningEffort:  picked.ReasoningEffort,
		TaskName:         task.Name,
		TaskRequirements: task.Requirements,
		Instructions:     r.resolveInstructions(),
		RoleInstructions: roleInstructions,
		LapsEnabled:      r.cfg.LapsEnabled,
		ResumeSessionID:  sessionID,
		WorkspaceDir:     r.cfg.WorkspaceDir,
	}
	// The handoff-only prompt forbids implementation and directs a blocker
	// summary + laps handoff + laps wrapup. Built as an explicit override so the
	// normal task template (which invites continued implementation) is bypassed.
	opts.Prompt = buildHandoffOnlyPrompt(opts)

	tryID := r.store.NextTryID()
	tryLogPath := filepath.Join(r.cfg.DataDir, "tries", repoKey(r.cfg.WorkspaceDir), fmt.Sprintf("try-%d.log", tryID))
	_ = os.MkdirAll(filepath.Dir(tryLogPath), 0o755)
	opts.LogPath = tryLogPath

	tryCtx, trySpan := r.tel().StartSpan(ctx, "try", fmt.Sprintf("relay-%d-run-%d-try-%d", relay.ID, runIndex+1, tryID))

	if err := progress.SetActiveTry(r.cfg.WorkspaceDir, progress.ActiveTryMetadata{
		RelayID:   relay.ID,
		RunID:     runIndex + 1,
		TryID:     tryID,
		LogPath:   tryLogPath,
		StartedAt: startedAt,
	}); err != nil {
		trySpan.Finish()
		return reliability.OutcomeHandoffTimeout, nil, false, false, fmt.Errorf("set active try metadata: %w", err)
	}

	handoffCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Bound the phase by HandoffTimeout. No stall detector and no per-run budget
	// arm join this select — only the handoff limit and parent-context
	// cancellation can end it besides the agent finishing.
	var handoffBound <-chan time.Time
	if r.cfg.HandoffTimeout > 0 {
		ch, stop := r.newBoundTimer(r.cfg.HandoffTimeout)
		defer stop()
		handoffBound = ch
	}

	tryCh := make(chan tryResult, 1)
	go func() {
		res, err := r.executeTry(handoffCtx, picked, opts)
		tryCh <- tryResult{res, err}
	}()

	var (
		result   *harnessapi.TryResult
		execErr  error
		timedOut bool
	)
	select {
	case res := <-tryCh:
		result = res.result
		execErr = res.err
	case <-handoffBound:
		cancel()
		timedOut = true
		res := <-tryCh
		result = res.result
		execErr = res.err
	case <-ctx.Done():
		cancel()
		res := <-tryCh
		result = res.result
		execErr = res.err
	}
	endedAt := time.Now().UTC()

	summary := r.normalizeFinalSnippet(runID, tryLogPath, summaryEntryCountBeforeRun, result, execErr)
	if result == nil {
		result = &harnessapi.TryResult{}
	}
	result.Summary = summary

	// A durable current-run handoff entry (appended after the run started) proves
	// both laps handoff and laps wrapup completed. Transient HandoffState alone
	// (a laps handoff with no wrapup) is NOT sufficient — it only distinguishes a
	// partial/no-wrapup attempt for the failure reason.
	runEntry := recordedRunEntryForRun(r.cfg.WorkspaceDir, runID, summaryEntryCountBeforeRun)
	handoffEntry := handoffEntryFromRunEntry(runEntry)
	recoveryClassification := recoveryClassificationForRun(task, runEntry)
	handoffState := 0
	if rs, err := progress.LoadRunState(r.cfg.WorkspaceDir); err == nil && rs != nil {
		handoffState = rs.HandoffState
	}
	dirtyAfter, _ := gitx.WorkspaceDirtyPaths(r.cfg.WorkspaceDir)
	hasOwnUncommittedChanges := hasDirtyChangesSince(runStartDirtySnapshot, dirtyAfter)
	dirtyHandoff := handoffEntry != nil && hasOwnUncommittedChanges

	outcome := reliability.OutcomeHandoffTimeout
	failReason := ""
	succeeded := false
	switch {
	case handoffEntry != nil:
		outcome = reliability.OutcomeHandoffRequested
		succeeded = true
	case timedOut:
		failReason = "handoff timeout"
	case execErr != nil:
		failReason = "handoff failed"
	case handoffState != 0:
		failReason = "handoff without wrapup"
	default:
		failReason = "handoff not completed"
	}

	// Fold handoff bookkeeping (summary.jsonl, any head followups) into history so
	// the durable handoff entry is committed. Dirty-handoff auto-commit
	// suppression for leftover code changes is owned by a later lap.
	if err := gitx.FoldRallyState(r.cfg.WorkspaceDir); err != nil {
		fmt.Fprintf(log, "relay %d run %d handoff-only rally state fold warning: %v\n", relay.ID, runIndex+1, err)
	}

	runtime := endedAt.Sub(startedAt)
	renderRunFooter(r.outWriter(), style.FooterOptions{
		Passed:      succeeded,
		Duration:    runtime,
		FailReason:  failReason,
		Attempt:     attemptNumber,
		MaxAttempts: maxAttempts,
	})

	tryRecord := store.TryRecord{
		ID:                     tryID,
		RunID:                  runIndex + 1,
		RelayID:                relay.ID,
		AgentType:              picked.Harness,
		Completed:              succeeded,
		Outcome:                outcome,
		HandoffOnly:            true,
		ResolvedRoute:          task.ResolvedRoute,
		DirtyHandoff:           dirtyHandoff,
		RecoveryClassification: recoveryClassification,
		Summary:                result.Summary,
		RemainingWork:          result.RemainingWork,
		FilesChanged:           []string{},
		StartedAt:              startedAt.Format(time.RFC3339),
		EndedAt:                endedAt.Format(time.RFC3339),
		AttemptNumber:          attemptNumber,
		LogPath:                tryLogPath,
		FailReason:             failReason,
		RuntimeMs:              runtime.Milliseconds(),
		LapID:                  task.LapID,
		LapAssignee:            task.Assignee,
		HandoffCreatedLapIDs:   handoffCreatedLapIDs(handoffEntry),
		ToolCalls:              result.ToolCalls,
	}

	tryTags := telemetry.Tags(telemetry.EventInfo{
		RelayID:  relay.ID,
		RunID:    runIndex + 1,
		TryID:    tryID,
		Role:     task.promptAssignee(),
		Harness:  picked.Harness,
		Model:    resolvedRunnerModel(result, picked),
		Repo:     rc.Repo,
		RepoName: rc.RepoName,
		LapID:    task.LapID,
	})
	applyTags(trySpan, tryTags)
	trySpan.SetTag("outcome", string(outcome))
	trySpan.SetTag("handoff_only", "true")
	trySpan.SetData("completed", succeeded)
	trySpan.SetData("outcome", string(outcome))
	trySpan.SetData("handoff_only", true)
	trySpan.SetTag("timeout_kind", "handoff")
	trySpan.SetData("timeout_kind", "handoff")
	if r.cfg.HandoffTimeout > 0 {
		trySpan.SetData("timeout_budget_ms", r.cfg.HandoffTimeout.Milliseconds())
	}
	trySpan.SetData("session_captured", sessionID != "")
	trySpan.SetData("resume_supported", true)
	trySpan.SetData("handoff_only_attempted", true)
	if task.ResolvedRoute == "recovery" && recoveryClassification != "" {
		trySpan.SetTag("recovery_classification", recoveryClassification)
		trySpan.SetData("recovery_classification", recoveryClassification)
	}
	trySpan.SetData("fail_reason", failReason)
	tryLogFields := map[string]interface{}{
		"event":                  "try",
		"relay_id":               relay.ID,
		"run_id":                 runIndex + 1,
		"try_id":                 tryID,
		"attempt":                attemptNumber,
		"role":                   task.promptAssignee(),
		"runner":                 telemetry.RunnerLabel(picked.Harness, resolvedRunnerModel(result, picked)),
		"repo":                   rc.Repo,
		"repo_name":              rc.RepoName,
		"lap_id":                 task.LapID,
		"completed":              succeeded,
		"outcome":                string(outcome),
		"handoff_only":           true,
		"fail_reason":            failReason,
		"runtime_ms":             runtime.Milliseconds(),
		"timeout_kind":           "handoff",
		"session_captured":       sessionID != "",
		"resume_supported":       true,
		"handoff_only_attempted": true,
	}
	if r.cfg.HandoffTimeout > 0 {
		tryLogFields["timeout_budget_ms"] = r.cfg.HandoffTimeout.Milliseconds()
	}
	if age, ok := lastOutputAge(tryLogPath, endedAt); ok {
		trySpan.SetData("last_output_age_ms", age.Milliseconds())
		tryLogFields["last_output_age_ms"] = age.Milliseconds()
	}
	if outcome == reliability.OutcomeHandoffTimeout {
		trySpan.SetData("handoff_resume_blocker", "handoff timeout")
		tryLogFields["handoff_resume_blocker"] = "handoff timeout"
	}
	if task.ResolvedRoute == "recovery" && recoveryClassification != "" {
		tryLogFields["recovery_classification"] = recoveryClassification
	}
	if task.ResolvedRoute == "recovery" && recoveryClassification == "needs_user" {
		fs := telemetry.FailureState{
			Attempt:                attemptNumber,
			MaxAttempts:            maxAttempts,
			Outcome:                string(outcome),
			RecoveryClassification: recoveryClassification,
			AgentState:             r.agentStateName(picked),
		}
		r.tel().CaptureFailure(tryCtx, fmt.Sprintf("relay %d run %d try %d recovery needs_user", relay.ID, runIndex+1, tryID), failureStateEvent(tryTags, rc, fs))
	}
	trySpan.Finish()

	fmt.Fprintf(log, "relay %d run %d handoff-only continuation: attempt=%d outcome=%q fail_reason=%q runtime=%s handoff_state=%d durable_handoff=%v\n",
		relay.ID, runIndex+1, attemptNumber, outcome, failReason, runtime, handoffState, handoffEntry != nil)

	if err := r.store.AppendTry(tryRecord); err != nil {
		return outcome, result, succeeded, dirtyHandoff, err
	}
	if err := progress.ClearActiveTry(r.cfg.WorkspaceDir); err != nil {
		return outcome, result, succeeded, dirtyHandoff, fmt.Errorf("clear active try metadata: %w", err)
	}
	r.tel().EmitTryLog(tryCtx, tryLogFields)

	return outcome, result, succeeded, dirtyHandoff, nil
}
