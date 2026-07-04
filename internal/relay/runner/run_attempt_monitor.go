package runner

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/monitor"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func (r *Runner) runMonitoredAttempt(ctx context.Context, relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, state *runOneState, attempt *runAttemptState, onStall func(), log io.Writer) {
	var actionCh <-chan runtimeevent.Press
	if r.cfg.Controls != nil {
		var err error
		actionCh, err = r.cfg.Controls.Start(ctx)
		if err != nil {
			fmt.Fprintf(log, "relay %d run %d attempt %d controls warning: %v\n", relay.ID, runIndex+1, attempt.attempt, err)
		}
	}

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
		r.eventSink().Emit(ctx, runtimeevent.TryStatusSnapshot{Status: initialStatus})
		cursorUp = 2
	}
	r.eventSink().Emit(ctx, runtimeevent.ShortcutHintReady{Width: 0})
	mon.SetCursorUpLines(cursorUp)
	mon.Start(r.statusWriter())

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
	if r.cfg.Controls != nil {
		r.cfg.Controls.Stop()
	}

	attempt.endedAt = time.Now().UTC()
	attempt.headAfter, _ = r.headHash()
}

func (r *Runner) executeTry(ctx context.Context, picked harnessapi.ResolvedAgent, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
	exec, ok := r.executors[picked.Harness]
	if !ok {
		return nil, fmt.Errorf("no executor for agent %s", picked.Harness)
	}
	return exec.Execute(ctx, opts)
}

func (r *Runner) resolveAttemptFinalSnippet(state *runOneState, attempt *runAttemptState) {
	normalizedSummary := r.normalizeFinalSnippet(state.runID, attempt.tryLogPath, state.summaryEntryCountBeforeRun, attempt.result, attempt.execErr)
	if attempt.result == nil {
		attempt.result = &harnessapi.TryResult{}
	}
	attempt.result.Summary = normalizedSummary
}
