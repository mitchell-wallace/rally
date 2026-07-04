package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/routing"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

type Config struct {
	WorkspaceDir     string
	DataDir          string
	MachineID        string
	AgentMixSpecs    []string
	RouteSpecs       map[string][]string
	Reasoning        map[string]string
	Providers        *routing.ProviderIndex
	UseOverrideRoute bool
	TargetIterations int
	StallThreshold   time.Duration
	LivenessProbe    bool
	RetryBudget      int
	// RunTimeout/TryTimeout/HandoffTimeout carry the effective wall-clock
	// budgets parsed from [reliability] (run_timeout_secs / try_timeout_secs /
	// handoff_timeout_secs).
	RunTimeout             time.Duration
	TryTimeout             time.Duration
	HandoffTimeout         time.Duration
	RunHooksOnAutoCommit   bool
	LapsEnabled            bool
	Instructions           string
	TaskPrompt             string
	OverwriteMixOnResume   bool
	Resolver               relaycore.Resolver
	ReasoningResolver      routing.RoleReasoningResolver
	LapsInstructionsFile   string
	FreeRunPromptFile      string
	RecentTryCount         int
	RecentTryCharLimit     int
	RecentContextCharLimit int

	// EventSink receives presentation-neutral runtime events emitted by the
	// runner at the call sites that render operator-facing output today. It is
	// the seam a presentation adapter (today's terminal sink, a future TUI)
	// subscribes to. A nil EventSink is treated as a no-op sink: [Runner.eventSink]
	// substitutes [runtimeevent.NoopSink] so emit sites never need a nil check.
	// The runner never inspects the concrete sink, keeping the seam
	// presentation-neutral. Nothing is emitted through this field yet; phase 3.1
	// wires the field only and phase 3.2 begins emitting alongside existing
	// prints.
	EventSink runtimeevent.Sink

	// StatusWriter receives the runner-driven monitor status line. A nil writer
	// preserves the terminal presentation path by writing to os.Stdout.
	StatusWriter io.Writer

	// Controls is the operator-input source the runner consumes for per-phase
	// control sessions (wait countdown, active try, pause-resume). A nil
	// Controls means no operator input, as in headless/library construction: the
	// runner takes no action from operator presses. The CLI injects a real
	// terminal control source. Nothing consumes this field yet; phase 3.1 wires
	// the field only and phase 5 cuts the keyboard sessions over to it.
	Controls runtimeevent.ControlSource
}

type Runner struct {
	store      *store.Store
	cfg        Config
	executors  map[string]harnessapi.Executor
	stopFlag   atomic.Bool
	skipFlag   atomic.Bool
	log        io.WriteCloser
	resilience *relaycore.Resilience
	relayStart time.Time

	lapsInstructionsCache string
	lapsWarned            bool
	freeRunPromptCache    string
	freeRunWarned         bool

	stallControllerFactory func(logPath string) reliability.StallController
	sleepFunc              func(time.Duration)

	// timerFunc constructs the one-shot wall-clock bound timers in runOne: the
	// shared per-run budget (built once before the attempt loop) and the
	// per-attempt try cap (built fresh each attempt). Defaults to a real
	// time.NewTimer via newBoundTimer; tests inject a fake to fire bounds
	// deterministically without real multi-second sleeps. The returned stop func
	// releases the timer, mirroring time.Timer.Stop.
	timerFunc func(d time.Duration) (<-chan time.Time, func() bool)

	// forceKillFunc is the escalation hook for a second quit-now during the
	// cancel drain. Defaults to reliability.ForceKillProcessGroup; tests inject
	// a fake to assert the force-kill path is taken without signalling a real
	// process group.
	forceKillFunc func(pgid int) error

	telemetry telemetry.Sink
}

// newBoundTimer builds a one-shot wall-clock bound timer for runOne. It routes
// through timerFunc when a test has injected one, otherwise constructs a real
// time.Timer. The returned channel fires once when the bound elapses; the stop
// func releases the timer (no-op-safe to call after it has fired).
func (r *Runner) newBoundTimer(d time.Duration) (<-chan time.Time, func() bool) {
	if r.timerFunc != nil {
		return r.timerFunc(d)
	}
	t := time.NewTimer(d)
	return t.C, t.Stop
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

// eventSink returns the active runtime-event sink, defaulting to a no-op so
// emit sites never need a nil check. A nil [Config.EventSink] (direct
// library/test construction, or the CLI before it wires a real sink) means no
// presentation is attached; [runtimeevent.NoopSink] keeps call sites branch-free
// so emitting through a headless runner is silently discarded.
func (r *Runner) eventSink() runtimeevent.Sink {
	if r.cfg.EventSink == nil {
		return runtimeevent.NoopSink{}
	}
	return r.cfg.EventSink
}

// statusWriter returns the destination for the monitor status line. A nil
// [Config.StatusWriter] writes to stdout for CLI callers.
func (r *Runner) statusWriter() io.Writer {
	if r.cfg.StatusWriter == nil {
		return os.Stdout
	}
	return r.cfg.StatusWriter
}

func NewRunner(s *store.Store, cfg Config, executors map[string]harnessapi.Executor) *Runner {
	return &Runner{
		store:     s,
		cfg:       cfg,
		executors: executors,
	}
}

func (r *Runner) RequestStop() {
	r.stopFlag.Store(true)
}

func (r *Runner) Run(ctx context.Context) error {
	relay, routeRuntime, log, err := r.startOrResumeRelay()
	if err != nil {
		return err
	}
	defer func() {
		_ = log.Close()
	}()

	ctx, relaySpan, rc := r.startRelaySpan(ctx, relay)
	defer relaySpan.Finish()

	// Failover: on every relay exit path, make sure summary.jsonl is not left as
	// an uncommitted change. The per-run amend normally folds it into the run's
	// commit, so this should be a no-op; when it fires it is recorded to New
	// Relic so the leftover is visible. Registered after relaySpan so it runs
	// before the span finishes (LIFO), keeping the event inside the transaction.
	defer r.commitLeftoverSummary(ctx, relay, rc)

	resilience := r.resilience
	if resilience == nil {
		resilience = relaycore.NewResilience(r.store)
	}

	relayMsg, err := r.consumeRelayScopedMessage(relay)
	if err != nil {
		return err
	}

	runIndex := relay.CompletedIterations
	var fallbackCause *routeFallbackCause
	for relay.CompletedIterations < relay.TargetIterations {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.stopFlag.Load() {
			fmt.Fprintf(log, "relay %d stop requested, halting after current try\n", relay.ID)
			break
		}

		task, selection, ready, done, err := r.selectRouteOrWait(ctx, relay, runIndex, routeRuntime, resilience, rc, log)
		if err != nil {
			return err
		}
		if done {
			break
		}
		if !ready {
			continue
		}

		runID := runIndex + 1
		runCtx, runSpan := r.startRunSpan(ctx, relay, runID, task, selection, rc)
		fallbackCause = r.emitFallbackEvents(ctx, runCtx, relay, runID, task, selection, fallbackCause, rc, runSpan, log)

		consumedMsg, err := r.consumeRunScopedMessage(runID)
		if err != nil {
			return err
		}

		res, err := r.runOne(
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
		if res.Interrupted {
			fmt.Fprintf(log, "relay %d stop requested, halting\n", relay.ID)
			break
		}

		fallbackCause = r.resolveFallbackCause(runID, selection, res)

		if r.skipFlag.Load() {
			runIndex, err = r.updateSkippedRunProgress(relay, selection, runIndex)
			if err != nil {
				return err
			}
			continue
		}

		if err := r.applyRunOutcomeToResilience(relay, runIndex, selection, res, routeRuntime, resilience, log); err != nil {
			return err
		}

		runIndex, err = r.updateRunProgress(relay, relayMsg, consumedMsg, res, runIndex)
		if err != nil {
			return err
		}
	}

	if err := r.completeRelayIfTargetMet(relay, log); err != nil {
		return err
	}

	r.printRelaySummary(relay)

	return nil
}
