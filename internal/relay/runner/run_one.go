package runner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/keyboard"
	"github.com/mitchell-wallace/rally/internal/monitor"
	"github.com/mitchell-wallace/rally/internal/progress"
	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/style"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

// runOutcome carries the result of one run (one runner assigned to one lap)
// back to the routing dispatch loop in Run.
type runOutcome struct {
	// Success reports whether the run finalized successfully.
	Success bool
	// Addressed reports whether the consumed inbox/relay message was
	// addressed, from the final try's MessageAddressed.
	Addressed bool
	// Interrupted reports that the run ended on an operator stop request.
	Interrupted bool
	// FailReason is the display-formatted reason of the most recent failed
	// attempt; empty for unknown errors.
	FailReason string
	// FailureClass is the resilience class of the most recent failed attempt.
	FailureClass reliability.FailureClass
	// Category is the resolved FailureCategory of the most recent failed
	// attempt; empty when no failed attempt was classified.
	Category reliability.FailureCategory
	// Outcome is the lifecycle outcome of the resolving try.
	Outcome reliability.TryOutcome
	// LapID is the queue lap resolved by this run.
	LapID string
	// DirtyHandoff reports that the resolving try durable-handed off while
	// leaving own uncommitted work behind.
	DirtyHandoff bool
	// ResetEvidence carries parsed reset timing (ResetAt/ResetAfter) used to
	// size the bench window on a usage_limit.
	ResetEvidence *reliability.FailureEvidence
	// InfraFailures counts attempts classified infra-class within this run.
	InfraFailures int
}

type routeFallbackCause struct {
	fromRunner           string
	triggerRunID         int
	triggerTryID         int
	triggerOutcome       string
	triggerFailReason    string
	triggerFailureClass  string
	triggerFailureCat    string
	triggerLapID         string
	routeName            string
	entryExhaustedReason string
}

func (c *routeFallbackCause) addTo(fields map[string]interface{}, span telemetry.Span) {
	if c == nil {
		return
	}
	values := map[string]interface{}{
		"trigger_run_id":               c.triggerRunID,
		"trigger_try_id":               c.triggerTryID,
		"trigger_outcome":              c.triggerOutcome,
		"trigger_fail_reason":          c.triggerFailReason,
		"trigger_failure_class":        c.triggerFailureClass,
		"trigger_failure_category":     c.triggerFailureCat,
		"trigger_lap_id":               c.triggerLapID,
		"route_name":                   c.routeName,
		"route_entry_exhausted_reason": c.entryExhaustedReason,
	}
	for k, v := range values {
		if v == "" || v == 0 {
			continue
		}
		fields[k] = v
		if span != nil {
			switch x := v.(type) {
			case string:
				span.SetTag(k, x)
			default:
				span.SetData(k, x)
			}
		}
	}
}

func (r *Runner) runOne(
	ctx context.Context,
	relay *store.RelayRecord,
	runIndex int,
	picked agent.ResolvedAgent,
	task runTask,
	consumedMsg *store.MessageRecord,
	relayMsg *store.MessageRecord,
	isHourlyRetry bool,
	isProbation bool,
	onStall func(),
	onStallRecovered func(),
	log io.Writer,
) (runOutcome, error) {
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

	var previousSummary string
	var lastResult *agent.TryResult
	var sessionID string
	success := false
	failReason := ""
	failureClass := reliability.FailureAgent
	// failureCategory and resetEvidence carry the most recent attempt's resolved
	// classification up to the routing dispatch loop. The category lets the loop
	// route/bench on quota exhaustion or auth errors; resetEvidence carries the
	// parsed reset deadline (ResetAt/ResetAfter) used to size the bench window.
	var failureCategory reliability.FailureCategory
	var resetEvidence *reliability.FailureEvidence
	var resolvingOutcome reliability.TryOutcome
	var resolvingDirtyHandoff bool
	infraFailures := 0
	// outcome snapshots the run-scoped failure state for return; only the
	// success/addressed/interrupted flags vary per return site.
	outcome := func(succeeded, addressed, interrupted bool) runOutcome {
		return runOutcome{
			Success:       succeeded,
			Addressed:     addressed,
			Interrupted:   interrupted,
			FailReason:    failReason,
			FailureClass:  failureClass,
			Category:      failureCategory,
			Outcome:       resolvingOutcome,
			LapID:         task.LapID,
			DirtyHandoff:  resolvingDirtyHandoff,
			ResetEvidence: resetEvidence,
			InfraFailures: infraFailures,
		}
	}
	lastAttemptIncomplete := false
	stallMarked := false
	// lastAttempt tracks the final attempt number reached, so the
	// unfinalized-agent capture below (which runs after the loop) can report the
	// last known attempt as context.
	lastAttempt := 0
	runLapPinMismatch := false
	// Bounded handoff-only continuation state (task 4). When the run budget is
	// exhausted on a resume-capable harness with a captured session, the attempt
	// loop persists the cancelled implementation try as run_timeout and breaks,
	// then the post-loop phase resumes that session once under HandoffTimeout to
	// capture a clean handoff. These carry the decision and session out of the
	// loop; handoffResumeBaseAttempt is the run_timeout attempt number, so the
	// continuation records the next attempt (allowed to exceed maxAttempts).
	handoffResumePending := false
	handoffResumeSessionID := ""
	handoffResumeBaseAttempt := 0
	roleInstructions, err := r.resolveRoleInstructions(task.promptAssignee())
	if err != nil {
		return outcome(false, false, false), err
	}

	// Check for uncommitted non-rally changes at run start. Errors are
	// tolerated (treat as clean) so a broken git setup never crashes the run.
	leftoverWork, _ := gitx.IsWorkspaceDirty(r.cfg.WorkspaceDir)
	runStartDirtySnapshot, _ := gitx.WorkspaceDirtyPaths(r.cfg.WorkspaceDir)

	exec := r.executors[picked.Harness]

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

	// Per-run wall-clock budget across all retry attempts. Constructed ONCE here
	// (measured from run start), never inside the attempt loop, so a single timer
	// channel — passed into every runActionLoop invocation — measures cumulative
	// time across retries instead of resetting each attempt. A non-positive
	// budget leaves runBudgetCh nil, disabling the bound. The per-try cap is
	// created per attempt inside the loop (mirroring stallTicker).
	var runBudgetCh <-chan time.Time
	var runDeadline time.Time
	if r.cfg.RunTimeout > 0 {
		ch, stop := r.newBoundTimer(r.cfg.RunTimeout)
		defer stop()
		runBudgetCh = ch
		runDeadline = time.Now().Add(r.cfg.RunTimeout)
	}
	tryTimeout := r.cfg.TryTimeout
	if r.cfg.RunTimeout > 0 && tryTimeout >= r.cfg.RunTimeout {
		// When the per-try cap is equal to or longer than the per-run budget, the
		// run budget subsumes it. Leaving both timers armed creates a select race
		// at the same deadline, which can incorrectly persist a retryable "try
		// timeout" instead of the run-budget handoff path.
		tryTimeout = 0
	}
	var runStartedAt time.Time
attemptLoop:
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastAttempt = attempt
		if ctx.Err() != nil {
			return outcome(false, false, false), ctx.Err()
		}
		if r.stopFlag.Load() {
			return outcome(false, false, true), nil
		}

		if attempt > 1 {
			if exec != nil && exec.ResumeSupported() && sessionID != "" {
				rs, rsErr := progress.LoadRunState(r.cfg.WorkspaceDir)
				if rsErr == nil {
					rs.SessionID = sessionID
					_ = progress.SaveRunState(r.cfg.WorkspaceDir, rs)
				}
			} else {
				sessionID = ""
				_ = progress.SaveRunState(r.cfg.WorkspaceDir, newProgressRunState(runID, task.LapID))
			}
		}

		// Each try (attempt) is a child span of the run. NextTryID peeks the
		// id this attempt's record will be assigned at AppendTry below.
		tryID := r.store.NextTryID()
		tryCtx, trySpan := r.tel().StartSpan(ctx, "try", fmt.Sprintf("relay-%d-run-%d-try-%d", relay.ID, runIndex+1, tryID))

		opts := agent.RunOptions{
			Persona:          picked.Harness,
			Model:            picked.Model,
			ReasoningEffort:  picked.ReasoningEffort,
			Role:             task.promptAssignee(),
			TaskName:         task.Name,
			TaskRequirements: task.Requirements,
			TaskPrompt:       task.Prompt,
			Instructions:     r.resolveInstructions(),
			RoleInstructions: roleInstructions,
			InboxMessage:     inbox,
			RelayMessage:     relayMessage,
			PreviousSummary:  previousSummary,
			RecentTryContext: recentContext,
			LapsEnabled:      r.cfg.LapsEnabled,
			LeftoverWork:     leftoverWork,
			ResumeSessionID:  sessionID,
			WorkspaceDir:     r.cfg.WorkspaceDir,
		}
		if lastAttemptIncomplete {
			if opts.TaskPrompt != "" {
				opts.TaskPrompt += "\n\n" + incompleteRetryGuidance
			} else {
				opts.TaskPrompt = incompleteRetryGuidance
			}
		}
		prompt := agent.BuildPrompt(opts)

		taskPath := store.CurrentTaskPath(r.cfg.WorkspaceDir)
		if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
			return outcome(false, false, false), fmt.Errorf("create current_task.md dir: %w", err)
		}
		if err := os.WriteFile(taskPath, []byte(prompt), 0o644); err != nil {
			return outcome(false, false, false), fmt.Errorf("write current_task.md: %w", err)
		}

		tryLogPath := filepath.Join(r.cfg.DataDir, "tries", repoKey(r.cfg.WorkspaceDir), fmt.Sprintf("try-%d.log", tryID))
		_ = os.MkdirAll(filepath.Dir(tryLogPath), 0o755)
		opts.LogPath = tryLogPath

		headBefore, _ := r.headHash()
		startedAt := time.Now().UTC()
		if runStartedAt.IsZero() {
			runStartedAt = startedAt
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
			header := style.RenderHeader(style.HeaderOptions{
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
			})
			fmt.Fprintln(r.outWriter(), header)
		}

		if err := progress.SetActiveTry(r.cfg.WorkspaceDir, progress.ActiveTryMetadata{
			RelayID:   relay.ID,
			RunID:     runIndex + 1,
			TryID:     tryID,
			LogPath:   tryLogPath,
			StartedAt: startedAt,
		}); err != nil {
			trySpan.Finish()
			return outcome(false, false, false), fmt.Errorf("set active try metadata: %w", err)
		}

		kb := keyboard.NewKeyboard(os.Stdin, os.Stdout)
		_ = kb.SetRawMode()
		kbCtx, kbCancel := context.WithCancel(ctx)
		actionCh := kb.Start(kbCtx)

		mon := monitor.NewMonitor(r.cfg.WorkspaceDir, tryLogPath, 0)
		stallController := r.newStallController(tryLogPath, exec)

		// Wire reliability indicators into the monitor.
		stallThreshold := r.cfg.StallThreshold
		if stallThreshold <= 0 {
			stallThreshold = reliability.DefaultStallThreshold
		}
		mon.SetStallThreshold(stallThreshold)
		mon.SetRetry(attempt, maxAttempts)

		initialStatus, _ := mon.Tick()
		// Skip empty/whitespace status to avoid an extra blank line below the
		// header. The control hint line is always shown. Each line is cleared
		// (\r\x1b[2K) before printing so it cleanly overwrites the neutral retry
		// line a prior failing attempt parked here (see renderRunFooter).
		cursorUp := 1
		if strings.TrimSpace(initialStatus) != "" {
			fmt.Printf("\r\x1b[2K%s\n", initialStatus)
			cursorUp = 2
		}
		fmt.Printf("\r\x1b[2K%s\n", style.ShortcutHint())
		mon.SetCursorUpLines(cursorUp)
		mon.Start(os.Stdout)

		tryCh := make(chan tryResult, 1)
		attemptCtx, cancelAttempt := context.WithCancel(ctx)
		defer cancelAttempt()
		pidCh := make(chan int, 1)
		opts.OnStart = func(pid int) {
			select {
			case pidCh <- pid:
			default:
			}
		}
		go func() {
			res, err := r.executeTry(attemptCtx, picked, opts)
			tryCh <- tryResult{res, err}
		}()

		stallTicker := time.NewTicker(stallCheckInterval)
		// Per-attempt cap: a fresh timer each attempt so it bounds this single
		// attempt without consuming the shared run budget (runBudgetCh). A
		// non-positive cap leaves tryDeadline nil, disabling the per-try bound.
		var tryDeadline <-chan time.Time
		var stopTryTimer func() bool
		if tryTimeout > 0 {
			tryDeadline, stopTryTimer = r.newBoundTimer(tryTimeout)
		}
		loopOut := r.runActionLoop(actionLoopDeps{
			tryCh:           tryCh,
			pidCh:           pidCh,
			actionCh:        actionCh,
			stallTick:       stallTicker.C,
			runBudgetCh:     runBudgetCh,
			tryDeadline:     tryDeadline,
			attemptCtx:      attemptCtx,
			cancelAttempt:   cancelAttempt,
			stallController: stallController,
			mon:             mon,
			onStall:         onStall,
			log:             log,
			relayID:         relay.ID,
			runIndex:        runIndex,
			attempt:         attempt,
			harness:         picked.Harness,
		})
		stallTicker.Stop()
		if stopTryTimer != nil {
			stopTryTimer()
		}

		result := loopOut.result
		execErr := loopOut.execErr
		actionTaken := loopOut.actionTaken
		if loopOut.stallTriggered {
			stallMarked = true
		}
		// A wall-clock timeout (run budget or per-try cap) cancelled the attempt.
		// timedOut keeps this distinct from a stall (silence) or an ordinary
		// agent error in the classification below; runBudgetExhausted decides
		// whether the run stops retrying (and hands off, task 4) or may retry.
		timedOut := loopOut.timedOut
		runBudgetExhausted := loopOut.timedOut && loopOut.runBudgetExhausted

		mon.Stop()
		kbCancel()
		_ = kb.Stop()

		endedAt := time.Now().UTC()

		headAfter, _ := r.headHash()

		normalizedSummary := r.normalizeFinalSnippet(runID, tryLogPath, summaryEntryCountBeforeRun, result, execErr)
		if result == nil {
			result = &agent.TryResult{}
		}
		result.Summary = normalizedSummary

		runStateAfter, _ := progress.LoadRunState(r.cfg.WorkspaceDir)
		recordedLaps := []string{}
		lapsAttempted := []store.LapAttempt{}
		handoffState := 0
		if runStateAfter != nil {
			recordedLaps = append(recordedLaps, runStateAfter.RecordedLaps...)
			lapsAttempted = append(lapsAttempted, storeLapAttempts(runStateAfter.LapsAttempted)...)
			handoffState = runStateAfter.HandoffState
		}
		runEntry := recordedRunEntryForRun(r.cfg.WorkspaceDir, runID, summaryEntryCountBeforeRun)
		if task.IsLapsBacked && runEntry != nil {
			recordedLaps = mergeStrings(recordedLaps, progressRunEntryLapIDs(*runEntry))
		}
		handoffEntry := handoffEntryFromRunEntry(runEntry)
		recoveryClassification := recoveryClassificationForRun(task, runEntry)

		runtime := endedAt.Sub(startedAt)
		runRuntime := runtime
		if !runStartedAt.IsZero() {
			runRuntime = endedAt.Sub(runStartedAt)
		}
		commitHash := ""
		commitHistory := []string{}
		preCommitFilesChanged := r.filesChangedList(result, headBefore, headAfter, "")
		dirtyBeforeAutoCommit, _ := gitx.IsWorkspaceDirty(r.cfg.WorkspaceDir)
		dirtyAfter, _ := gitx.WorkspaceDirtyPaths(r.cfg.WorkspaceDir)
		hasOwnUncommittedChanges := hasDirtyChangesSince(runStartDirtySnapshot, dirtyAfter)
		finalized := !task.IsLapsBacked || len(recordedLaps) > 0 || handoffEntry != nil || handoffState != 0 || (task.LapID == "" && result != nil && result.Completed)
		hasUserFileChanges := len(preCommitFilesChanged) > 0
		incomplete := task.IsLapsBacked && hasOwnUncommittedChanges && !finalized
		dirtyHandoff := handoffEntry != nil && hasOwnUncommittedChanges
		if headBefore != "" && headAfter != "" && headBefore != headAfter {
			commitHistory = r.commitRange(headBefore, headAfter)
			if len(commitHistory) == 0 {
				commitHistory = []string{headAfter}
			}
			commitHash = commitHistory[len(commitHistory)-1]
		} else if dirtyBeforeAutoCommit && hasUserFileChanges && !incomplete && !dirtyHandoff && finalized {
			hash, commitErr := r.autoCommit(runIndex, picked.Harness, attempt)
			if commitErr != nil {
				fmt.Fprintf(log, "relay %d run %d attempt %d auto-commit warning: %v\n", relay.ID, runIndex+1, attempt, commitErr)
			} else if hash != "" {
				commitHash = hash
				commitHistory = []string{hash}
			}
		}

		filesChangedList := preCommitFilesChanged
		if commitHash != "" {
			filesChangedList = r.filesChangedList(result, headBefore, headAfter, commitHash)
		}
		filesChangedCount := len(filesChangedList)

		// Fold Rally's own bookkeeping (state + the summary.jsonl append) into
		// this attempt's commit when it produced one, so the summary lands inside
		// that commit rather than trailing it as a separate `rally: update state`
		// commit or a leftover working-tree change. With no commit, fall back to
		// the no-commit insurance path. Done here — after filesChangedList is
		// computed but before the footer/try record — so reported hashes are the
		// amended hash while filesChanged still excludes folded rally state.
		if commitHash != "" {
			if newHash, foldErr := gitx.FoldRallyStateIntoHead(r.cfg.WorkspaceDir); foldErr != nil {
				fmt.Fprintf(log, "relay %d run %d attempt %d rally state fold warning: %v\n", relay.ID, runIndex+1, attempt, foldErr)
			} else if newHash != "" && newHash != commitHash {
				if len(commitHistory) > 0 && commitHistory[len(commitHistory)-1] == commitHash {
					commitHistory[len(commitHistory)-1] = newHash
				}
				commitHash = newHash
			}
		} else if foldErr := gitx.FoldRallyState(r.cfg.WorkspaceDir); foldErr != nil {
			fmt.Fprintf(log, "relay %d run %d attempt %d rally state fold warning: %v\n", relay.ID, runIndex+1, attempt, foldErr)
		}

		shortHash := ""
		commitTitle := ""
		if len(commitHash) >= 7 {
			shortHash = commitHash[:7]
			cmd := osexec.Command("git", "log", "-1", "--pretty=%s", commitHash)
			cmd.Dir = r.cfg.WorkspaceDir
			if out, err := cmd.Output(); err == nil {
				commitTitle = strings.TrimSpace(string(out))
			}
		} else if commitHash != "" {
			shortHash = commitHash
		}

		// Operator cancellation short-circuit: when the action loop recorded
		// a cancellation source (skip / graceful_stop / quit_now), the attempt
		// is classified as OutcomeCancelled without entering the normal failure
		// taxonomy, retry scheduling, or resilience counter updates. The
		// cancelled try is persisted with its source and the attempt loop breaks
		// immediately; route semantics (skip advances, stop/quit halts) are
		// preserved by the existing skipFlag/stopFlag handling after the break.
		cancellationSource := loopOut.cancellationSource
		if cancellationSource != CancellationSourceNone {
			cancellationSourceValue := cancellationSource.String()
			attemptOutcome := reliability.OutcomeCancelled
			failReason = "cancelled (" + cancellationSourceValue + ")"

			// Capture session id before it goes out of scope — cancelled
			// attempts may still carry a resumable session that downstream
			// handling (bounded handoff continuation) needs.
			if result != nil && result.SessionID != "" {
				sessionID = result.SessionID
			}

			// Render a terminal footer for the cancelled attempt. The persisted
			// outcome/source below are the source of truth; the style layer owns
			// the muted cancelled presentation.
			renderRunFooter(r.outWriter(), style.FooterOptions{
				Cancelled:          true,
				Duration:           runRuntime,
				FilesChanged:       filesChangedCount,
				CommitHash:         shortHash,
				CommitTitle:        commitTitle,
				FailReason:         failReason,
				CancellationSource: cancellationSourceValue,
				Interim:            false,
				Attempt:            attempt,
				MaxAttempts:        maxAttempts,
			})

			tryRecord := store.TryRecord{
				ID:                     tryID,
				RunID:                  runIndex + 1,
				RelayID:                relay.ID,
				AgentType:              picked.Harness,
				Completed:              false,
				Outcome:                attemptOutcome,
				CancellationSource:     cancellationSourceValue,
				ResolvedRoute:          task.ResolvedRoute,
				RecoveryClassification: recoveryClassification,
				Summary:                "",
				RemainingWork:          "",
				FilesChanged:           filesChangedList,
				CommitHash:             commitHash,
				CommitHistory:          commitHistory,
				StartedAt:              startedAt.Format(time.RFC3339),
				EndedAt:                endedAt.Format(time.RFC3339),
				AttemptNumber:          attempt,
				LogPath:                tryLogPath,
				FailReason:             failReason,
				RuntimeMs:              runtime.Milliseconds(),
				LapID:                  task.LapID,
				LapAssignee:            task.Assignee,
				RecordedLaps:           recordedLaps,
				LapsAttempted:          lapsAttempted,
			}
			if result != nil {
				tryRecord.Summary = result.Summary
				tryRecord.RemainingWork = result.RemainingWork
				tryRecord.ToolCalls = result.ToolCalls
				if len(result.FilesChanged) > 0 {
					tryRecord.FilesChanged = result.FilesChanged
				}
			}
			fmt.Fprintf(log, "relay %d run %d attempt %d cancelled: source=%q runtime=%s files_changed=%d commit=%q lap_id=%q assignee=%q\n",
				relay.ID, runIndex+1, attempt, cancellationSourceValue, runtime, filesChangedCount, shortHash, task.LapID, task.Assignee)

			// Telemetry: span tags + structured log, but NO failure capture and
			// NO issue-worthy classification — this is a deliberate operator
			// action, not a fault.
			tryTags := telemetry.Tags(telemetry.EventInfo{
				RelayID:  relay.ID,
				RunID:    runIndex + 1,
				TryID:    tryRecord.ID,
				Role:     task.promptAssignee(),
				Harness:  picked.Harness,
				Model:    resolvedRunnerModel(result, picked),
				Repo:     rc.Repo,
				RepoName: rc.RepoName,
				LapID:    task.LapID,
			})
			applyTags(trySpan, tryTags)
			trySpan.SetTag("outcome", string(attemptOutcome))
			trySpan.SetTag("cancellation_source", cancellationSourceValue)
			trySpan.SetData("completed", false)
			trySpan.SetData("outcome", string(attemptOutcome))
			trySpan.SetData("cancellation_source", cancellationSourceValue)
			trySpan.Finish()

			resolvingOutcome = attemptOutcome
			if err := r.store.AppendTry(tryRecord); err != nil {
				return outcome(false, false, false), err
			}
			if err := progress.ClearActiveTry(r.cfg.WorkspaceDir); err != nil {
				return outcome(false, false, false), fmt.Errorf("clear active try metadata: %w", err)
			}
			r.tel().EmitTryLog(tryCtx, map[string]interface{}{
				"event":               "try",
				"relay_id":            relay.ID,
				"run_id":              runIndex + 1,
				"try_id":              tryRecord.ID,
				"attempt":             attempt,
				"role":                task.promptAssignee(),
				"runner":              telemetry.RunnerLabel(picked.Harness, resolvedRunnerModel(result, picked)),
				"repo":                rc.Repo,
				"repo_name":           rc.RepoName,
				"lap_id":              task.LapID,
				"completed":           false,
				"outcome":             string(attemptOutcome),
				"cancellation_source": cancellationSourceValue,
				"runtime_ms":          runtime.Milliseconds(),
				"files_changed":       filesChangedCount,
				"tool_calls":          tryRecord.ToolCalls,
			})

			// Cancelled is terminal for the attempt loop. The route semantics
			// (skip → advance, stop/quit → halt relay) are preserved because
			// skipFlag and stopFlag were set by the action loop and are checked
			// by the actionTaken handling below and the Run() dispatch loop.
			lastResult = result
			break attemptLoop
		}

		// Compute failed before rendering the footer so the displayed result
		// matches what gets recorded in the try record.
		failed := false
		failReason = ""
		attemptFailureClass := reliability.FailureAgent
		if timedOut {
			// A timeout takes precedence over the execErr/agent-error branches: the
			// cancelled attempt typically surfaces a context-cancelled execErr, but
			// that is a consequence of the timeout, not a harness fault. Classify it
			// as a non-freezing run_timeout attempt (see attemptOutcome override and
			// the classifier-bypass below) rather than "harness error".
			failed = true
			if runBudgetExhausted {
				failReason = "run timeout"
			} else {
				failReason = "try timeout"
			}
		} else if incomplete {
			failed = true
			failReason = reliability.CategoryDisplayLabel(reliability.CategoryIncompleteFinalization)
			attemptFailureClass = reliability.FailureIncomplete
		} else if execErr != nil {
			failed = true
			failReason = "harness error"
		} else if result == nil || !result.Completed {
			failed = true
			failReason = "agent error"
		} else {
			hasChanges := commitHash != "" || filesChangedCount > 0
			if !hasChanges {
				dirty, _ := gitx.IsWorkspaceDirty(r.cfg.WorkspaceDir)
				hasChanges = dirty
			}
			noFileChanges := !hasChanges
			if noFileChanges && runtime < 3*time.Minute && handoffEntry == nil {
				failed = true
				failReason = "no changes made"
			}
		}

		// Detect agents that emit "laps done" / "laps handoff" as text instead of
		// invoking the shell command. Symptom: the lap hooks never updated
		// RecordedLaps or HandoffState, yet the summary contains the literal marker.
		markerAsText := ""
		if task.IsLapsBacked && len(recordedLaps) == 0 && handoffState == 0 && result != nil {
			markerAsText = detectLapsMarkerInText(result.Summary)
			if markerAsText != "" {
				if !failed {
					failed = true
					failReason = fmt.Sprintf("%s emitted as text, hook never ran", markerAsText)
				}
				fmt.Fprintf(log, "relay %d run %d attempt %d laps-marker-as-text: agent wrote %q in summary but did not invoke the shell command (no hook fired, tool_calls=%d). Likely a model/harness output-vs-tool-call mismatch.\n", relay.ID, runIndex+1, attempt, markerAsText, result.ToolCalls)
			}
		}
		lapPinMismatch := false
		if task.IsLapsBacked {
			if reason, mismatch := validatePinnedLap(task.LapID, recordedLaps); mismatch {
				failReason = reason
				lapPinMismatch = true
				runLapPinMismatch = true
				attemptFailureClass = reliability.FailureAgent
				failureClass = reliability.FailureAgent
				failureCategory = ""
				resetEvidence = nil
				failed = false
				if pinnedLapCompleteElsewhere(r.cfg.WorkspaceDir, runID, task.LapID, recordedLaps) {
					fmt.Fprintf(log, "relay %d run %d attempt %d lap pin mismatch warning: pinned_lap=%q consumed_laps=%v reason=%s pinned_lap_already_complete=true\n", relay.ID, runIndex+1, attempt, task.LapID, recordedLaps, reason)
				} else {
					fmt.Fprintf(log, "relay %d run %d attempt %d lap pin mismatch warning: pinned_lap=%q consumed_laps=%v reason=%s\n", relay.ID, runIndex+1, attempt, task.LapID, recordedLaps, reason)
				}
			}
		}
		// Stall recovery: if the stall detector killed the process but the agent had
		// already committed or created files (autoCommit ran), treat the try as
		// successful. This handles agents (e.g. opencode TUI) that complete the
		// task then idle in an interactive loop until the stall detector kills them.
		// VERIFY runs are excluded: a trivial commit is not evidence verification happened.
		if failed && stallMarked && commitHash != "" && !lapPinMismatch {
			if strings.EqualFold(task.Assignee, "verify") {
				fmt.Fprintf(log, "relay %d run %d attempt %d stall recovery: files committed but assignee is %s, not treating as success\n", relay.ID, runIndex+1, attempt, task.Assignee)
			} else {
				failed = false
				success = true
				fmt.Fprintf(log, "relay %d run %d attempt %d stall recovery: files committed, treating as success\n", relay.ID, runIndex+1, attempt)
			}
		}

		// A run-budget exhaustion only yields a separate handoff-only continuation
		// when the harness can resume into the captured session. Capture this
		// attempt's session id (the cancelled attempt may still carry it) before
		// deciding. When no resumable session exists, the budget-cancelled
		// implementation try is itself the resolving try and is labelled
		// handoff_timeout (task 4.3) rather than run_timeout, so recovery routing
		// has a persisted resolving record even without a continuation.
		if result != nil && result.SessionID != "" {
			sessionID = result.SessionID
		}
		canHandoffResume := runBudgetExhausted && exec != nil && exec.ResumeSupported() && sessionID != ""

		hasFailureEvidence := result != nil && result.Evidence != nil && result.Evidence.Category != ""
		attemptOutcome := tryOutcomeForAttempt(failed, incomplete && !hasFailureEvidence, actionTaken && r.stopFlag.Load(), handoffEntry != nil)
		// A timed-out attempt (run budget or per-try cap) records a non-freezing
		// run_timeout outcome: it carries no FailureCategory (so the classifier
		// below is skipped), is not an Issue, and is not terminal by the outcome
		// itself. Whether the run stops (to hand off — task 4) or retries is
		// decided by runBudgetExhausted at the loop bottom, not by this outcome.
		// Guarded on `failed` so a stall-recovery success above is not relabelled.
		if timedOut && failed {
			attemptOutcome = reliability.OutcomeRunTimeout
			if runBudgetExhausted && !canHandoffResume {
				attemptOutcome = reliability.OutcomeHandoffTimeout
				failReason = noHandoffResumeReason(exec, sessionID)
			}
		}

		// Error classification and strategy dispatch.
		//
		// terminalCategory marks usage_limit / auth_or_proxy: categories whose
		// non-infra (agent-class) mapping means the freeze counter never bounds
		// them, so the attempt-loop break below is the only thing that bounds
		// them to a single attempt. On detection the loop terminates and control
		// returns to the routing dispatch loop, which benches the quota scope or
		// routes the entry away using the surfaced category + reset evidence.
		terminalForRun := attemptOutcome.IsTerminalForRun("")
		var decisionEvidence *reliability.FailureEvidence
		if failed && !lapPinMismatch && attemptOutcome.CarriesFailureCategory() {
			logLines := readLastNLines(tryLogPath, 50)
			decision := reliability.ClassifyError(logLines, picked.Harness, &reliability.ClassifyContext{
				HasFileChanges: incomplete,
				Finalized:      finalized,
				ChangedPaths:   filesChangedList,
			}, result.Evidence)
			decisionEvidence = decision.Evidence
			attemptFailureClass = decision.FailureClass
			failureClass = decision.FailureClass
			failureCategory = decision.Category
			resetEvidence = result.Evidence
			if resetEvidence == nil {
				resetEvidence = decision.Evidence
			}
			terminalForRun = attemptOutcome.IsTerminalForRun(decision.Category)
			if decision.FailureClass == reliability.FailureInfra {
				infraFailures++
			}
			if decision.Reason != "unknown error" && markerAsText == "" {
				failReason = formatCategorizedDisplay(failureCategory, decision.Cooldown, resetEvidence)
			}
			switch decision.Strategy {
			case reliability.StrategyNoOp:
				failed = false
				success = true
				attemptOutcome = tryOutcomeForAttempt(false, false, actionTaken && r.stopFlag.Load(), handoffEntry != nil)
				terminalForRun = false
				failureCategory = ""
			case reliability.StrategyRotate:
				r.skipFlag.Store(true)
			case reliability.StrategyWaitResume:
				// A terminal category never resumes within budget, so don't burn
				// the cooldown only to break the loop immediately afterward.
				if !terminalForRun && attempt < maxAttempts && decision.Cooldown > 0 {
					cooldown := decision.Cooldown
					if !runDeadline.IsZero() {
						remaining := time.Until(runDeadline)
						if remaining <= 0 {
							cooldown = 0
							runBudgetExhausted = true
						} else if cooldown >= remaining {
							cooldown = remaining
							runBudgetExhausted = true
						}
					}
					if cooldown > 0 {
						fmt.Println(style.DimStyle.Render(fmt.Sprintf("waiting %v for rate limit...", cooldown)))
						if r.sleepFunc != nil {
							r.sleepFunc(cooldown)
						} else {
							time.Sleep(cooldown)
						}
					}
				}
			case reliability.StrategyFreshRestart:
				if attempt < maxAttempts {
					sessionID = ""
				}
			}
		} else {
			attemptFailureClass = attemptOutcome.FailureClass("")
			failureClass = attemptFailureClass
			if !attemptOutcome.CarriesFailureCategory() {
				failureCategory = ""
			}
		}
		if failed && runBudgetExhausted {
			canHandoffResume = exec != nil && exec.ResumeSupported() && sessionID != ""
			attemptOutcome = reliability.OutcomeRunTimeout
			failReason = "run timeout"
			// A run-budget kill carries a non-empty category unless an
			// authoritative Category was already produced by the classifier
			// (decision.Category from the block above, e.g. a text-pattern
			// agent_error or dirty-tree incomplete_finalization) or by
			// executor/session/disk-log evidence. Empty = no telemetry signal,
			// so fall back to the non-freezing unidentified_issue floor.
			if runBudgetExhausted && failureCategory == "" && (result.Evidence == nil || result.Evidence.Category == "") {
				failureCategory = reliability.CategoryUnidentifiedIssue
			}
			attemptFailureClass = reliability.FailureAgent
			failureClass = attemptFailureClass
			terminalForRun = false
			if !canHandoffResume {
				attemptOutcome = reliability.OutcomeHandoffTimeout
				failReason = noHandoffResumeReason(exec, sessionID)
			}
		}
		// Try-cap-only kill (per-try deadline fired, run budget remains):
		// ClassifyError was skipped (attemptOutcome = OutcomeRunTimeout, whose
		// CarriesFailureCategory() is false), so failureCategory would
		// otherwise stay empty. Give it the non-freezing agent-class
		// unidentified_issue floor — unless executor/session/disk-log evidence
		// already produced an authoritative Category.
		if failed && timedOut && !runBudgetExhausted && failureCategory == "" && (result.Evidence == nil || result.Evidence.Category == "") {
			failureCategory = reliability.CategoryUnidentifiedIssue
			failReason = "try budget exhausted; no output"
			failureClass = reliability.FailureAgent
			attemptFailureClass = reliability.FailureAgent
		}
		lastAttemptIncomplete = failed && attemptFailureClass == reliability.FailureIncomplete

		// A failing attempt that will be retried within budget is not a terminal
		// outcome: it gets the neutral, in-place retry line rather than a red
		// footer. Exactly one coloured footer prints when the run resolves —
		// green on success, red when the budget is exhausted (or the run breaks
		// out via skip/stop/lap-pin mismatch/terminal category). A single-attempt
		// run is terminal on its first failure, so it colours immediately.
		willRetry := failed && attempt < maxAttempts &&
			!actionTaken && !r.skipFlag.Load() && !lapPinMismatch && !r.stopFlag.Load() &&
			!terminalForRun && !runBudgetExhausted
		footerDuration := runtime
		if !willRetry {
			footerDuration = runRuntime
		}
		renderRunFooter(r.outWriter(), style.FooterOptions{
			Passed:       !failed,
			Duration:     footerDuration,
			FilesChanged: filesChangedCount,
			CommitHash:   shortHash,
			CommitTitle:  commitTitle,
			FailReason:   failReason,
			Interim:      willRetry,
			Attempt:      attempt,
			MaxAttempts:  maxAttempts,
		})

		tryRecord := store.TryRecord{
			ID:                     tryID,
			RunID:                  runIndex + 1,
			RelayID:                relay.ID,
			AgentType:              picked.Harness,
			Completed:              !failed,
			Outcome:                attemptOutcome,
			ResolvedRoute:          task.ResolvedRoute,
			DirtyHandoff:           dirtyHandoff,
			RecoveryClassification: recoveryClassification,
			Summary:                "",
			RemainingWork:          "",
			FilesChanged:           filesChangedList,
			CommitHash:             commitHash,
			CommitHistory:          commitHistory,
			StartedAt:              startedAt.Format(time.RFC3339),
			EndedAt:                endedAt.Format(time.RFC3339),
			AttemptNumber:          attempt,
			LogPath:                tryLogPath,
			FailReason:             failReason,
			Category:               string(failureCategory),
			RuntimeMs:              runtime.Milliseconds(),
			LapID:                  task.LapID,
			LapAssignee:            task.Assignee,
			HandoffCreatedLapIDs:   handoffCreatedLapIDs(handoffEntry),
			RecordedLaps:           recordedLaps,
			LapsAttempted:          lapsAttempted,
		}
		if result != nil {
			tryRecord.Summary = result.Summary
			tryRecord.RemainingWork = result.RemainingWork
			tryRecord.ToolCalls = result.ToolCalls
			if len(result.FilesChanged) > 0 {
				// Prefer the agent-reported list if it gave one.
				tryRecord.FilesChanged = result.FilesChanged
			}
		}
		fmt.Fprintf(log, "relay %d run %d attempt %d result: completed=%v outcome=%q fail_reason=%q runtime=%s files_changed=%d tool_calls=%d commit=%q lap_id=%q assignee=%q recorded_laps=%v laps_attempted=%v handoff_state=%d\n",
			relay.ID, runIndex+1, attempt, !failed, attemptOutcome, failReason, runtime, filesChangedCount, tryRecord.ToolCalls, shortHash, task.LapID, task.Assignee, recordedLaps, lapsAttempted, handoffState)

		// Telemetry: per-try structured log + trace span tags. Only summaries
		// and byte sizes are emitted — never current_task.md contents or the
		// transcript (the scrubber is defense-in-depth on top of this).
		tryTags := telemetry.Tags(telemetry.EventInfo{
			RelayID:  relay.ID,
			RunID:    runIndex + 1,
			TryID:    tryRecord.ID,
			Role:     task.promptAssignee(),
			Harness:  picked.Harness,
			Model:    resolvedRunnerModel(result, picked),
			Repo:     rc.Repo,
			RepoName: rc.RepoName,
			LapID:    task.LapID,
		})
		applyTags(trySpan, tryTags)
		trySpan.SetTag("outcome", string(attemptOutcome))
		trySpan.SetData("completed", !failed)
		trySpan.SetData("outcome", string(attemptOutcome))
		if dirtyHandoff {
			trySpan.SetData("dirty_handoff", true)
		}
		if task.ResolvedRoute == "recovery" && recoveryClassification != "" {
			trySpan.SetTag("recovery_classification", recoveryClassification)
			trySpan.SetData("recovery_classification", recoveryClassification)
		}
		trySpan.SetData("fail_reason", failReason)
		tryLogFields := map[string]interface{}{
			"event":                          "try",
			"relay_id":                       relay.ID,
			"run_id":                         runIndex + 1,
			"try_id":                         tryRecord.ID,
			"attempt":                        attempt,
			"role":                           task.promptAssignee(),
			"runner":                         telemetry.RunnerLabel(picked.Harness, resolvedRunnerModel(result, picked)),
			"repo":                           rc.Repo,
			"repo_name":                      rc.RepoName,
			"lap_id":                         task.LapID,
			"completed":                      !failed,
			"outcome":                        string(attemptOutcome),
			"fail_reason":                    failReason,
			"failure_class":                  string(failureClass),
			"runtime_ms":                     runtime.Milliseconds(),
			"files_changed":                  filesChangedCount,
			"tool_calls":                     tryRecord.ToolCalls,
			"prompt_bytes":                   len(prompt),
			"prompt_recent_context_bytes":    len(opts.RecentTryContext),
			"prompt_previous_summary_bytes":  len(opts.PreviousSummary),
			"prompt_instructions_bytes":      len(opts.Instructions),
			"prompt_role_instructions_bytes": len(opts.RoleInstructions),
			"prompt_task_bytes":              len(opts.TaskPrompt),
			"prompt_inbox_bytes":             len(opts.InboxMessage),
			"prompt_relay_message_bytes":     len(opts.RelayMessage),
		}
		if dirtyHandoff {
			tryLogFields["dirty_handoff"] = true
		}
		if task.ResolvedRoute == "recovery" && recoveryClassification != "" {
			tryLogFields["recovery_classification"] = recoveryClassification
		}
		evidenceState := telemetry.FailureState{
			Attempt:                attempt,
			MaxAttempts:            maxAttempts,
			Outcome:                string(attemptOutcome),
			Category:               string(failureCategory),
			RecoveryClassification: recoveryClassification,
			AgentState:             r.agentStateName(picked),
		}
		applyEvidenceToFailureState(&evidenceState, resetEvidence, "executor_evidence")
		if result.Evidence == nil && decisionEvidence == nil {
			applySafeExecErrorEvidence(&evidenceState, execErr)
		}
		if failed {
			addFailureEvidenceTelemetry(trySpan, tryLogFields, evidenceState)
		}
		if timedOut {
			timeoutKind := "try_cap"
			timeoutBudget := tryTimeout
			if runBudgetExhausted {
				timeoutKind = "run_budget"
				timeoutBudget = r.cfg.RunTimeout
			}
			trySpan.SetTag("timeout_kind", timeoutKind)
			trySpan.SetData("timeout_kind", timeoutKind)
			tryLogFields["timeout_kind"] = timeoutKind
			if timeoutBudget > 0 {
				trySpan.SetData("timeout_budget_ms", timeoutBudget.Milliseconds())
				tryLogFields["timeout_budget_ms"] = timeoutBudget.Milliseconds()
			}
			if age, ok := lastOutputAge(tryLogPath, endedAt); ok {
				trySpan.SetData("last_output_age_ms", age.Milliseconds())
				tryLogFields["last_output_age_ms"] = age.Milliseconds()
			}
			sessionCaptured := sessionID != ""
			resumeSupported := exec != nil && exec.ResumeSupported()
			handoffOnlyAttempted := runBudgetExhausted && canHandoffResume
			trySpan.SetData("session_captured", sessionCaptured)
			trySpan.SetData("resume_supported", resumeSupported)
			trySpan.SetData("handoff_only_attempted", handoffOnlyAttempted)
			tryLogFields["session_captured"] = sessionCaptured
			tryLogFields["resume_supported"] = resumeSupported
			tryLogFields["handoff_only_attempted"] = handoffOnlyAttempted
			if handoffOnlyAttempted {
				handoffOnlyTryID := tryRecord.ID + 1
				trySpan.SetData("handoff_only_try_id", handoffOnlyTryID)
				tryLogFields["handoff_only_try_id"] = handoffOnlyTryID
			}
			if runBudgetExhausted && !canHandoffResume {
				blocker := noHandoffResumeReason(exec, sessionID)
				trySpan.SetData("handoff_resume_blocker", blocker)
				tryLogFields["handoff_resume_blocker"] = blocker
			}
		}
		if lapPinMismatch {
			trySpan.SetTag("event_kind", "lap_pin_mismatch")
			trySpan.SetData("mismatch_reason", failReason)
			tryLogFields["event_kind"] = "lap_pin_mismatch"
			tryLogFields["mismatch_reason"] = failReason
			tryLogFields["expected_lap_id"] = task.LapID
			tryLogFields["consumed_lap_count"] = len(recordedLaps)
			if len(recordedLaps) > 0 {
				tryLogFields["consumed_lap_ids"] = strings.Join(recordedLaps, ",")
			}
			fs := telemetry.FailureState{
				Attempt:     attempt,
				MaxAttempts: maxAttempts,
				Outcome:     string(attemptOutcome),
				AgentState:  r.agentStateName(picked),
			}
			r.tel().CaptureEvent(tryCtx, fmt.Sprintf("relay %d run %d try %d lap pin mismatch: %s", relay.ID, runIndex+1, tryRecord.ID, failReason),
				lapPinMismatchDiagnosticEvent(tryTags, rc, fs, failReason, task.LapID, recordedLaps))
		}
		// Capture provider-limit evidence as low-severity diagnostic telemetry
		// regardless of whether the failure is operator-worthy enough to become an
		// operator failure. This builds the parser-validation corpus without
		// broadening alerts.
		if failed && attemptOutcome.ShouldCaptureIssue() {
			fs := telemetry.FailureState{
				Attempt:                attempt,
				MaxAttempts:            maxAttempts,
				Outcome:                string(attemptOutcome),
				Category:               string(failureCategory),
				RecoveryClassification: recoveryClassification,
				AgentState:             r.agentStateName(picked),
			}
			if resetEvidence != nil {
				applyEvidenceToFailureState(&fs, resetEvidence, "executor_evidence")
			}
			if result.Evidence == nil && decisionEvidence == nil {
				applySafeExecErrorEvidence(&fs, execErr)
			}
			if evt, ok := limitSignalEvent(tryTags, rc, fs); ok {
				r.tel().CaptureEvent(tryCtx, fmt.Sprintf("relay %d run %d try %d provider limit signal: %s", relay.ID, runIndex+1, tryRecord.ID, failReason), evt)
			}

			// Capture operator-worthy failures. Ordinary
			// agent-class retries (recoverable agent errors, short no-ops) stay
			// spans/logs only to avoid alert noise.
			issueWorthy := !lapPinMismatch &&
				(failureClass == reliability.FailureInfra ||
					execErr != nil ||
					markerAsText != "" ||
					strings.Contains(strings.ToLower(failReason), "panic"))
			if issueWorthy {
				// Enrich the terminal-try capture with the structured failure
				// state already resolved above: attempt/budget, the resolved
				// failure category, the failing runner's resilience standing, and
				// — for provider-limit categories — the parsed quota scope/reset
				// and bounded raw provider signal from TryResult.Evidence.
				r.tel().CaptureFailure(tryCtx, fmt.Sprintf("relay %d run %d try %d failed: %s", relay.ID, runIndex+1, tryRecord.ID, failReason), failureStateEvent(tryTags, rc, fs))
			}
		}
		if task.ResolvedRoute == "recovery" && recoveryClassification == "needs_user" {
			fs := telemetry.FailureState{
				Attempt:                attempt,
				MaxAttempts:            maxAttempts,
				Outcome:                string(attemptOutcome),
				RecoveryClassification: recoveryClassification,
				AgentState:             r.agentStateName(picked),
			}
			r.tel().CaptureFailure(tryCtx, fmt.Sprintf("relay %d run %d try %d recovery needs_user", relay.ID, runIndex+1, tryRecord.ID), failureStateEvent(tryTags, rc, fs))
		}
		trySpan.Finish()

		resolvingOutcome = attemptOutcome
		resolvingDirtyHandoff = dirtyHandoff
		if err := r.store.AppendTry(tryRecord); err != nil {
			return outcome(false, false, false), err
		}
		if err := progress.ClearActiveTry(r.cfg.WorkspaceDir); err != nil {
			return outcome(false, false, false), fmt.Errorf("clear active try metadata: %w", err)
		}
		r.tel().EmitTryLog(tryCtx, tryLogFields)

		if actionTaken {
			if r.stopFlag.Load() {
				return outcome(false, false, true), nil
			}
			if r.skipFlag.Load() {
				return outcome(false, false, false), nil
			}
			fmt.Println("Paused — press Enter to resume")
			bufio.NewReader(os.Stdin).ReadString('\n')
			if result != nil {
				previousSummary = result.Summary
				lastResult = result
				if result.SessionID != "" {
					sessionID = result.SessionID
				}
			} else {
				previousSummary = ""
				lastResult = &agent.TryResult{Completed: false}
			}
			continue
		}

		if !failed {
			if stallMarked && onStallRecovered != nil {
				onStallRecovered()
				mon.SetStalled(false)
				mon.SetRecovered()
				stallMarked = false
			}
			success = true
			lastResult = result
			break attemptLoop
		}

		if r.skipFlag.Load() {
			break attemptLoop
		}
		if lapPinMismatch {
			break attemptLoop
		}
		// usage_limit / auth_or_proxy are bounded to a single attempt: break here
		// so the routing dispatch loop can bench the quota scope or route away
		// rather than burning the retry budget on a quota/auth failure that a
		// retry cannot clear.
		if terminalForRun {
			lastResult = result
			break attemptLoop
		}
		// Run-budget exhaustion is terminal for the normal implementation loop,
		// even though OutcomeRunTimeout is deliberately NOT terminal in
		// IsTerminalForRun (a per-try cap with budget remaining must still be able
		// to retry). Unlike that retryable per-try cap, an exhausted run budget
		// stops retries here and the run proceeds to the bounded handoff-only
		// continuation. That continuation is owned by the next lap (task 4); until
		// it lands, breaking here resolves the run on the persisted run_timeout
		// attempt rather than burning more of an already-exhausted budget.
		if runBudgetExhausted {
			lastResult = result
			// When the harness can resume the captured session, defer to the
			// bounded handoff-only continuation below: the run_timeout try just
			// recorded is observability only, and the continuation (a separate
			// HandoffOnly try) becomes the run resolver. Without a resumable
			// session the run_timeout try was already relabelled handoff_timeout
			// above and is itself the resolving try.
			if canHandoffResume {
				handoffResumePending = true
				handoffResumeSessionID = sessionID
				handoffResumeBaseAttempt = attempt
			}
			break attemptLoop
		}

		if result != nil {
			previousSummary = result.Summary
			lastResult = result
			if result.SessionID != "" {
				sessionID = result.SessionID
			}
		} else {
			previousSummary = ""
			lastResult = &agent.TryResult{Completed: false}
		}
	}

	// Bounded handoff-only continuation (task 4). The run budget was exhausted on
	// a resume-capable harness with a captured session: resume that session once
	// under HandoffTimeout (no stall detector, not counted against the run budget)
	// to capture a clean handoff. This continuation, not the cancelled run_timeout
	// implementation try, resolves the run.
	if handoffResumePending && !r.stopFlag.Load() && ctx.Err() == nil {
		contOutcome, contResult, contSucceeded, contDirtyHandoff, contErr := r.runBoundedHandoffOnly(
			ctx, relay, runIndex, picked, task, rc, roleInstructions,
			handoffResumeSessionID, handoffResumeBaseAttempt+1, maxAttempts,
			summaryEntryCountBeforeRun, runID, runStartDirtySnapshot, log,
		)
		if contErr != nil {
			return outcome(false, false, false), contErr
		}
		resolvingOutcome = contOutcome
		resolvingDirtyHandoff = contDirtyHandoff
		if contResult != nil {
			lastResult = contResult
		}
		if contSucceeded {
			success = true
		}
	}

	// Write stub entry if the agent did not finalize the run.
	stubSummary := ""
	if lastResult != nil {
		stubSummary = lastResult.Summary
	}
	wroteUnfinalized := false
	if resolvingOutcome != reliability.OutcomeCancelled {
		wroteUnfinalized, _ = r.maybeWriteStubAndClearState(stubSummary)
	}
	designedHandoffOutcome := resolvingOutcome == reliability.OutcomeRunTimeout ||
		resolvingOutcome == reliability.OutcomeHandoffTimeout ||
		resolvingOutcome == reliability.OutcomeHandoffRequested ||
		resolvingOutcome == reliability.OutcomeCancelled
	if wroteUnfinalized && !success && !designedHandoffOutcome && !runLapPinMismatch {
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
			Attempt:     lastAttempt,
			MaxAttempts: maxAttempts,
			AgentState:  r.agentStateName(picked),
		}
		dirtyTreeEv := reliability.DirtyTreeEvidence(r.filesChangedList(nil, "", "", ""))
		if resetEvidence != nil && resetEvidence.Source == "dirty_tree" {
			dirtyTreeEv = resetEvidence
		}
		applyEvidenceToFailureState(&fs, dirtyTreeEv, "dirty_tree")
		r.tel().CaptureFailure(ctx, fmt.Sprintf("relay %d run %d: agent exited without finalizing", relay.ID, runIndex+1),
			failureStateEvent(telemetry.Tags(telemetry.EventInfo{
				RelayID:  relay.ID,
				RunID:    runIndex + 1,
				Role:     task.promptAssignee(),
				Harness:  picked.Harness,
				Model:    resolvedRunnerModel(lastResult, picked),
				Repo:     rc.Repo,
				RepoName: rc.RepoName,
				LapID:    task.LapID,
			}), rc, fs))
	}

	addressed := false
	if lastResult != nil && lastResult.MessageAddressed != nil {
		addressed = *lastResult.MessageAddressed
	}
	interrupted := resolvingOutcome == reliability.OutcomeCancelled && r.stopFlag.Load()
	return outcome(success, addressed, interrupted), nil
}

func (r *Runner) executeTry(ctx context.Context, picked agent.ResolvedAgent, opts agent.RunOptions) (*agent.TryResult, error) {
	exec, ok := r.executors[picked.Harness]
	if !ok {
		return nil, fmt.Errorf("no executor for agent %s", picked.Harness)
	}
	return exec.Execute(ctx, opts)
}

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}
