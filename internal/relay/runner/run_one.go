package runner

import (
	"context"
	"io"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/monitor"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
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
