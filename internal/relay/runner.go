package relay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/agent_prompt"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/keyboard"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/monitor"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/style"
	"github.com/mitchell-wallace/rally/internal/telemetry"
	"github.com/mitchell-wallace/rally/internal/textutil"
	"github.com/mitchell-wallace/rally/internal/user_prompt/roleloader"
)

type Config struct {
	WorkspaceDir           string
	DataDir                string
	AgentMixSpecs          []string
	RouteSpecs             map[string][]string
	UseOverrideRoute       bool
	TargetIterations       int
	StallThreshold         time.Duration
	LivenessProbe          bool
	RetryBudget            int
	RunHooksOnAutoCommit   bool
	LapsEnabled            bool
	Instructions           string
	TaskPrompt             string
	OverwriteMixOnResume   bool
	Resolver               Resolver
	LapsInstructionsFile   string
	FreeRunPromptFile      string
	RecentTryCount         int
	RecentTryCharLimit     int
	RecentContextCharLimit int
}

type Runner struct {
	store      *store.Store
	cfg        Config
	executors  map[string]agent.Executor
	stopFlag   atomic.Bool
	skipFlag   atomic.Bool
	log        io.WriteCloser
	resilience *Resilience
	relayStart time.Time

	lapsInstructionsCache string
	lapsWarned            bool
	freeRunPromptCache    string
	freeRunWarned         bool

	stallControllerFactory func(logPath string) reliability.StallController
	sleepFunc              func(time.Duration)

	// forceKillFunc is the escalation hook for a second quit-now during the
	// cancel drain. Defaults to reliability.ForceKillProcessGroup; tests inject
	// a fake to assert the force-kill path is taken without signalling a real
	// process group.
	forceKillFunc func(pgid int) error

	// out is the console writer for run headers and outcome footers. Defaults
	// to os.Stdout via outWriter; tests inject a buffer to assert on footer
	// cadence and colouring.
	out io.Writer

	telemetry telemetry.Sink
}

// outWriter returns the console writer for headers/footers, defaulting to
// os.Stdout so call sites never need a nil check.
func (r *Runner) outWriter() io.Writer {
	if r.out == nil {
		return os.Stdout
	}
	return r.out
}

// SetTelemetry wires a telemetry sink into the runner. When unset, telemetry
// calls route to a no-op sink (see tel).
func (r *Runner) SetTelemetry(sink telemetry.Sink) {
	r.telemetry = sink
}

// tel returns the active telemetry sink, defaulting to a no-op so call sites
// never need a nil check.
func (r *Runner) tel() telemetry.Sink {
	if r.telemetry == nil {
		return telemetry.NoopSink{}
	}
	return r.telemetry
}

func applyTags(span telemetry.Span, tags map[string]string) {
	for k, v := range tags {
		span.SetTag(k, v)
	}
}

var headPullLap = func(ctx context.Context, workspaceDir string) (laps.Lap, error) {
	return (&laps.Adapter{WorkspaceDir: workspaceDir}).HeadPull(ctx)
}

var queueSize = func(ctx context.Context, workspaceDir string) (int, error) {
	return (&laps.Adapter{WorkspaceDir: workspaceDir}).QueueSize(ctx)
}

func NewRunner(s *store.Store, cfg Config, executors map[string]agent.Executor) *Runner {
	return &Runner{
		store:     s,
		cfg:       cfg,
		executors: executors,
	}
}

var stallCheckInterval = monitor.TickInterval

// renderRunFooter writes a per-attempt outcome to out, collapsing a burst of
// retries into one updating line. While the run still has retry budget
// (opts.Interim), it draws the neutral retry line, clearing the current line
// and parking the cursor at its start with no committed newline — so the next
// attempt's status line, or the next retry line, overwrites it in place. At a
// terminal outcome it clears any pending interim line and commits the coloured
// ✓/✗ footer block. This is the only place the per-attempt footer is emitted.
func renderRunFooter(out io.Writer, opts style.FooterOptions) {
	rendered := style.RenderFooter(opts)
	if opts.Interim {
		fmt.Fprintf(out, "\r\x1b[2K%s\r", rendered)
		return
	}
	fmt.Fprintf(out, "\r\x1b[2K%s\n", rendered)
}

const builtInDefaultFreeRunPrompt = "Continue the relay run. Review the current state of the codebase and continue making progress on the project."
const incompleteRetryGuidance = "The last run was incomplete. Check any current git changes, finish anything not done, verify correctness, commit when good, then run `laps done`."

const (
	finalSnippetFallbackRuneLimit = 1000
	finalSnippetTailMarker        = "... [tail truncated] ..."
	noFinalSnippetIndicator       = "(agent produced no final summary)"
)

// waitOutcome enumerates how a waitWithCountdown call ended.
type waitOutcome int

const (
	waitElapsed   waitOutcome = iota // timer ran out normally
	waitSkipped                      // user pressed Ctrl+S (skip) to bail out early
	waitStopped                      // user pressed Ctrl+X / Ctrl+C to abort the relay
	waitCancelled                    // ctx was cancelled (returns ctx.Err alongside)
)

// waitWithCountdown blocks for `total`, redrawing a one-line countdown +
// shortcut hint on stdout once per second. See [waitLoop] for the core logic;
// this wrapper handles the keyboard, terminal raw mode, and stdout rendering.
func waitWithCountdown(ctx context.Context, total time.Duration, msgFmt string) (waitOutcome, error) {
	if total <= 0 {
		return waitElapsed, nil
	}

	kb := keyboard.NewKeyboard(os.Stdin, os.Stdout)
	_ = kb.SetRawMode()
	defer func() { _ = kb.Stop() }()
	kbCtx, kbCancel := context.WithCancel(ctx)
	defer kbCancel()
	actionCh := kb.Start(kbCtx)

	outcome := waitLoop(ctx, total, msgFmt, actionCh, os.Stdout, time.Second)
	if outcome == waitCancelled {
		return outcome, ctx.Err()
	}
	return outcome, nil
}

// waitLoop is the I/O-free core of [waitWithCountdown]: it ticks at
// `tickInterval`, renders the countdown + shortcut hint to `out`, and returns
// when the timer elapses, ctx is cancelled, or an action arrives on actionCh.
// Split out from waitWithCountdown for testability.
func waitLoop(ctx context.Context, total time.Duration, msgFmt string, actionCh <-chan keyboard.Action, out io.Writer, tickInterval time.Duration) waitOutcome {
	deadline := time.Now().Add(total)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	render := func(remaining time.Duration) {
		if remaining < 0 {
			remaining = 0
		}
		remaining = remaining.Round(time.Second)
		line := style.DimStyle.Render(fmt.Sprintf(msgFmt, formatRemaining(remaining)))
		hint := style.ShortcutHint()
		// \r\x1b[J clears from cursor to end of screen so a shorter countdown
		// can't leave stale characters; the trailing \x1b[1A\r parks the
		// cursor back at the start of the countdown line ready for the next
		// tick. We always print the same two lines so the layout is stable.
		fmt.Fprintf(out, "\r\x1b[J%s\n%s\x1b[1A\r", line, hint)
	}
	clear := func() {
		// Erase both lines and leave the cursor at the top one so subsequent
		// stdout writes land on a fresh line.
		fmt.Fprint(out, "\r\x1b[J")
	}

	render(total)
	for {
		select {
		case <-ctx.Done():
			clear()
			return waitCancelled
		case action := <-actionCh:
			switch action {
			case keyboard.ActionSkip:
				clear()
				return waitSkipped
			case keyboard.ActionStop, keyboard.ActionQuit:
				clear()
				return waitStopped
			}
			// Ignore pause and any other actions during a wait — there is no
			// active try to pause.
		case now := <-ticker.C:
			remaining := time.Until(deadline)
			if remaining <= 0 || !now.Before(deadline) {
				clear()
				return waitElapsed
			}
			render(remaining)
		}
	}
}

// formatRemaining renders d as `Hh Mm Ss`, `Mm Ss`, or `Ss`, dropping
// zero-valued higher units so the countdown stays compact.
func formatRemaining(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second).Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

type runTask struct {
	Name          string
	Requirements  string
	Prompt        string
	Assignee      string
	LapID         string
	IsLapsBacked  bool
	LapsRemaining int
}

func (r *Runner) resolveInstructions() string {
	if !r.cfg.LapsEnabled {
		return r.cfg.Instructions
	}
	if r.cfg.LapsInstructionsFile == "" {
		return r.cfg.Instructions
	}
	if r.lapsInstructionsCache != "" {
		return r.lapsInstructionsCache
	}
	data, err := os.ReadFile(r.cfg.LapsInstructionsFile)
	if err != nil {
		if !r.lapsWarned {
			fmt.Fprintf(os.Stderr, "warning: laps instructions file %q not readable: %v; using default\n", r.cfg.LapsInstructionsFile, err)
			r.lapsWarned = true
		}
		return r.cfg.Instructions
	}
	r.lapsInstructionsCache = string(data)
	return r.lapsInstructionsCache
}

func (r *Runner) loadFreeRunPrompt() string {
	if r.freeRunPromptCache != "" {
		return r.freeRunPromptCache
	}
	if r.cfg.FreeRunPromptFile != "" {
		data, err := os.ReadFile(r.cfg.FreeRunPromptFile)
		if err != nil {
			if !r.freeRunWarned {
				fmt.Fprintf(os.Stderr, "warning: free-run prompt file %q not readable: %v; using built-in default\n", r.cfg.FreeRunPromptFile, err)
				r.freeRunWarned = true
			}
			return builtInDefaultFreeRunPrompt
		}
		r.freeRunPromptCache = string(data)
		return r.freeRunPromptCache
	}
	return builtInDefaultFreeRunPrompt
}

func (r *Runner) RequestStop() {
	r.stopFlag.Store(true)
}

func (r *Runner) Run(ctx context.Context) error {
	// Clear any stale run-state from a previous interrupted relay.
	_, _ = r.maybeWriteStubAndClearState("")

	relay, resumed, err := ResumeRelay(r.store)
	if err != nil {
		return err
	}

	routeRuntime := (*routeRuntime)(nil)
	selectionLabel := ""
	if resumed {
		if r.cfg.OverwriteMixOnResume {
			routeRuntime, selectionLabel, err = newRouteRuntimeFromConfig(r.cfg)
			if err != nil {
				return err
			}
			relay.AgentMix = selectionLabel
			if err := r.store.UpdateRelay(*relay); err != nil {
				return err
			}
		} else {
			routeRuntime, selectionLabel, err = newRouteRuntimeFromStoredLabel(r.cfg, relay.AgentMix)
			if err != nil {
				return err
			}
		}
	} else {
		routeRuntime, selectionLabel, err = newRouteRuntimeFromConfig(r.cfg)
		if err != nil {
			return err
		}
		relay, err = CreateRelay(r.store, r.cfg.TargetIterations, selectionLabel)
		if err != nil {
			return err
		}
	}

	for _, w := range routeRuntime.Warnings() {
		fmt.Fprintln(os.Stderr, w)
	}

	log, err := openRelayLog(r.cfg.DataDir, r.cfg.WorkspaceDir, relay.ID)
	if err != nil {
		return err
	}
	r.log = log
	defer func() {
		_ = log.Close()
	}()

	fmt.Fprintf(log, "relay %d started (target %d iterations, mix: %s)\n", relay.ID, relay.TargetIterations, relay.AgentMix)
	r.relayStart = time.Now()

	// Model the relay as a trace transaction; runs and tries are child spans.
	ctx, relaySpan := r.tel().StartSpan(ctx, "relay", fmt.Sprintf("relay-%d", relay.ID))
	applyTags(relaySpan, telemetry.Tags(telemetry.EventInfo{RelayID: relay.ID, Repo: repoKey(r.cfg.WorkspaceDir)}))
	defer relaySpan.Finish()

	resilience := r.resilience
	if resilience == nil {
		resilience = NewResilience(r.store)
	}

	// Consume oldest eligible relay-scoped message at relay start
	var relayMsg *store.MessageRecord
	relayPending := r.store.EligibleRelayScopedMessages(relay.ID)
	if len(relayPending) > 0 {
		msg := relayPending[0]
		// Record consumption at consume time (Task 6)
		if msg.ConsumedByRelayID == nil {
			msg.ConsumedByRelayID = &relay.ID
			if err := r.store.UpdateMessage(msg); err != nil {
				return err
			}
			// Append to ConsumedMessageIDs immediately
			relay.ConsumedMessageIDs = append(relay.ConsumedMessageIDs, msg.ID)
			if err := r.store.UpdateRelay(*relay); err != nil {
				return err
			}
		}
		relayMsg = &msg
	}

	runIndex := relay.CompletedIterations
	for relay.CompletedIterations < relay.TargetIterations {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.stopFlag.Load() {
			fmt.Fprintf(log, "relay %d stop requested, halting after current try\n", relay.ID)
			break
		}

		task, err := r.resolveRunTask(ctx)
		if err != nil {
			if errors.Is(err, errQueueEmpty) {
				fmt.Fprintf(log, "relay %d completed: laps queue empty\n", relay.ID)
				_ = CompleteRelay(r.store, relay.ID)
				break
			}
			return err
		}

		selection, err := routeRuntime.next(task, resilience)
		if err != nil {
			var routeErr *routeSelectionError
			if errors.As(err, &routeErr) {
				if routeErr.AllFrozen {
					fmt.Fprintf(log, "relay %d failed: all agents frozen\n", relay.ID)
					// A relay ending with every agent type frozen is a lockout
					// that warrants operator attention — capture it as an Issue.
					r.tel().CaptureFailure(ctx, fmt.Sprintf("relay %d stalled: all agents frozen", relay.ID),
						telemetry.Tags(telemetry.EventInfo{RelayID: relay.ID, Repo: repoKey(r.cfg.WorkspaceDir)}))
					_ = CompleteRelay(r.store, relay.ID)
					return fmt.Errorf("relay failed: all agents frozen")
				}
				fmt.Fprintf(log, "relay %d all agents paused, waiting %v\n", relay.ID, routeErr.Wait)
				outcome, waitErr := waitWithCountdown(ctx, routeErr.Wait, "agents frozen, waiting %s...")
				if waitErr != nil {
					return waitErr
				}
				switch outcome {
				case waitSkipped:
					unpaused, err := routeRuntime.forceUnpauseAll(resilience, relay.ID)
					if err != nil {
						return err
					}
					fmt.Fprintf(log, "relay %d skip pressed during wait; force-unpaused %d agent(s)\n", relay.ID, unpaused)
				case waitStopped:
					fmt.Fprintf(log, "relay %d stop requested during wait\n", relay.ID)
					r.stopFlag.Store(true)
				}
				continue
			}
			return err
		}
		if selection.Route.Warning != "" {
			fmt.Fprintln(os.Stderr, selection.Route.Warning)
			fmt.Fprintln(log, selection.Route.Warning)
		}
		r.prepareExecutorForSelection(relay.ID, runIndex, selection, log)

		// Consume run-scoped message at start of each run
		// First check if there's an already-consumed message from a failed run
		runID := runIndex + 1

		runTags := telemetry.Tags(telemetry.EventInfo{
			RelayID: relay.ID,
			RunID:   runID,
			Role:    task.Assignee,
			Harness: selection.Agent.Harness,
			Model:   selection.Agent.Model,
			Repo:    repoKey(r.cfg.WorkspaceDir),
			LapID:   task.LapID,
		})
		runCtx, runSpan := r.tel().StartSpan(ctx, "run", fmt.Sprintf("relay-%d-run-%d", relay.ID, runID))
		applyTags(runSpan, runTags)

		// Rotating to a backup runner is a healthy recovery, not an alert — log
		// it as a common event, never an Issue.
		if selection.PreviousAgent != nil &&
			(selection.PreviousAgent.Harness != selection.Agent.Harness ||
				selection.PreviousAgent.Model != selection.Agent.Model) {
			from := telemetry.RunnerLabel(selection.PreviousAgent.Harness, selection.PreviousAgent.Model)
			to := telemetry.RunnerLabel(selection.Agent.Harness, selection.Agent.Model)
			fmt.Fprintf(log, "relay %d run %d route fallback: rotated %s -> %s\n", relay.ID, runID, from, to)
			r.tel().EmitTryLog(runCtx, map[string]interface{}{
				"event":       "route_fallback",
				"relay_id":    relay.ID,
				"run_id":      runID,
				"runner":      to,
				"from_runner": from,
				"to_runner":   to,
				"role":        task.Assignee,
				"repo":        repoKey(r.cfg.WorkspaceDir),
				"lap_id":      task.LapID,
			})
		}
		var consumedMsg *store.MessageRecord
		if existingMsg := r.store.ConsumedRunScopedMessageForRun(runID); existingMsg != nil {
			// Reuse the message from the failed run
			consumedMsg = existingMsg
		} else {
			// Consume a new message
			pending := r.store.PendingMessages()
			for _, p := range pending {
				if p.Scope != "relay" && p.ConsumedByRunID == nil {
					msg := p
					msg.ConsumedByRunID = &runID
					if err := r.store.UpdateMessage(msg); err != nil {
						return err
					}
					consumedMsg = &msg
					break
				}
			}
		}

		success, addressed, interrupted, _, failureClass, infraFailures, err := r.runOne(
			runCtx,
			relay,
			runIndex,
			selection.Agent,
			task,
			consumedMsg,
			relayMsg,
			selection.HourlyRetry,
			selection.Probation,
			func() {
				selection.Scheduler.OnAgentFailed(selection.Entry, "stall", true)
			},
			func() {
				selection.Scheduler.OnAgentRecovered(selection.Entry)
			},
			log,
		)
		runSpan.Finish()
		if err != nil {
			fmt.Fprintf(log, "relay %d run %d error: %v\n", relay.ID, runIndex+1, err)
			return err
		}
		if interrupted {
			fmt.Fprintf(log, "relay %d stop requested, halting\n", relay.ID)
			break
		}

		// If skipped, don't pause the agent — just advance rotation
		if r.skipFlag.Load() {
			r.skipFlag.Store(false)
			selection.Entry.Exhausted = true
			selection.Entry.Benched = false
			runIndex++
			relay.LastTryID = r.store.NextTryID() - 1
			if relay.FirstTryID == 0 {
				relay.FirstTryID = relay.LastTryID
			}
			if err := r.store.UpdateRelay(*relay); err != nil {
				return err
			}
			continue
		}
		if !success {
			selection.Scheduler.OnAgentFailed(selection.Entry, "retry-budget-exhausted", false)
		}

		if selection.Probation {
			if success || failureClass == reliability.FailureIncomplete {
				if err := resilience.UnpauseAgent(KeyFromAgent(selection.Agent), relay.ID); err != nil {
					return err
				}
			} else {
				if err := resilience.FreezeAgent(KeyFromAgent(selection.Agent), relay.ID, "probation run failed"); err != nil {
					return err
				}
			}
		} else if selection.HourlyRetry {
			if success {
				if err := resilience.UnpauseAgent(KeyFromAgent(selection.Agent), relay.ID); err != nil {
					return err
				}
			} else if failureClass == reliability.FailureInfra && infraFailures > 1 {
				if err := resilience.RecordHourlyFailure(KeyFromAgent(selection.Agent), relay.ID); err != nil {
					return err
				}
			}
		} else {
			if !success && failureClass == reliability.FailureInfra && infraFailures > 1 {
				if err := resilience.PauseAgent(KeyFromAgent(selection.Agent), relay.ID); err != nil {
					return err
				}
			}
		}

		if success {
			relay.CompletedIterations++
			runIndex++
			if consumedMsg != nil && addressed {
				consumedMsg.Status = "addressed"
				now := time.Now().UTC().Format(time.RFC3339)
				consumedMsg.UpdatedAt = now
				if err := r.store.UpdateMessage(*consumedMsg); err != nil {
					return err
				}
				// Add to ConsumedMessageIDs if not already present
				if !containsInt(relay.ConsumedMessageIDs, consumedMsg.ID) {
					relay.ConsumedMessageIDs = append(relay.ConsumedMessageIDs, consumedMsg.ID)
				}
			}
			if relayMsg != nil && addressed && relayMsg.Status == "pending" {
				relayMsg.Status = "addressed"
				now := time.Now().UTC().Format(time.RFC3339)
				relayMsg.UpdatedAt = now
				if err := r.store.UpdateMessage(*relayMsg); err != nil {
					return err
				}
				// Already added at consume time, but ensure no duplicates
				if !containsInt(relay.ConsumedMessageIDs, relayMsg.ID) {
					relay.ConsumedMessageIDs = append(relay.ConsumedMessageIDs, relayMsg.ID)
				}
			}
		} else {
			runIndex++
		}

		relay.LastTryID = r.store.NextTryID() - 1
		if relay.FirstTryID == 0 {
			relay.FirstTryID = relay.LastTryID
		}
		if err := r.store.UpdateRelay(*relay); err != nil {
			return err
		}
	}

	if relay.CompletedIterations >= relay.TargetIterations {
		if err := CompleteRelay(r.store, relay.ID); err != nil {
			return err
		}
		fmt.Fprintf(log, "relay %d completed\n", relay.ID)
	}

	// Print relay summary
	totalRuns := relay.CompletedIterations
	if totalRuns > 0 {
		passCount, failCount := tallyRuns(r.store.AllTries(), relay.ID)
		totalDuration := time.Since(r.relayStart)
		summary := style.RenderSummary(totalRuns, passCount, failCount, totalDuration)
		fmt.Println(summary)
	}

	return nil
}

// tallyRuns aggregates try records into a run-level pass/fail count for the
// given relay. Each run (identified by RunID) is counted exactly once: it passes
// if any of its attempts ultimately completed, and fails only if every attempt
// in its retry budget was exhausted without completion. A retry-then-success run
// is therefore one pass and zero failures; an exhausted run is one failure.
func tallyRuns(tries []store.TryRecord, relayID int) (passCount, failCount int) {
	completedByRun := make(map[int]bool)
	order := make([]int, 0)
	for _, tr := range tries {
		if tr.RelayID != relayID {
			continue
		}
		if _, seen := completedByRun[tr.RunID]; !seen {
			order = append(order, tr.RunID)
		}
		if tr.Completed {
			completedByRun[tr.RunID] = true
		} else if _, seen := completedByRun[tr.RunID]; !seen {
			completedByRun[tr.RunID] = false
		}
	}
	for _, runID := range order {
		if completedByRun[runID] {
			passCount++
		} else {
			failCount++
		}
	}
	return passCount, failCount
}

func (r *Runner) prepareExecutorForSelection(relayID, runIndex int, selection routeSelection, log io.Writer) {
	if selection.PreviousAgent == nil {
		return
	}
	if selection.PreviousAgent.Harness != selection.Agent.Harness {
		return
	}

	exec := r.executors[selection.Agent.Harness]
	if exec == nil || !exec.RotateSupported() {
		return
	}

	// Each Execute starts a fresh CLI process, so doing nothing here naturally
	// preserves the existing teardown/respawn fallback path. Rotation is only an
	// optimization when the adapter opts in and the swap succeeds.
	if err := exec.RotateModel(selection.Agent.Model); err != nil {
		fmt.Fprintf(log, "relay %d run %d rotate fallback for %s: %v\n", relayID, runIndex+1, selection.Agent.Harness, err)
	}
}

func buildRecentContext(tries []store.TryRecord, perSummaryLimit, overallLimit int) string {
	var buf strings.Builder
	for _, t := range tries {
		summary := t.Summary
		if perSummaryLimit > 0 && len(summary) > perSummaryLimit {
			headSize := perSummaryLimit / 2
			tailSize := perSummaryLimit - headSize
			summary = summary[:headSize] + textutil.HeadTailTruncationMarker + summary[len(summary)-tailSize:]
		}
		fmt.Fprintf(&buf, "Run %d (%s): completed=%v summary=%s\n", t.RunID, t.AgentType, t.Completed, summary)
	}
	if overallLimit > 0 && buf.Len() > overallLimit {
		result := buf.String()
		headSize := overallLimit / 2
		tailSize := overallLimit - headSize
		return result[:headSize] + textutil.HeadTailTruncationMarker + result[len(result)-tailSize:]
	}
	return buf.String()
}

// forceKillGroup escalates the cancel drain to an immediate group-wide SIGKILL,
// routing through the injectable hook so tests can observe the escalation.
func (r *Runner) forceKillGroup(pgid int) error {
	if r.forceKillFunc != nil {
		return r.forceKillFunc(pgid)
	}
	return reliability.ForceKillProcessGroup(pgid)
}

// tryResult carries one attempt's executor outcome from the execute goroutine
// back to the action loop.
type tryResult struct {
	result *agent.TryResult
	err    error
}

// actionMonitor is the slice of *monitor.Monitor the action loop drives. It is
// an interface so the loop can be tested with a fake that records calls.
type actionMonitor interface {
	SetProcessGroupID(pgid int)
	SetStalled(v bool)
	SetStopping(v bool)
}

// actionLoopDeps bundles the channels and collaborators the in-try action loop
// selects over. Splitting them out lets [Runner.runActionLoop] be driven by a
// fake executor/try channel and simulated keyboard.Action values in tests.
type actionLoopDeps struct {
	tryCh           <-chan tryResult
	pidCh           <-chan int
	actionCh        <-chan keyboard.Action
	stallTick       <-chan time.Time
	attemptCtx      context.Context
	cancelAttempt   context.CancelFunc
	stallController reliability.StallController
	mon             actionMonitor
	onStall         func()
	log             io.Writer

	// Identifiers used only for log lines.
	relayID  int
	runIndex int
	attempt  int
	harness  string
}

// actionLoopResult is the outcome of one pass through the action loop.
type actionLoopResult struct {
	result         *agent.TryResult
	execErr        error
	actionTaken    bool
	stallTriggered bool
	attemptPGID    int
}

// runActionLoop is the in-try select-based state machine. It blocks until the
// attempt resolves on tryCh, or an operator action redirects it:
//
//   - Ctrl+S (skip) / Ctrl+P (pause): cancel the attempt, then drain tryCh.
//   - Ctrl+X (stop): set stopFlag and keep waiting so the current try finishes
//     naturally; the relay halts afterwards.
//   - Ctrl+C (quit now): set stopFlag, cancel the attempt immediately, and drain
//     tryCh while staying responsive — a second Ctrl+C within the grace window
//     escalates to a group-wide SIGKILL via forceKillGroup rather than waiting
//     the window out.
//
// While quit-now drains, the monitor shows a "stopping…" indicator so the UI
// does not look frozen. Stall detection (stallTick) and late pid reports
// (pidCh) are handled alongside the action cases.
func (r *Runner) runActionLoop(d actionLoopDeps) actionLoopResult {
	var out actionLoopResult
actionLoop:
	for {
		select {
		case res := <-d.tryCh:
			out.result = res.result
			out.execErr = res.err
			break actionLoop
		case pid := <-d.pidCh:
			out.attemptPGID = pid
			d.mon.SetProcessGroupID(pid)
			if d.stallController != nil {
				d.stallController.SetProcessGroupID(pid)
			}
		case <-d.stallTick:
			if d.stallController == nil || out.stallTriggered {
				continue
			}
			stalled, err := d.stallController.Check(d.attemptCtx)
			if err != nil {
				fmt.Fprintf(d.log, "relay %d run %d attempt %d stall check warning: %v\n", d.relayID, d.runIndex+1, d.attempt, err)
				continue
			}
			if !stalled {
				continue
			}
			out.stallTriggered = true
			d.mon.SetStalled(true)
			if d.onStall != nil {
				d.onStall()
			}
			fmt.Fprintf(d.log, "relay %d run %d attempt %d stall detected for %s\n", d.relayID, d.runIndex+1, d.attempt, d.harness)
		case action := <-d.actionCh:
			switch action {
			case keyboard.ActionSkip:
				d.cancelAttempt()
				r.skipFlag.Store(true)
				out.actionTaken = true
				res := <-d.tryCh
				out.result = res.result
				out.execErr = res.err
				break actionLoop
			case keyboard.ActionPause:
				d.cancelAttempt()
				out.actionTaken = true
				res := <-d.tryCh
				out.result = res.result
				out.execErr = res.err
				break actionLoop
			case keyboard.ActionStop:
				// Graceful stop (Ctrl+X): let the current try finish, then
				// stop the relay. The outer loop sees stopFlag and launches
				// no further runs. For a frozen agent this is bounded by the
				// stall detector.
				r.stopFlag.Store(true)
			case keyboard.ActionQuit:
				// Quit now (Ctrl+C): cancel the running try immediately and
				// abort the relay. cancelAttempt fires Cmd.Cancel, which sends
				// SIGINT to the process group then escalates to a group-wide
				// SIGKILL after the grace window. Mirror the pause/skip drain
				// of tryCh, but keep selecting on actionCh so a second Ctrl+C
				// within the grace window forces an immediate SIGKILL rather
				// than waiting it out. The "stopping…" monitor indicator keeps
				// the UI from looking frozen during the drain.
				r.stopFlag.Store(true)
				d.cancelAttempt()
				d.mon.SetStopping(true)
				out.actionTaken = true
			drainLoop:
				for {
					select {
					case res := <-d.tryCh:
						out.result = res.result
						out.execErr = res.err
						break drainLoop
					case pid := <-d.pidCh:
						// A late OnStart can land here if the try hadn't
						// reported its pid yet; capture it so a second quit
						// can target the right group.
						out.attemptPGID = pid
					case a := <-d.actionCh:
						// Second quit-now: escalate past the grace window.
						// SIGKILL is idempotent (a re-signalled or already
						// exited group resolves to nil), so this cannot race
						// the WaitDelay/Cmd.Cancel escalation into an error.
						if a == keyboard.ActionQuit && out.attemptPGID > 0 {
							_ = r.forceKillGroup(out.attemptPGID)
						}
					}
				}
				break actionLoop
			}
		}
	}
	return out
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
) (bool, bool, bool, string, reliability.FailureClass, int, error) {
	// Initialize run-state for this run.
	runID := fmt.Sprintf("relay-%d-run-%d", relay.ID, runIndex+1)
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
	infraFailures := 0
	lastAttemptIncomplete := false
	stallMarked := false
	roleInstructions, err := r.resolveRoleInstructions(task.Assignee)
	if err != nil {
		return false, false, false, "", failureClass, infraFailures, err
	}

	// Check for uncommitted non-rally changes at run start. Errors are
	// tolerated (treat as clean) so a broken git setup never crashes the run.
	leftoverWork, _ := gitx.IsWorkspaceDirty(r.cfg.WorkspaceDir)

	exec := r.executors[picked.Harness]

	maxAttempts := r.cfg.RetryBudget
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if isHourlyRetry {
		maxAttempts = HourlyRetryMaxAttempts
	}
	if isProbation {
		maxAttempts = HourlyRetryMaxAttempts
	}
attemptLoop:
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return false, false, false, "", failureClass, infraFailures, ctx.Err()
		}
		if r.stopFlag.Load() {
			return false, false, true, "", failureClass, infraFailures, nil
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
			return false, false, false, "", failureClass, infraFailures, fmt.Errorf("create current_task.md dir: %w", err)
		}
		if err := os.WriteFile(taskPath, []byte(prompt), 0o644); err != nil {
			return false, false, false, "", failureClass, infraFailures, fmt.Errorf("write current_task.md: %w", err)
		}

		tryLogPath := filepath.Join(r.cfg.DataDir, "tries", repoKey(r.cfg.WorkspaceDir), fmt.Sprintf("try-%d.log", r.store.NextTryID()))
		_ = os.MkdirAll(filepath.Dir(tryLogPath), 0o755)
		opts.LogPath = tryLogPath

		headBefore, _ := r.headHash()
		dirtySnapshot, _ := gitx.WorkspaceDirtyPaths(r.cfg.WorkspaceDir)
		startedAt := time.Now().UTC()

		var lapsStarted, lapsTotal int
		if task.IsLapsBacked {
			// task.LapsRemaining is the current queue size including the head
			// (HeadPull reads but does not dequeue), so total = completed + queue.
			lapsStarted = runIndex + 1
			lapsTotal = runIndex + task.LapsRemaining
		}
		// Retries are surfaced inline as a `retry N/M` field on the live status
		// line (see mon.SetRetry below) rather than re-announcing the run with a
		// fresh header block per attempt.
		if attempt == 1 {
			header := style.RenderHeader(style.HeaderOptions{
				RunIndex:     runIndex,
				TotalRuns:    relay.TargetIterations,
				AgentName:    picked.Harness,
				Attempt:      attempt,
				StartTime:    startedAt,
				IsLapsBacked: task.IsLapsBacked,
				LapTitle:     task.Name,
				LapsStarted:  lapsStarted,
				LapsTotal:    lapsTotal,
				Model:        picked.Model,
			})
			fmt.Println(header)
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
		loopOut := r.runActionLoop(actionLoopDeps{
			tryCh:           tryCh,
			pidCh:           pidCh,
			actionCh:        actionCh,
			stallTick:       stallTicker.C,
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

		result := loopOut.result
		execErr := loopOut.execErr
		actionTaken := loopOut.actionTaken
		if loopOut.stallTriggered {
			stallMarked = true
		}

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
		if task.IsLapsBacked {
			recordedLaps = mergeStrings(recordedLaps, progressLapsCompletedForRun(r.cfg.WorkspaceDir, runID))
		}

		runtime := endedAt.Sub(startedAt)
		commitHash := ""
		commitHistory := []string{}
		preCommitFilesChanged := r.filesChangedList(result, headBefore, headAfter, "")
		dirtyBeforeAutoCommit, _ := gitx.IsWorkspaceDirty(r.cfg.WorkspaceDir)
		dirtyAfter, _ := gitx.WorkspaceDirtyPaths(r.cfg.WorkspaceDir)
		hasOwnUncommittedChanges := false
		for path, afterXY := range dirtyAfter {
			beforeXY, existed := dirtySnapshot[path]
			if !existed || beforeXY != afterXY {
				hasOwnUncommittedChanges = true
				break
			}
		}
		finalized := !task.IsLapsBacked || len(recordedLaps) > 0 || handoffState != 0 || (task.LapID == "" && result != nil && result.Completed)
		hasUserFileChanges := len(preCommitFilesChanged) > 0
		incomplete := task.IsLapsBacked && hasOwnUncommittedChanges && !finalized
		if headBefore != "" && headAfter != "" && headBefore != headAfter {
			commitHistory = r.commitRange(headBefore, headAfter)
			if len(commitHistory) == 0 {
				commitHistory = []string{headAfter}
			}
			commitHash = commitHistory[len(commitHistory)-1]
		} else if dirtyBeforeAutoCommit && hasUserFileChanges && !incomplete && finalized {
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

		// Compute failed before rendering the footer so the displayed result
		// matches what gets recorded in the try record.
		failed := false
		failReason = ""
		attemptFailureClass := reliability.FailureAgent
		if incomplete {
			failed = true
			failReason = "incomplete run"
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
			if noFileChanges && runtime < 3*time.Minute {
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
				failed = true
				failReason = reason
				lapPinMismatch = true
				failureClass = reliability.FailureAgent
				fmt.Fprintf(log, "relay %d run %d attempt %d lap pin mismatch: pinned_lap=%q consumed_laps=%v reason=%s\n", relay.ID, runIndex+1, attempt, task.LapID, recordedLaps, reason)
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

		// Error classification and strategy dispatch.
		if failed && !lapPinMismatch {
			logLines := readLastNLines(tryLogPath, 50)
			decision := reliability.ClassifyError(logLines, picked.Harness, &reliability.ClassifyContext{HasFileChanges: incomplete, Finalized: finalized}, result.Evidence)
			attemptFailureClass = decision.FailureClass
			failureClass = decision.FailureClass
			if decision.FailureClass == reliability.FailureInfra {
				infraFailures++
			}
			if decision.Reason != "unknown error" && markerAsText == "" {
				failReason = decision.Reason
			}
			switch decision.Strategy {
			case reliability.StrategyNoOp:
				failed = false
				success = true
			case reliability.StrategyRotate:
				r.skipFlag.Store(true)
			case reliability.StrategyWaitResume:
				if attempt < maxAttempts && decision.Cooldown > 0 {
					fmt.Println(style.DimStyle.Render(fmt.Sprintf("waiting %v for rate limit...", decision.Cooldown)))
					if r.sleepFunc != nil {
						r.sleepFunc(decision.Cooldown)
					} else {
						time.Sleep(decision.Cooldown)
					}
				}
			case reliability.StrategyFreshRestart:
				if attempt < maxAttempts {
					sessionID = ""
				}
			}
		}
		lastAttemptIncomplete = failed && attemptFailureClass == reliability.FailureIncomplete

		// A failing attempt that will be retried within budget is not a terminal
		// outcome: it gets the neutral, in-place retry line rather than a red
		// footer. Exactly one coloured footer prints when the run resolves —
		// green on success, red when the budget is exhausted (or the run breaks
		// out via skip/stop/lap-pin mismatch). A single-attempt run is terminal
		// on its first failure, so it colours immediately.
		willRetry := failed && attempt < maxAttempts &&
			!actionTaken && !r.skipFlag.Load() && !lapPinMismatch && !r.stopFlag.Load()
		renderRunFooter(r.outWriter(), style.FooterOptions{
			Passed:       !failed,
			Duration:     runtime,
			FilesChanged: filesChangedCount,
			CommitHash:   shortHash,
			CommitTitle:  commitTitle,
			FailReason:   failReason,
			Interim:      willRetry,
			Attempt:      attempt,
			MaxAttempts:  maxAttempts,
		})

		// Fold any remaining Rally state churn into history. The work commit
		// above already staged summary.jsonl via `git add -A`; this only fires
		// for no-code runs, amending a rally-authored HEAD or creating a single
		// `rally: update state` commit. User code is never staged here.
		if err := gitx.FoldRallyState(r.cfg.WorkspaceDir); err != nil {
			fmt.Fprintf(log, "relay %d run %d attempt %d rally state fold warning: %v\n", relay.ID, runIndex+1, attempt, err)
		}

		tryRecord := store.TryRecord{
			ID:            r.store.NextTryID(),
			RunID:         runIndex + 1,
			RelayID:       relay.ID,
			AgentType:     picked.Harness,
			Completed:     !failed,
			Summary:       "",
			RemainingWork: "",
			FilesChanged:  filesChangedList,
			CommitHash:    commitHash,
			CommitHistory: commitHistory,
			StartedAt:     startedAt.Format(time.RFC3339),
			EndedAt:       endedAt.Format(time.RFC3339),
			AttemptNumber: attempt,
			LogPath:       tryLogPath,
			FailReason:    failReason,
			RuntimeMs:     runtime.Milliseconds(),
			LapID:         task.LapID,
			LapAssignee:   task.Assignee,
			RecordedLaps:  recordedLaps,
			LapsAttempted: lapsAttempted,
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
		fmt.Fprintf(log, "relay %d run %d attempt %d result: completed=%v fail_reason=%q runtime=%s files_changed=%d tool_calls=%d commit=%q lap_id=%q assignee=%q recorded_laps=%v laps_attempted=%v handoff_state=%d\n",
			relay.ID, runIndex+1, attempt, !failed, failReason, runtime, filesChangedCount, tryRecord.ToolCalls, shortHash, task.LapID, task.Assignee, recordedLaps, lapsAttempted, handoffState)

		// Telemetry: per-try structured log + trace span tags. Only summaries
		// and byte sizes are emitted — never current_task.md contents or the
		// transcript (the scrubber is defense-in-depth on top of this).
		tryTags := telemetry.Tags(telemetry.EventInfo{
			RelayID: relay.ID,
			RunID:   runIndex + 1,
			TryID:   tryRecord.ID,
			Role:    task.Assignee,
			Harness: picked.Harness,
			Model:   picked.Model,
			Repo:    repoKey(r.cfg.WorkspaceDir),
			LapID:   task.LapID,
		})
		applyTags(trySpan, tryTags)
		trySpan.SetData("completed", !failed)
		trySpan.SetData("fail_reason", failReason)
		r.tel().EmitTryLog(tryCtx, map[string]interface{}{
			"event":                          "try",
			"relay_id":                       relay.ID,
			"run_id":                         runIndex + 1,
			"try_id":                         tryRecord.ID,
			"attempt":                        attempt,
			"role":                           task.Assignee,
			"runner":                         telemetry.RunnerLabel(picked.Harness, picked.Model),
			"repo":                           repoKey(r.cfg.WorkspaceDir),
			"lap_id":                         task.LapID,
			"completed":                      !failed,
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
		})

		// Capture operator-worthy failures as Sentry Issues. Ordinary
		// agent-class retries (recoverable agent errors, short no-ops) stay
		// spans/logs only to avoid alert noise.
		if failed {
			issueWorthy := failureClass == reliability.FailureInfra ||
				execErr != nil ||
				markerAsText != "" ||
				lapPinMismatch ||
				strings.Contains(strings.ToLower(failReason), "panic")
			if issueWorthy {
				r.tel().CaptureFailure(tryCtx, fmt.Sprintf("relay %d run %d try %d failed: %s", relay.ID, runIndex+1, tryRecord.ID, failReason), tryTags)
			}
		}
		trySpan.Finish()

		if err := r.store.AppendTry(tryRecord); err != nil {
			return false, false, false, "", failureClass, infraFailures, err
		}

		if actionTaken {
			if r.stopFlag.Load() {
				return false, false, true, "", failureClass, infraFailures, nil
			}
			if r.skipFlag.Load() {
				return false, false, false, "", failureClass, infraFailures, nil
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

	// Write stub entry if the agent did not finalize the run.
	stubSummary := ""
	if lastResult != nil {
		stubSummary = lastResult.Summary
	}
	wroteUnfinalized, _ := r.maybeWriteStubAndClearState(stubSummary)
	if wroteUnfinalized && !success {
		// "agent exited without finalizing" is an operator-worthy recognized
		// failure — the agent process ended without `laps done`/`laps handoff`.
		r.tel().CaptureFailure(ctx, fmt.Sprintf("relay %d run %d: agent exited without finalizing", relay.ID, runIndex+1),
			telemetry.Tags(telemetry.EventInfo{
				RelayID: relay.ID,
				RunID:   runIndex + 1,
				Role:    task.Assignee,
				Harness: picked.Harness,
				Model:   picked.Model,
				Repo:    repoKey(r.cfg.WorkspaceDir),
				LapID:   task.LapID,
			}))
	}

	addressed := false
	if lastResult != nil && lastResult.MessageAddressed != nil {
		addressed = *lastResult.MessageAddressed
	}
	return success, addressed, false, failReason, failureClass, infraFailures, nil
}

func (r *Runner) newStallController(tryLogPath string, exec agent.Executor) reliability.StallController {
	if r.stallControllerFactory != nil {
		return r.stallControllerFactory(tryLogPath)
	}
	threshold := r.cfg.StallThreshold
	if threshold <= 0 {
		threshold = reliability.DefaultStallThreshold
	}
	netStatsPath := strings.TrimSuffix(tryLogPath, ".log") + ".netstat.jsonl"
	return reliability.NewStallControllerFull(tryLogPath, threshold, r.buildLivenessProbe(exec), netStatsPath)
}

func (r *Runner) buildLivenessProbe(exec agent.Executor) *reliability.LivenessProbe {
	if !r.cfg.LivenessProbe || exec == nil || !exec.LivenessProbeSupported() {
		return nil
	}
	return reliability.NewLivenessProbe(reliability.DefaultProbeTimeout, exec.ProbeLiveness)
}

var errQueueEmpty = errors.New("laps queue empty")

func (r *Runner) resolveRunTask(ctx context.Context) (runTask, error) {
	task := runTask{
		Name:   "relay run",
		Prompt: r.cfg.TaskPrompt,
	}

	if !r.cfg.LapsEnabled {
		if task.Prompt == "" {
			task.Prompt = r.loadFreeRunPrompt()
		}
		return task, nil
	}

	lap, err := headPullLap(ctx, r.cfg.WorkspaceDir)
	if err != nil {
		return runTask{}, fmt.Errorf("pull head lap: %w", err)
	}
	if lap == laps.NoLap {
		return runTask{}, errQueueEmpty
	}

	task.Name = lap.Title
	task.LapID = lap.ID
	task.IsLapsBacked = true
	if qs, err := queueSize(ctx, r.cfg.WorkspaceDir); err == nil {
		task.LapsRemaining = qs
	}
	if strings.TrimSpace(lap.Description) != "" {
		task.Prompt = lap.Description
	} else {
		task.Prompt = lap.Title
	}
	task.Assignee = lap.Assignee

	var details []string
	if lap.ID != "" {
		details = append(details, "Lap ID: "+lap.ID)
	}
	if lap.Assignee != "" {
		details = append(details, "Assignee: "+lap.Assignee)
	}
	task.Requirements = strings.Join(details, "\n")

	return task, nil
}

// resolveRoleInstructions fills the role slot of the composed agent prompt.
// An on-disk .rally/agents/<role>.md file overrides only this slot; when none
// exists, the embedded roles/<role>.md default is used. Either way the shared
// general/ finalize and headless guidance is added separately by BuildPrompt
// and is never suppressed by an on-disk override.
func (r *Runner) resolveRoleInstructions(assignee string) (string, error) {
	if !r.cfg.LapsEnabled || strings.TrimSpace(assignee) == "" {
		return "", nil
	}

	onDisk, err := roleloader.Loader{WorkspaceDir: r.cfg.WorkspaceDir}.Load(assignee)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(onDisk) != "" {
		return onDisk, nil
	}

	// No operator override — fall back to the embedded role default.
	if embedded, ok := agent_prompt.Role(assignee); ok {
		return embedded, nil
	}
	return "", nil
}

func (r *Runner) executeTry(ctx context.Context, picked agent.ResolvedAgent, opts agent.RunOptions) (*agent.TryResult, error) {
	exec, ok := r.executors[picked.Harness]
	if !ok {
		return nil, fmt.Errorf("no executor for agent %s", picked.Harness)
	}
	return exec.Execute(ctx, opts)
}

func (r *Runner) headHash() (string, error) {
	_, inGit, err := gitx.GitRepoRoot(r.cfg.WorkspaceDir)
	if err != nil || !inGit {
		return "", nil
	}
	out, err := gitx.GitOutput(r.cfg.WorkspaceDir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// commitRange returns the commit hashes created between headBefore (exclusive)
// and headAfter (inclusive), oldest first. This captures every manual commit an
// agent made in a single try, not just the final HEAD. The last element is
// always headAfter.
func (r *Runner) commitRange(headBefore, headAfter string) []string {
	out, err := gitx.GitOutput(r.cfg.WorkspaceDir, "rev-list", "--reverse", headBefore+".."+headAfter)
	if err != nil {
		return nil
	}
	var hashes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if h := strings.TrimSpace(line); h != "" {
			hashes = append(hashes, h)
		}
	}
	return hashes
}

func (r *Runner) autoCommit(runIndex int, agentType string, attempt int) (string, error) {
	repoRoot, ok, err := gitx.GitRepoRoot(r.cfg.WorkspaceDir)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}

	if _, err := gitx.GitOutput(repoRoot, "add", "-A"); err != nil {
		return "", err
	}

	_, err = gitx.GitOutput(repoRoot, "diff", "--cached", "--quiet")
	if err == nil {
		return "", nil
	}
	var exitErr *osexec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return "", err
	}

	commitArgs := append(gitx.GitUserFallbackConfig(repoRoot), "commit")
	if !r.cfg.RunHooksOnAutoCommit {
		commitArgs = append(commitArgs, "--no-verify")
	}
	commitArgs = append(commitArgs, "-m", fmt.Sprintf("rally: run %d attempt %d (%s)", runIndex+1, attempt, agentType))
	if _, err := gitx.GitOutput(repoRoot, commitArgs...); err != nil {
		return "", err
	}

	hashOut, err := gitx.GitOutput(repoRoot, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(hashOut)), nil
}

// filesChangedList returns the list of paths that changed during the try.
// Prefers any explicit list from the agent's TryResult; falls back to a git
// diff against the recorded head before/after hashes (or the new commit
// hash); finally falls back to `git status --porcelain` (excluding rally's
// own state under `.rally/` and `.laps/`).
func (r *Runner) filesChangedList(result *agent.TryResult, headBefore, headAfter, commitHash string) []string {
	if result != nil && len(result.FilesChanged) > 0 {
		out := make([]string, len(result.FilesChanged))
		copy(out, result.FilesChanged)
		return out
	}

	repoRoot, ok, err := gitx.GitRepoRoot(r.cfg.WorkspaceDir)
	if err == nil && ok {
		var out []byte
		if headBefore != "" && headAfter != "" && headBefore != headAfter {
			out, err = gitx.GitOutput(repoRoot, "diff", "--name-only", headBefore, headAfter)
		} else if commitHash != "" {
			out, err = gitx.GitOutput(repoRoot, "diff-tree", "--no-commit-id", "--name-only", "-r", commitHash)
		}
		if err == nil && len(out) > 0 {
			return nonEmptyLines(string(out))
		}
	}

	// Last resort: list dirty files via `git status --porcelain`, excluding
	// rally's own state files so a no-op try doesn't look like real progress.
	if ok && err == nil {
		statusOut, statusErr := gitx.GitOutput(repoRoot, "status", "--porcelain")
		if statusErr == nil {
			var dirty []string
			for _, line := range strings.Split(string(statusOut), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				// Porcelain format: "XY path". Skip the two status chars and the space.
				path := line
				if len(line) > 3 {
					path = strings.TrimSpace(line[2:])
				}
				if gitx.IsRallyOwnedOrTransientPath(path) {
					continue
				}
				dirty = append(dirty, path)
			}
			if len(dirty) > 0 {
				return dirty
			}
		}
	}
	return nil
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func newProgressRunState(runID, lapID string) *progress.RunState {
	return &progress.RunState{RunID: runID, PinnedLapID: lapID, RecordedLaps: []string{}}
}

func storeLapAttempts(in []progress.LapAttempt) []store.LapAttempt {
	out := make([]store.LapAttempt, 0, len(in))
	for _, attempt := range in {
		out = append(out, store.LapAttempt{LapID: attempt.LapID, Timestamp: attempt.Timestamp})
	}
	return out
}

func mergeStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, value := range append(a, b...) {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func progressLapsCompletedForRun(workspaceDir, runID string) []string {
	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.RunID != runID {
			continue
		}
		switch lapsCompleted := entry.LapsCompleted.(type) {
		case string:
			if lapsCompleted != "" && lapsCompleted != "none" {
				out = append(out, lapsCompleted)
			}
		case []string:
			out = append(out, lapsCompleted...)
		case []interface{}:
			for _, value := range lapsCompleted {
				if s, ok := value.(string); ok && s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// normalizeFinalSnippet selects the one summary value used by retry prompts,
// try records, and synthesized summary entries. An explicit progress wrapup is
// authoritative; executor summaries are the next-best structured source.
func (r *Runner) normalizeFinalSnippet(runID, tryLogPath string, summaryEntryCountBefore int, result *agent.TryResult, execErr error) string {
	if summary := recordedWrapupSummaryForRun(r.cfg.WorkspaceDir, runID, summaryEntryCountBefore); summary != "" {
		return summary
	}
	if result != nil && strings.TrimSpace(result.Summary) != "" {
		return result.Summary
	}
	if tail := boundedFinalSnippetTail(readTryLog(tryLogPath), finalSnippetFallbackRuneLimit); tail != "" {
		return tail
	}
	if execErr != nil {
		return finalSnippetErrorIndicator(execErr)
	}
	return noFinalSnippetIndicator
}

func progressSummaryEntryCount(workspaceDir string) int {
	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		return 0
	}
	return len(entries)
}

func recordedWrapupSummaryForRun(workspaceDir, runID string, firstNewEntry int) string {
	if runID == "" {
		return ""
	}
	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		return ""
	}
	if firstNewEntry < 0 {
		firstNewEntry = 0
	}
	for i := len(entries) - 1; i >= firstNewEntry; i-- {
		entry := entries[i]
		if entry.RunID != runID {
			continue
		}
		if strings.TrimSpace(entry.Summary) != "" {
			return entry.Summary
		}
		if entry.Handoff != nil && strings.TrimSpace(entry.Handoff.Summary) != "" {
			return entry.Handoff.Summary
		}
	}
	return ""
}

func readTryLog(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func boundedFinalSnippetTail(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if text == "" || maxRunes <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}

	marker := []rune(finalSnippetTailMarker)
	if maxRunes <= len(marker) {
		return string(marker[:maxRunes])
	}
	tailSize := maxRunes - len(marker)
	return string(marker) + string(runes[len(runes)-tailSize:])
}

func finalSnippetErrorIndicator(err error) string {
	const prefix = "harness error: "
	detail := strings.Join(strings.Fields(err.Error()), " ")
	if detail == "" {
		return strings.TrimSpace(prefix)
	}
	return prefix + boundedFinalSnippetTail(detail, finalSnippetFallbackRuneLimit-len([]rune(prefix)))
}

func validatePinnedLap(pinnedLapID string, recordedLaps []string) (string, bool) {
	if pinnedLapID == "" || len(recordedLaps) == 0 {
		return "", false
	}
	unique := mergeStrings(nil, recordedLaps)
	if len(unique) > 1 {
		return "multi_lap_consumed", true
	}
	if unique[0] != pinnedLapID {
		return "wrong_lap_consumed", true
	}
	return "", false
}

// detectLapsMarkerInText returns "laps done" / "laps handoff" when the agent's
// summary contains it on its own line or as a leading marker — a strong signal
// the model emitted the command as text instead of invoking the shell tool.
func detectLapsMarkerInText(summary string) string {
	if summary == "" {
		return ""
	}
	lower := strings.ToLower(summary)
	// Check leading line and any line that begins with the marker.
	for _, raw := range strings.Split(lower, "\n") {
		line := strings.TrimSpace(raw)
		if line == "laps done" || strings.HasPrefix(line, "laps done\n") || strings.HasPrefix(line, "laps done ") {
			return "laps done"
		}
		if line == "laps handoff" || strings.HasPrefix(line, "laps handoff\n") || strings.HasPrefix(line, "laps handoff ") {
			return "laps handoff"
		}
	}
	return ""
}

func readLastNLines(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

// maybeWriteStubAndClearState writes a stub progress entry when the agent left
// run-state on disk (i.e. it never finalized via `laps done`/`laps handoff`).
// It returns wroteUnfinalized=true whenever it had to synthesize that stub so
// the caller can surface the recognized "agent exited without finalizing"
// failure to telemetry even if the model produced a partial summary first.
func (r *Runner) maybeWriteStubAndClearState(lastOutput string) (bool, error) {
	rs, err := progress.LoadRunState(r.cfg.WorkspaceDir)
	if err != nil {
		return false, err
	}
	// If no run-state file exists, LoadRunState returns a fresh empty state.
	// We only write a stub if the file actually existed on disk.
	if _, err := os.Stat(progress.RunStatePath(r.cfg.WorkspaceDir)); os.IsNotExist(err) {
		return false, nil
	}

	var lapsCompleted interface{}
	if r.cfg.LapsEnabled {
		if len(rs.RecordedLaps) > 0 {
			lapsCompleted = rs.RecordedLaps
		} else {
			lapsCompleted = "none"
		}
	}

	summary := lastOutput
	if summary == "" {
		summary = "(agent exited without finalizing)"
	}

	entry := progress.RunEntry{
		RunID:         rs.RunID,
		Summary:       summary,
		LapsCompleted: lapsCompleted,
	}
	_ = progress.AppendRunEntry(r.cfg.WorkspaceDir, entry)
	_ = progress.ClearRunState(r.cfg.WorkspaceDir)
	return true, nil
}
