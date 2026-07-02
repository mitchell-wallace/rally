package runner

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/keyboard"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

// These tests lock the parallel-emit event ORDER (design Decision 3 / phase 3.2)
// for each operator-facing flow path by driving it with a recording sink and
// asserting the sequence of emitted [runtimeevent.Kind]s. They are deliberately
// order-focused — no styling, payload, or byte assertions — so they survive the
// phase-4 rendering cut-over unchanged. Each path drives the same seam a real
// presentation adapter subscribes to: Config.EventSink (relay/run paths) and the
// waitLoop/runActionLoop cores that the runner calls inline at its print sites.

// suppressStdout redirects the monitor residual to devnull for the test's
// lifetime. The recording sink captures the events under test.
func suppressStdout(t *testing.T) {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	orig := os.Stdout
	os.Stdout = devnull
	t.Cleanup(func() {
		os.Stdout = orig
		_ = devnull.Close()
	})
}

// assertKinds asserts the recording sink captured exactly the want sequence of
// event kinds, in order. It is the order-contract lock these tests exist for.
func assertKinds(t *testing.T, rec *runtimeevent.RecordingSink, want ...runtimeevent.Kind) {
	t.Helper()
	got := rec.Kinds()
	if len(got) != len(want) {
		t.Fatalf("event order length mismatch:\n got: %v\nwant: %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("event order mismatch at index %d:\n got: %v\nwant: %v", i, got, want)
		}
	}
}

// assertWaitCountdownOrder asserts the wait-countdown contract: the sequence
// starts with WaitStarted, ends with WaitFinished, and every interior event is a
// WaitTick. The number of ticks is timing-dependent (the countdown repaints only
// when its rendered frame changes), so the order structure — not the exact tick
// count — is the durable contract.
func assertWaitCountdownOrder(t *testing.T, rec *runtimeevent.RecordingSink) {
	t.Helper()
	got := rec.Kinds()
	if len(got) < 2 {
		t.Fatalf("wait countdown emitted too few events: %v", got)
	}
	if got[0] != runtimeevent.KindWaitStarted || got[len(got)-1] != runtimeevent.KindWaitFinished {
		t.Fatalf("wait countdown must start with WaitStarted and end with WaitFinished:\n got: %v", got)
	}
	for _, k := range got[1 : len(got)-1] {
		if k != runtimeevent.KindWaitTick {
			t.Fatalf("interior wait event must be WaitTick:\n got: %v", got)
		}
	}
}

// orderTestWorkspace builds an initialised git workspace + store pair for a
// relay-level order test, suppressing stdout. Events are asserted via the
// recording sink, not terminal bytes.
func orderTestWorkspace(t *testing.T) (workspaceDir string, s *store.Store) {
	t.Helper()
	suppressStdout(t)
	workspaceDir = t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	s = newTestStore(t, rallyDir)
	return workspaceDir, s
}

func writeSuccessFile(t *testing.T, workspaceDir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workspaceDir, name), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEventOrderSimpleSuccess pins the event order for a run that completes on
// its first attempt: the relay-started bookend, one run header / status /
// shortcut-hint block, the passing attempt footer, then the relay summary and
// relay-completed bookend.
func TestEventOrderSimpleSuccess(t *testing.T) {
	workspaceDir, s := orderTestWorkspace(t)
	exec := &funcExecutor{fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
		writeSuccessFile(t, workspaceDir, "ok.txt")
		return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
	}}
	rec := runtimeevent.NewRecordingSink()
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		EventSink:        rec,
	}, map[string]harnessapi.Executor{"claude": exec})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertKinds(t, rec,
		runtimeevent.KindRouteWarning,
		runtimeevent.KindRelayStarted,
		runtimeevent.KindRunHeaderReady,
		runtimeevent.KindTryStatusSnapshot,
		runtimeevent.KindShortcutHintReady,
		runtimeevent.KindAttemptFinished,
		runtimeevent.KindRelaySummaryReady,
		runtimeevent.KindRelayCompleted,
	)
}

// TestEventOrderRetryThenFail pins the event order for a run that retries within
// budget and ultimately fails: each within-budget retry emits an interim
// RetryFooterUpdated (preceded by a fresh status/shortcut-hint frame), and the
// exhausted final attempt emits exactly one terminal AttemptFinished. With a
// single-agent lane the relay then errors out (all agents benched), so no relay
// summary is emitted — the within-run retry cadence is the contract locked here.
func TestEventOrderRetryThenFail(t *testing.T) {
	workspaceDir, s := orderTestWorkspace(t)
	exec := &funcExecutor{fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
		return &harnessapi.TryResult{Completed: false, Summary: "nope"}, nil
	}}
	rec := runtimeevent.NewRecordingSink()
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      3,
		EventSink:        rec,
	}, map[string]harnessapi.Executor{"claude": exec})

	_ = r.Run(context.Background())

	assertKinds(t, rec,
		runtimeevent.KindRouteWarning,
		runtimeevent.KindRelayStarted,
		// Attempt 1 (within budget): header, status, shortcut, interim retry footer.
		runtimeevent.KindRunHeaderReady,
		runtimeevent.KindTryStatusSnapshot,
		runtimeevent.KindShortcutHintReady,
		runtimeevent.KindRetryFooterUpdated,
		// Attempt 2 (within budget): no fresh header, interim retry footer.
		runtimeevent.KindTryStatusSnapshot,
		runtimeevent.KindShortcutHintReady,
		runtimeevent.KindRetryFooterUpdated,
		// Attempt 3 (budget exhausted): terminal fail footer.
		runtimeevent.KindTryStatusSnapshot,
		runtimeevent.KindShortcutHintReady,
		runtimeevent.KindAttemptFinished,
	)
}

// TestEventOrderRateLimitWait pins the event order for a rate-limited attempt
// that recovers on retry: after the first attempt fails with a retry-after
// cooldown, RateLimitWaitStarted is emitted (before the interim retry footer),
// then the retried attempt succeeds with a terminal AttemptFinished, and the
// relay completes with its summary + completed bookends.
func TestEventOrderRateLimitWait(t *testing.T) {
	workspaceDir, s := orderTestWorkspace(t)
	attempt := 0
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			if attempt == 1 && opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("sending to claude...\nerror 429 Too Many Requests\nretry-after: 1\n"), 0o644)
				return &harnessapi.TryResult{Completed: false, Summary: "rate limited", SessionID: "sess-1"}, nil
			}
			writeSuccessFile(t, workspaceDir, "ok.txt")
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	rec := runtimeevent.NewRecordingSink()
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      3,
		Resolver:         testResolver,
		EventSink:        rec,
	}, map[string]harnessapi.Executor{"claude": exec})
	r.sleepFunc = func(time.Duration) {} // stub the cooldown sleep so the retry is immediate

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	assertKinds(t, rec,
		runtimeevent.KindRouteWarning,
		runtimeevent.KindRelayStarted,
		// Attempt 1 fails with a rate-limit cooldown.
		runtimeevent.KindRunHeaderReady,
		runtimeevent.KindTryStatusSnapshot,
		runtimeevent.KindShortcutHintReady,
		runtimeevent.KindRateLimitWaitStarted,
		runtimeevent.KindRetryFooterUpdated,
		// Attempt 2 succeeds (no fresh header on retry).
		runtimeevent.KindTryStatusSnapshot,
		runtimeevent.KindShortcutHintReady,
		runtimeevent.KindAttemptFinished,
		runtimeevent.KindRelaySummaryReady,
		runtimeevent.KindRelayCompleted,
	)
}

// TestEventOrderStallRecovery pins the event order through the stall path: a
// SENIOR attempt that stalls with committed work is auto-completed on recovery,
// emitting the standard header / status / shortcut-hint / passing-footer
// sequence (the stall itself surfaces on the monitor, not as a runtime event).
func TestEventOrderStallRecovery(t *testing.T) {
	suppressStdout(t)
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")
	s := newTestStore(t, rallyDir)

	freezeCh := make(chan struct{})
	exec := &funcExecutor{fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
		if err := os.WriteFile(filepath.Join(workspaceDir, "impl-fix.txt"), []byte("implementation fix"), 0o644); err != nil {
			return nil, err
		}
		runGit(t, workspaceDir, "add", "impl-fix.txt")
		runGit(t, workspaceDir, "commit", "-m", "implementation work", "--no-verify")
		<-freezeCh
		return &harnessapi.TryResult{Completed: false, Summary: "stalled"}, nil
	}}

	rec := runtimeevent.NewRecordingSink()
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      1,
		EventSink:        rec,
	}, map[string]harnessapi.Executor{"claude": exec})

	controllerCount := 0
	r.stallControllerFactory = func(string) reliability.StallController {
		controllerCount++
		if controllerCount == 1 {
			triggered := false
			return &fakeStallController{check: func(context.Context) (bool, error) {
				if triggered {
					return false, nil
				}
				triggered = true
				close(freezeCh)
				return true, nil
			}}
		}
		return &fakeStallController{}
	}
	oldInterval := stallCheckInterval
	stallCheckInterval = time.Millisecond
	defer func() { stallCheckInterval = oldInterval }()

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "claude"},
		runTask{Name: "impl run", Prompt: "implement feature", Assignee: "senior"},
		nil, nil, false, false, nil, nil, io.Discard,
	)
	if err != nil {
		t.Fatalf("runOne: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected stall recovery success, fail reason = %q", res.FailReason)
	}

	assertKinds(t, rec,
		runtimeevent.KindRunHeaderReady,
		runtimeevent.KindTryStatusSnapshot,
		runtimeevent.KindShortcutHintReady,
		runtimeevent.KindAttemptFinished,
	)
}

// TestEventOrderAllPausedWait pins the wait-countdown order for the relay-level
// "all agents paused, waiting" path (the message format selectRouteOrWait uses
// when every lane is paused). The countdown emits WaitStarted, redraws on each
// frame change as WaitTick, and clears with WaitFinished when the timer elapses.
// It drives waitLoop — the I/O-free core factored out for testability — with the
// exact paused-wait message format from relay_steps.go.
func TestEventOrderAllPausedWait(t *testing.T) {
	rec := runtimeevent.NewRecordingSink()
	actionCh := make(chan keyboard.Press) // no operator input: wait runs to elapsed
	outcome := waitLoop(context.Background(), rec, 600*time.Millisecond, "agents paused, waiting %s...", actionCh, 100*time.Millisecond)
	if outcome != waitElapsed {
		t.Fatalf("outcome = %v, want waitElapsed", outcome)
	}
	assertWaitCountdownOrder(t, rec)
}

// TestEventOrderOperatorCancellation pins the operator-cancellation event order
// across both contexts where the operator can act:
//   - the wait countdown (Ctrl+S skip; Ctrl+C arm then confirm quit), which
//     emits the Wait* frames plus OperatorActionArmed, and
//   - the active try (Ctrl+S arm then confirm), which emits OperatorActionArmed
//     followed by OperatorActionApplied.
//
// It locks the relative order of the armed/applied feedback and the wait
// bookends; no styling or payload is asserted.
func TestEventOrderOperatorCancellation(t *testing.T) {
	t.Run("wait_skip", func(t *testing.T) {
		rec := runtimeevent.NewRecordingSink()
		actionCh := make(chan keyboard.Press, 1)
		actionCh <- keyboard.Press{Action: keyboard.ActionSkip, Confirmed: true}
		// A large tick interval guarantees no ticker frame interleaves with the
		// buffered action, so the order is exactly start → finish.
		outcome := waitLoop(context.Background(), rec, 5*time.Second, "agents paused, waiting %s...", actionCh, time.Second)
		if outcome != waitSkipped {
			t.Fatalf("outcome = %v, want waitSkipped", outcome)
		}
		assertKinds(t, rec,
			runtimeevent.KindWaitStarted,
			runtimeevent.KindWaitFinished,
		)
	})

	t.Run("wait_quit_armed", func(t *testing.T) {
		rec := runtimeevent.NewRecordingSink()
		actionCh := make(chan keyboard.Press, 2)
		// First press arms (OperatorActionArmed + a repaint frame as WaitTick,
		// since the hint line changes); the confirmed press clears (WaitFinished).
		actionCh <- keyboard.Press{Action: keyboard.ActionQuit, Confirmed: false}
		actionCh <- keyboard.Press{Action: keyboard.ActionQuit, Confirmed: true}
		outcome := waitLoop(context.Background(), rec, 5*time.Second, "agents paused, waiting %s...", actionCh, time.Second)
		if outcome != waitStopped {
			t.Fatalf("outcome = %v, want waitStopped", outcome)
		}
		assertKinds(t, rec,
			runtimeevent.KindWaitStarted,
			runtimeevent.KindOperatorActionArmed,
			runtimeevent.KindWaitTick,
			runtimeevent.KindWaitFinished,
		)
	})

	t.Run("active_try_skip", func(t *testing.T) {
		r := &Runner{}
		rec := runtimeevent.NewRecordingSink()
		r.cfg.EventSink = rec
		tryCh := make(chan tryResult, 1)
		actionCh := make(chan keyboard.Press, 2)
		actionCh <- keyboard.Press{Action: keyboard.ActionSkip, Confirmed: false}
		actionCh <- keyboard.Press{Action: keyboard.ActionSkip, Confirmed: true}
		attemptCtx, cancelAttempt := context.WithCancel(context.Background())
		defer cancelAttempt()
		go func() {
			<-attemptCtx.Done()
			tryCh <- tryResult{result: &harnessapi.TryResult{Completed: false}, err: attemptCtx.Err()}
		}()
		done := runLoopAsync(r, actionLoopDeps{
			tryCh:         tryCh,
			pidCh:         make(chan int, 1),
			actionCh:      actionCh,
			stallTick:     neverTick(),
			attemptCtx:    attemptCtx,
			cancelAttempt: cancelAttempt,
			mon:           &fakeMonitor{},
			log:           io.Discard,
		})
		out := <-done
		if out.cancellationSource != CancellationSourceSkip {
			t.Fatalf("cancellationSource = %q, want %q", out.cancellationSource, CancellationSourceSkip)
		}
		assertKinds(t, rec,
			runtimeevent.KindOperatorActionArmed,
			runtimeevent.KindOperatorActionApplied,
		)
	})
}
