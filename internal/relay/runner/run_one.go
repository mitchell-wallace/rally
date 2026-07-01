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

	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
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

type runOneState struct {
	runID                      string
	rc                         telemetry.RallyContext
	summaryEntryCountBeforeRun int
	inbox                      string
	relayMessage               string
	recentContext              string
	roleInstructions           string
	leftoverWork               bool
	runStartDirtySnapshot      map[string]string
	exec                       harnessapi.Executor
	maxAttempts                int
	runBudgetCh                <-chan time.Time
	runDeadline                time.Time
	tryTimeout                 time.Duration
	runStartedAt               time.Time
	previousSummary            string
	lastResult                 *harnessapi.TryResult
	sessionID                  string
	success                    bool
	failReason                 string
	failureClass               reliability.FailureClass
	failureCategory            reliability.FailureCategory
	resetEvidence              *reliability.FailureEvidence
	resolvingOutcome           reliability.TryOutcome
	resolvingDirtyHandoff      bool
	infraFailures              int
	lastAttemptIncomplete      bool
	stallMarked                bool
	lastAttempt                int
	runLapPinMismatch          bool
	handoffResumePending       bool
	handoffResumeSessionID     string
	handoffResumeBaseAttempt   int
}

func (s *runOneState) outcome(task runTask, succeeded, addressed, interrupted bool) runOutcome {
	return runOutcome{
		Success:       succeeded,
		Addressed:     addressed,
		Interrupted:   interrupted,
		FailReason:    s.failReason,
		FailureClass:  s.failureClass,
		Category:      s.failureCategory,
		Outcome:       s.resolvingOutcome,
		LapID:         task.LapID,
		DirtyHandoff:  s.resolvingDirtyHandoff,
		ResetEvidence: s.resetEvidence,
		InfraFailures: s.infraFailures,
	}
}

type runAttemptState struct {
	attempt                int
	tryID                  int
	tryCtx                 context.Context
	trySpan                telemetry.Span
	cancelAttempt          context.CancelFunc
	opts                   harnessapi.RunOptions
	prompt                 string
	tryLogPath             string
	headBefore             string
	headAfter              string
	startedAt              time.Time
	endedAt                time.Time
	mon                    *monitor.Monitor
	result                 *harnessapi.TryResult
	execErr                error
	actionTaken            bool
	timedOut               bool
	runBudgetExhausted     bool
	runtime                time.Duration
	runRuntime             time.Duration
	recordedLaps           []string
	lapsAttempted          []store.LapAttempt
	handoffState           int
	handoffEntry           *progress.HandoffEntry
	recoveryClassification string
	commitHash             string
	commitHistory          []string
	filesChangedList       []string
	filesChangedCount      int
	finalized              bool
	incomplete             bool
	dirtyHandoff           bool
	shortHash              string
	commitTitle            string
	cancellationSource     CancellationSource
	failed                 bool
	attemptFailureClass    reliability.FailureClass
	markerAsText           string
	lapPinMismatch         bool
	canHandoffResume       bool
	attemptOutcome         reliability.TryOutcome
	terminalForRun         bool
	decisionEvidence       *reliability.FailureEvidence
}

type runOneAttemptAction int

const (
	runOneAttemptContinue runOneAttemptAction = iota
	runOneAttemptBreak
	runOneAttemptReturn
)

type runOneAttemptDecision struct {
	action  runOneAttemptAction
	outcome runOutcome
}

func (r *Runner) runOne(
	ctx context.Context,
	relay *store.RelayRecord,
	runIndex int,
	picked harnessapi.ResolvedAgent,
	task runTask,
	consumedMsg *store.MessageRecord,
	relayMsg *store.MessageRecord,
	isHourlyRetry bool,
	isProbation bool,
	onStall func(),
	onStallRecovered func(),
	log io.Writer,
) (runOutcome, error) {
	state := r.newRunOneState(relay, runIndex, task, consumedMsg, relayMsg)
	roleInstructions, err := r.resolveRoleInstructions(task.promptAssignee())
	if err != nil {
		return state.outcome(task, false, false, false), err
	}
	state.roleInstructions = roleInstructions
	r.captureRunStartWorkspaceState(state, picked)

	if stop := r.setupRunBudget(state, isHourlyRetry, isProbation); stop != nil {
		defer stop()
	}

attemptLoop:
	for attempt := 1; attempt <= state.maxAttempts; attempt++ {
		state.lastAttempt = attempt
		if ctx.Err() != nil {
			return state.outcome(task, false, false, false), ctx.Err()
		}
		if r.stopFlag.Load() {
			return state.outcome(task, false, false, true), nil
		}

		attemptState, err := r.prepareRunAttempt(ctx, relay, runIndex, picked, task, state, attempt)
		if err != nil {
			return state.outcome(task, false, false, false), err
		}

		r.runMonitoredAttempt(ctx, relay, runIndex, picked, state, attemptState, onStall, log)
		defer attemptState.cancelAttempt()
		r.resolveAttemptFinalSnippet(state, attemptState)
		r.reconcileAttemptProgress(relay, runIndex, picked, task, state, attemptState, log)

		cancelled, err := r.recordCancelledAttempt(relay, runIndex, picked, task, state, attemptState, log)
		if err != nil {
			return state.outcome(task, false, false, false), err
		}
		if cancelled {
			break attemptLoop
		}

		r.classifyAttemptOutcome(relay, runIndex, picked, task, state, attemptState, log)

		if err := r.recordAttemptOutcome(relay, runIndex, picked, task, state, attemptState, log); err != nil {
			return state.outcome(task, false, false, false), err
		}

		decision := r.decideRetryOrComplete(task, state, attemptState, onStallRecovered)
		switch decision.action {
		case runOneAttemptReturn:
			return decision.outcome, nil
		case runOneAttemptBreak:
			break attemptLoop
		case runOneAttemptContinue:
			continue
		}
	}

	if err := r.runHandoffContinuation(ctx, relay, runIndex, picked, task, state, log); err != nil {
		return state.outcome(task, false, false, false), err
	}
	r.finalizeRunProgress(ctx, relay, runIndex, picked, task, state)

	addressed := false
	if state.lastResult != nil && state.lastResult.MessageAddressed != nil {
		addressed = *state.lastResult.MessageAddressed
	}
	interrupted := state.resolvingOutcome == reliability.OutcomeCancelled && r.stopFlag.Load()
	return state.outcome(task, state.success, addressed, interrupted), nil
}

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

func (r *Runner) runMonitoredAttempt(ctx context.Context, relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, state *runOneState, attempt *runAttemptState, onStall func(), log io.Writer) {
	kb := keyboard.NewKeyboard(os.Stdin, os.Stdout)
	_ = kb.SetRawMode()
	kbCtx, kbCancel := context.WithCancel(ctx)
	actionCh := kb.Start(kbCtx)

	mon := monitor.NewMonitor(r.cfg.WorkspaceDir, attempt.tryLogPath, 0)
	attempt.mon = mon
	stallController := r.newStallController(attempt.tryLogPath, state.exec)

	// Wire reliability indicators into the monitor.
	stallThreshold := r.cfg.StallThreshold
	if stallThreshold <= 0 {
		stallThreshold = reliability.DefaultStallThreshold
	}
	mon.SetStallThreshold(stallThreshold)
	mon.SetRetry(attempt.attempt, state.maxAttempts)

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
	attempt.cancelAttempt = cancelAttempt
	pidCh := make(chan int, 1)
	attempt.opts.OnStart = func(pid int) {
		select {
		case pidCh <- pid:
		default:
		}
	}
	go func() {
		res, err := r.executeTry(attemptCtx, picked, attempt.opts)
		tryCh <- tryResult{res, err}
	}()

	stallTicker := time.NewTicker(stallCheckInterval)
	// Per-attempt cap: a fresh timer each attempt so it bounds this single
	// attempt without consuming the shared run budget (runBudgetCh). A
	// non-positive cap leaves tryDeadline nil, disabling the per-try bound.
	var tryDeadline <-chan time.Time
	var stopTryTimer func() bool
	if state.tryTimeout > 0 {
		tryDeadline, stopTryTimer = r.newBoundTimer(state.tryTimeout)
	}
	loopOut := r.runActionLoop(actionLoopDeps{
		tryCh:           tryCh,
		pidCh:           pidCh,
		actionCh:        actionCh,
		stallTick:       stallTicker.C,
		runBudgetCh:     state.runBudgetCh,
		tryDeadline:     tryDeadline,
		attemptCtx:      attemptCtx,
		cancelAttempt:   cancelAttempt,
		stallController: stallController,
		mon:             mon,
		onStall:         onStall,
		log:             log,
		relayID:         relay.ID,
		runIndex:        runIndex,
		attempt:         attempt.attempt,
		harness:         picked.Harness,
	})
	stallTicker.Stop()
	if stopTryTimer != nil {
		stopTryTimer()
	}

	attempt.result = loopOut.result
	attempt.execErr = loopOut.execErr
	attempt.actionTaken = loopOut.actionTaken
	if loopOut.stallTriggered {
		state.stallMarked = true
	}
	// A wall-clock timeout (run budget or per-try cap) cancelled the attempt.
	// timedOut keeps this distinct from a stall (silence) or an ordinary
	// agent error in the classification below; runBudgetExhausted decides
	// whether the run stops retrying (and hands off, task 4) or may retry.
	attempt.timedOut = loopOut.timedOut
	attempt.runBudgetExhausted = loopOut.timedOut && loopOut.runBudgetExhausted
	attempt.cancellationSource = loopOut.cancellationSource

	mon.Stop()
	kbCancel()
	_ = kb.Stop()

	attempt.endedAt = time.Now().UTC()
	attempt.headAfter, _ = r.headHash()
}

func (r *Runner) resolveAttemptFinalSnippet(state *runOneState, attempt *runAttemptState) {
	normalizedSummary := r.normalizeFinalSnippet(state.runID, attempt.tryLogPath, state.summaryEntryCountBeforeRun, attempt.result, attempt.execErr)
	if attempt.result == nil {
		attempt.result = &harnessapi.TryResult{}
	}
	attempt.result.Summary = normalizedSummary
}

func (r *Runner) reconcileAttemptProgress(relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, task runTask, state *runOneState, attempt *runAttemptState, log io.Writer) {
	runStateAfter, _ := progress.LoadRunState(r.cfg.WorkspaceDir)
	recordedLaps := []string{}
	lapsAttempted := []store.LapAttempt{}
	handoffState := 0
	if runStateAfter != nil {
		recordedLaps = append(recordedLaps, runStateAfter.RecordedLaps...)
		lapsAttempted = append(lapsAttempted, storeLapAttempts(runStateAfter.LapsAttempted)...)
		handoffState = runStateAfter.HandoffState
	}
	runEntry := recordedRunEntryForRun(r.cfg.WorkspaceDir, state.runID, state.summaryEntryCountBeforeRun)
	if task.IsLapsBacked && runEntry != nil {
		recordedLaps = mergeStrings(recordedLaps, progressRunEntryLapIDs(*runEntry))
	}
	handoffEntry := handoffEntryFromRunEntry(runEntry)
	recoveryClassification := recoveryClassificationForRun(task, runEntry)

	runtime := attempt.endedAt.Sub(attempt.startedAt)
	runRuntime := runtime
	if !state.runStartedAt.IsZero() {
		runRuntime = attempt.endedAt.Sub(state.runStartedAt)
	}
	commitHash := ""
	commitHistory := []string{}
	preCommitFilesChanged := r.filesChangedList(attempt.result, attempt.headBefore, attempt.headAfter, "")
	dirtyBeforeAutoCommit, _ := gitx.IsWorkspaceDirty(r.cfg.WorkspaceDir)
	dirtyAfter, _ := gitx.WorkspaceDirtyPaths(r.cfg.WorkspaceDir)
	hasOwnUncommittedChanges := hasDirtyChangesSince(state.runStartDirtySnapshot, dirtyAfter)
	finalized := !task.IsLapsBacked || len(recordedLaps) > 0 || handoffEntry != nil || handoffState != 0 || (task.LapID == "" && attempt.result != nil && attempt.result.Completed)
	hasUserFileChanges := len(preCommitFilesChanged) > 0
	incomplete := task.IsLapsBacked && hasOwnUncommittedChanges && !finalized
	dirtyHandoff := handoffEntry != nil && hasOwnUncommittedChanges
	if attempt.headBefore != "" && attempt.headAfter != "" && attempt.headBefore != attempt.headAfter {
		commitHistory = r.commitRange(attempt.headBefore, attempt.headAfter)
		if len(commitHistory) == 0 {
			commitHistory = []string{attempt.headAfter}
		}
		commitHash = commitHistory[len(commitHistory)-1]
	} else if dirtyBeforeAutoCommit && hasUserFileChanges && !incomplete && !dirtyHandoff && finalized {
		hash, commitErr := r.autoCommit(runIndex, picked.Harness, attempt.attempt)
		if commitErr != nil {
			fmt.Fprintf(log, "relay %d run %d attempt %d auto-commit warning: %v\n", relay.ID, runIndex+1, attempt.attempt, commitErr)
		} else if hash != "" {
			commitHash = hash
			commitHistory = []string{hash}
		}
	}

	filesChangedList := preCommitFilesChanged
	if commitHash != "" {
		filesChangedList = r.filesChangedList(attempt.result, attempt.headBefore, attempt.headAfter, commitHash)
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
			fmt.Fprintf(log, "relay %d run %d attempt %d rally state fold warning: %v\n", relay.ID, runIndex+1, attempt.attempt, foldErr)
		} else if newHash != "" && newHash != commitHash {
			if len(commitHistory) > 0 && commitHistory[len(commitHistory)-1] == commitHash {
				commitHistory[len(commitHistory)-1] = newHash
			}
			commitHash = newHash
		}
	} else if foldErr := gitx.FoldRallyState(r.cfg.WorkspaceDir); foldErr != nil {
		fmt.Fprintf(log, "relay %d run %d attempt %d rally state fold warning: %v\n", relay.ID, runIndex+1, attempt.attempt, foldErr)
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

	attempt.runtime = runtime
	attempt.runRuntime = runRuntime
	attempt.recordedLaps = recordedLaps
	attempt.lapsAttempted = lapsAttempted
	attempt.handoffState = handoffState
	attempt.handoffEntry = handoffEntry
	attempt.recoveryClassification = recoveryClassification
	attempt.commitHash = commitHash
	attempt.commitHistory = commitHistory
	attempt.filesChangedList = filesChangedList
	attempt.filesChangedCount = filesChangedCount
	attempt.finalized = finalized
	attempt.incomplete = incomplete
	attempt.dirtyHandoff = dirtyHandoff
	attempt.shortHash = shortHash
	attempt.commitTitle = commitTitle
}

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
	renderRunFooter(r.outWriter(), style.FooterOptions{
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
	})

	tryRecord := store.TryRecord{
		ID:                     attempt.tryID,
		RunID:                  runIndex + 1,
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

func (r *Runner) classifyAttemptOutcome(relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, task runTask, state *runOneState, attempt *runAttemptState, log io.Writer) {
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
		if noFileChanges && attempt.runtime < 3*time.Minute && attempt.handoffEntry == nil {
			attempt.failed = true
			state.failReason = "no changes made"
		}
	}

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
	// Stall recovery: if the stall detector killed the process but the agent had
	// already committed or created files (autoCommit ran), treat the try as
	// successful. This handles agents (e.g. opencode TUI) that complete the
	// task then idle in an interactive loop until the stall detector kills them.
	// VERIFY runs are excluded: a trivial commit is not evidence verification happened.
	if attempt.failed && state.stallMarked && attempt.commitHash != "" && !attempt.lapPinMismatch {
		if strings.EqualFold(task.Assignee, "verify") {
			fmt.Fprintf(log, "relay %d run %d attempt %d stall recovery: files committed but assignee is %s, not treating as success\n", relay.ID, runIndex+1, attempt.attempt, task.Assignee)
		} else {
			attempt.failed = false
			state.success = true
			fmt.Fprintf(log, "relay %d run %d attempt %d stall recovery: files committed, treating as success\n", relay.ID, runIndex+1, attempt.attempt)
		}
	}

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
					fmt.Println(style.DimStyle.Render(fmt.Sprintf("waiting %v for rate limit...", cooldown)))
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
	state.lastAttemptIncomplete = attempt.failed && attempt.attemptFailureClass == reliability.FailureIncomplete
}

func (r *Runner) recordAttemptOutcome(relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, task runTask, state *runOneState, attempt *runAttemptState, log io.Writer) error {
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
	renderRunFooter(r.outWriter(), style.FooterOptions{
		Passed:       !attempt.failed,
		Duration:     footerDuration,
		FilesChanged: attempt.filesChangedCount,
		CommitHash:   attempt.shortHash,
		CommitTitle:  attempt.commitTitle,
		FailReason:   state.failReason,
		Interim:      willRetry,
		Attempt:      attempt.attempt,
		MaxAttempts:  state.maxAttempts,
	})

	tryRecord := store.TryRecord{
		ID:                     attempt.tryID,
		RunID:                  runIndex + 1,
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
	fmt.Fprintf(log, "relay %d run %d attempt %d result: completed=%v outcome=%q fail_reason=%q runtime=%s files_changed=%d tool_calls=%d commit=%q lap_id=%q assignee=%q recorded_laps=%v laps_attempted=%v handoff_state=%d\n",
		relay.ID, runIndex+1, attempt.attempt, !attempt.failed, attempt.attemptOutcome, state.failReason, attempt.runtime, attempt.filesChangedCount, tryRecord.ToolCalls, attempt.shortHash, task.LapID, task.Assignee, attempt.recordedLaps, attempt.lapsAttempted, attempt.handoffState)

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

func (r *Runner) decideRetryOrComplete(task runTask, state *runOneState, attempt *runAttemptState, onStallRecovered func()) runOneAttemptDecision {
	if attempt.actionTaken {
		if r.stopFlag.Load() {
			return runOneAttemptDecision{action: runOneAttemptReturn, outcome: state.outcome(task, false, false, true)}
		}
		if r.skipFlag.Load() {
			return runOneAttemptDecision{action: runOneAttemptReturn, outcome: state.outcome(task, false, false, false)}
		}
		fmt.Println("Paused — press Enter to resume")
		bufio.NewReader(os.Stdin).ReadString('\n')
		if attempt.result != nil {
			state.previousSummary = attempt.result.Summary
			state.lastResult = attempt.result
			if attempt.result.SessionID != "" {
				state.sessionID = attempt.result.SessionID
			}
		} else {
			state.previousSummary = ""
			state.lastResult = &harnessapi.TryResult{Completed: false}
		}
		return runOneAttemptDecision{action: runOneAttemptContinue}
	}

	if !attempt.failed {
		if state.stallMarked && onStallRecovered != nil {
			onStallRecovered()
			attempt.mon.SetStalled(false)
			attempt.mon.SetRecovered()
			state.stallMarked = false
		}
		state.success = true
		state.lastResult = attempt.result
		return runOneAttemptDecision{action: runOneAttemptBreak}
	}

	if r.skipFlag.Load() {
		return runOneAttemptDecision{action: runOneAttemptBreak}
	}
	if attempt.lapPinMismatch {
		return runOneAttemptDecision{action: runOneAttemptBreak}
	}
	// usage_limit / auth_or_proxy are bounded to a single attempt: break here
	// so the routing dispatch loop can bench the quota scope or route away
	// rather than burning the retry budget on a quota/auth failure that a
	// retry cannot clear.
	if attempt.terminalForRun {
		state.lastResult = attempt.result
		return runOneAttemptDecision{action: runOneAttemptBreak}
	}
	// Run-budget exhaustion is terminal for the normal implementation loop,
	// even though OutcomeRunTimeout is deliberately NOT terminal in
	// IsTerminalForRun (a per-try cap with budget remaining must still be able
	// to retry). Unlike that retryable per-try cap, an exhausted run budget
	// stops retries here and the run proceeds to the bounded handoff-only
	// continuation. That continuation is owned by the next lap (task 4); until
	// it lands, breaking here resolves the run on the persisted run_timeout
	// attempt rather than burning more of an already-exhausted budget.
	if attempt.runBudgetExhausted {
		state.lastResult = attempt.result
		// When the harness can resume the captured session, defer to the
		// bounded handoff-only continuation below: the run_timeout try just
		// recorded is observability only, and the continuation (a separate
		// HandoffOnly try) becomes the run resolver. Without a resumable
		// session the run_timeout try was already relabelled handoff_timeout
		// above and is itself the resolving try.
		if attempt.canHandoffResume {
			state.handoffResumePending = true
			state.handoffResumeSessionID = state.sessionID
			state.handoffResumeBaseAttempt = attempt.attempt
		}
		return runOneAttemptDecision{action: runOneAttemptBreak}
	}

	if attempt.result != nil {
		state.previousSummary = attempt.result.Summary
		state.lastResult = attempt.result
		if attempt.result.SessionID != "" {
			state.sessionID = attempt.result.SessionID
		}
	} else {
		state.previousSummary = ""
		state.lastResult = &harnessapi.TryResult{Completed: false}
	}
	return runOneAttemptDecision{action: runOneAttemptContinue}
}

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

func (r *Runner) executeTry(ctx context.Context, picked harnessapi.ResolvedAgent, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
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
