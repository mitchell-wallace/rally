package relay

import (
	"bufio"
	"context"
	"encoding/json"
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
	"github.com/mitchell-wallace/rally/internal/routing"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/style"
	"github.com/mitchell-wallace/rally/internal/telemetry"
	"github.com/mitchell-wallace/rally/internal/textutil"
	"github.com/mitchell-wallace/rally/internal/user_prompt/roleloader"
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
	Resolver               Resolver
	ReasoningResolver      routing.RoleReasoningResolver
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

	// out is the console writer for run headers and outcome footers. Defaults
	// to os.Stdout via outWriter; tests inject a buffer to assert on footer
	// cadence and colouring.
	out io.Writer

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

// rallyContext builds the telemetry RallyContext for a relay: the anonymous
// machine identity, relay start, repo key, and home-collapsed cwd attached to
// the relay span and every captured failure.
func (r *Runner) rallyContext(relay *store.RelayRecord) telemetry.RallyContext {
	return telemetry.RallyContext{
		RelayID:        relay.ID,
		RelayStartedAt: relay.StartedAt,
		Repo:           repoKey(r.cfg.WorkspaceDir),
		RepoName:       repoDisplayName(r.cfg.WorkspaceDir),
		MachineID:      r.cfg.MachineID,
		Cwd:            r.cfg.WorkspaceDir,
	}
}

// applyRallyContext attaches the base correlation tags, the machine-identity /
// relay-guid tags, and the `rally` context block to a span. It is the span-side
// twin of rallyFailure so the relay span and every captured failure carry the
// same identity. The full machine id rides only in the `rally` data block, never
// as a tag.
func applyRallyContext(span telemetry.Span, baseTags map[string]string, rc telemetry.RallyContext) {
	applyTags(span, baseTags)
	applyTags(span, telemetry.MachineTags(rc))
	span.SetData("rally", telemetry.RallyContextBlock(rc))
}

// rallyFailure assembles a FailureEvent that carries the correlation tags, the
// machine-identity / relay-guid tags, and the `rally` context block. The base
// tags are copied so callers that also applied them to a span are unaffected.
func rallyFailure(tags map[string]string, rc telemetry.RallyContext) telemetry.FailureEvent {
	merged := make(map[string]string, len(tags)+3)
	for k, v := range tags {
		merged[k] = v
	}
	for k, v := range telemetry.MachineTags(rc) {
		merged[k] = v
	}
	return telemetry.FailureEvent{
		Tags:     merged,
		Contexts: map[string]map[string]interface{}{"rally": telemetry.RallyContextBlock(rc)},
	}
}

// failureStateEvent layers a failure-state snapshot onto a rally failure event:
// the attempt/max_attempts/failure_category/agent_state tags (plus
// quota_scope/reset for limit categories), and the bounded failure_evidence
// context block for limit categories. It is the one place the runner folds the
// structured failure state onto a captured failure, so the terminal-try,
// unfinalized, and relay-stall sites attach consistent fields. Telemetry never
// re-classifies here — callers pass the category/evidence already resolved in
// runOne. Sites that lack a field (e.g. the relay-level all-frozen capture has
// no attempt or reset evidence) simply leave it zero, and FailureStateTags omits
// it.
func failureStateEvent(baseTags map[string]string, rc telemetry.RallyContext, fs telemetry.FailureState) telemetry.FailureEvent {
	evt := rallyFailure(baseTags, rc)
	for k, v := range telemetry.FailureStateTags(fs) {
		evt.Tags[k] = v
	}
	if ec := telemetry.FailureEvidenceContext(fs); ec != nil {
		evt.Contexts["failure_evidence"] = ec
	}
	evt.Fingerprint = telemetry.FailureFingerprint(evt.Tags)
	return evt
}

func limitSignalEvent(baseTags map[string]string, rc telemetry.RallyContext, fs telemetry.FailureState) (telemetry.Event, bool) {
	if !runnerLimitCategory(fs.Category) {
		return telemetry.Event{}, false
	}
	evt := failureStateEvent(baseTags, rc, fs)
	if _, ok := evt.Contexts["failure_evidence"]; !ok {
		return telemetry.Event{}, false
	}
	evt.Tags["event_kind"] = "limit_signal"
	return telemetry.Event{
		Level:    telemetry.LevelInfo,
		Tags:     evt.Tags,
		Contexts: evt.Contexts,
	}, true
}

func runnerLimitCategory(category string) bool {
	switch reliability.FailureCategory(category) {
	case reliability.CategoryUsageLimit, reliability.CategoryShortRateLimit, reliability.CategoryProviderOverloaded:
		return true
	default:
		return false
	}
}

func applyEvidenceToFailureState(fs *telemetry.FailureState, ev *reliability.FailureEvidence, source string) {
	if fs == nil || ev == nil {
		return
	}
	if ev.QuotaScope != "" {
		fs.QuotaScope = ev.QuotaScope
	}
	fs.ResetAt = ev.ResetAt
	fs.ResetAfter = ev.ResetAfter
	if runnerLimitCategory(string(ev.Category)) {
		fs.RawSignal = ev.RawSignal
		fs.Message = ev.Message
		return
	}
	fs.EvidenceRawSignal = ev.RawSignal
	fs.EvidenceMessage = ev.Message
	// Prefer the evidence's own Source tag (set by the classification path)
	// over the caller-supplied default — this is how dirty_tree / text_pattern
	// / unmatched evidence propagates its source through to telemetry.
	if ev.Source != "" {
		fs.EvidenceSource = ev.Source
	} else {
		fs.EvidenceSource = source
	}
}

func applySafeExecErrorEvidence(fs *telemetry.FailureState, err error) {
	if fs == nil || err == nil {
		return
	}
	if fs.RawSignal != "" || fs.Message != "" || fs.EvidenceRawSignal != "" || fs.EvidenceMessage != "" {
		return
	}
	fs.EvidenceRawSignal = err.Error()
	fs.EvidenceSource = "safe_exec_error"
}

func addFailureEvidenceTelemetry(span telemetry.Span, fields map[string]interface{}, fs telemetry.FailureState) {
	evidence := telemetry.FailureEvidenceContext(fs)
	if len(evidence) == 0 {
		return
	}
	if span != nil {
		span.SetData("failure_evidence", evidence)
	}
	for k, v := range telemetry.FailureEvidenceFields(fs) {
		fields[k] = v
	}
}

func lapPinMismatchDiagnosticEvent(baseTags map[string]string, rc telemetry.RallyContext, fs telemetry.FailureState, reason string, expectedLapID string, consumedLapIDs []string) telemetry.Event {
	evt := failureStateEvent(baseTags, rc, fs)
	delete(evt.Tags, "failure_category")
	evt.Tags["event_kind"] = "lap_pin_mismatch"
	evt.Tags["mismatch_reason"] = reason
	if expectedLapID != "" {
		evt.Tags["expected_lap_id"] = expectedLapID
	}
	evt.Tags["consumed_lap_count"] = fmt.Sprintf("%d", len(consumedLapIDs))
	if len(consumedLapIDs) > 0 {
		evt.Tags["consumed_lap_ids"] = strings.Join(consumedLapIDs, ",")
	}
	return telemetry.Event{
		Level:    telemetry.LevelWarning,
		Tags:     evt.Tags,
		Contexts: evt.Contexts,
	}
}

// agentStateName reports the failing runner's current resilience standing using
// the verbatim active/probation/frozen/benched vocabulary, for the agent_state
// tag on captured failures. It reads persisted state from the store so it
// reflects the runner's standing at capture time.
func (r *Runner) agentStateName(picked agent.ResolvedAgent) string {
	res := r.resilience
	if res == nil {
		res = NewResilience(r.store)
	}
	state, _ := res.GetState(KeyFromAgent(picked))
	return string(state)
}

var headPullLap = func(ctx context.Context, workspaceDir string) (laps.Lap, error) {
	return (&laps.Adapter{WorkspaceDir: workspaceDir}).ClaimHead(ctx)
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
const incompleteRetryGuidance = "The last run was incomplete. Check any current git changes, finish anything not done, verify correctness, commit when good, then run `laps done` for the claimed lap."

func formatCategorizedDisplay(cat reliability.FailureCategory, cooldown time.Duration, evidence *reliability.FailureEvidence) string {
	label := reliability.CategoryDisplayLabel(cat)
	switch cat {
	case reliability.CategoryUsageLimit:
		// Only show a reset time backed by parsed evidence. The classifier's
		// cooldown is a legacy wait default, not the quota reset — the actual
		// bench window without evidence is BenchDefaultDuration, so echoing
		// the cooldown would display a deadline that does not match the bench.
		if dur := usageResetDuration(evidence); dur > 0 {
			return fmt.Sprintf("%s, resets in %s", label, formatHoursMinutes(dur))
		}
		return label
	case reliability.CategoryShortRateLimit:
		if cooldown > 0 {
			return fmt.Sprintf("%s, waiting %s", label, formatMinutesSeconds(cooldown))
		}
		return label
	default:
		return label
	}
}

func usageResetDuration(evidence *reliability.FailureEvidence) time.Duration {
	if evidence == nil {
		return 0
	}
	if evidence.ResetAfter > 0 {
		return evidence.ResetAfter
	}
	if evidence.ResetAt != nil {
		if remaining := time.Until(*evidence.ResetAt); remaining > 0 {
			return remaining
		}
	}
	return 0
}

func formatHoursMinutes(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func formatMinutesSeconds(d time.Duration) string {
	minutes := int(d.Minutes())
	if minutes > 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

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
func waitLoop(ctx context.Context, total time.Duration, msgFmt string, actionCh <-chan keyboard.Press, out io.Writer, tickInterval time.Duration) waitOutcome {
	deadline := time.Now().Add(total)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	var armedAction keyboard.Action
	var armedUntil time.Time
	lastFrame := ""

	render := func(remaining time.Duration) {
		if remaining < 0 {
			remaining = 0
		}
		remaining = remaining.Round(time.Second)
		line := style.DimStyle.Render(fmt.Sprintf(msgFmt, formatRemaining(remaining)))
		// The second line is normally the dim shortcut legend; while a shortcut
		// is armed it becomes a highlighted "press X again…" hint so the
		// operator sees the first press registered.
		hint := style.ShortcutHint()
		if armedAction != keyboard.ActionNone {
			if time.Now().Before(armedUntil) {
				hint = style.WarningStyle.Render("⌨ " + keyboard.ArmMessage(armedAction))
			} else {
				armedAction = keyboard.ActionNone
			}
		}
		// Only repaint when the visible frame actually changes. With a
		// minute-granularity countdown (see formatRemaining) this collapses a
		// long wait from one repaint per second to one per minute, killing the
		// scroll/noise the operator was seeing.
		frame := line + "\x00" + hint
		if frame == lastFrame {
			return
		}
		lastFrame = frame
		// \r\x1b[J clears from cursor to end of screen so a shorter countdown
		// can't leave stale characters. The \r\n (carriage return + line feed)
		// between the two lines matters in raw mode: a bare \n there only feeds
		// the line without returning the column, which is what made the hint
		// stair-step onto fresh lines. The trailing \x1b[1A\r parks the cursor
		// back at the start of the countdown line ready for the next repaint.
		fmt.Fprintf(out, "\r\x1b[J%s\r\n%s\x1b[1A\r", line, hint)
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
		case press := <-actionCh:
			if !press.Confirmed {
				// Ignore pause arming entirely — there is no try to pause —
				// otherwise show the "press again" hint for this shortcut.
				if press.Action != keyboard.ActionPause {
					armedAction = press.Action
					armedUntil = time.Now().Add(keyboard.ConfirmWindow)
					render(time.Until(deadline))
				}
				continue
			}
			switch press.Action {
			case keyboard.ActionSkip:
				clear()
				return waitSkipped
			case keyboard.ActionStop, keyboard.ActionQuit:
				clear()
				return waitStopped
			}
			// Ignore a confirmed pause during a wait — there is no active try.
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

// formatRemaining renders d for the wait countdown. Below a minute it shows
// seconds (`Ss`); from a minute up it shows only whole minutes (`Mm` / `Hh Mm`)
// and drops the seconds component. Coarsening above a minute means the rendered
// string only changes once per minute, so the countdown repaints once a minute
// during a long wait instead of every second — the dominant noise reduction.
func formatRemaining(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second).Seconds())
	if total < 60 {
		return fmt.Sprintf("%ds", total)
	}
	h := total / 3600
	m := (total % 3600) / 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

type runTask struct {
	Name              string
	Requirements      string
	Prompt            string
	Assignee          string
	EffectiveAssignee string
	ResolvedRoute     string
	LapID             string
	IsLapsBacked      bool
	LapsRemaining     int
}

func (t runTask) promptAssignee() string {
	if strings.TrimSpace(t.EffectiveAssignee) != "" {
		return t.EffectiveAssignee
	}
	return t.Assignee
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
	routeRuntime.store = r.store

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
	rc := r.rallyContext(relay)
	ctx, relaySpan := r.tel().StartSpan(ctx, "relay", fmt.Sprintf("relay-%d", relay.ID))
	relayTags := telemetry.Tags(telemetry.EventInfo{RelayID: relay.ID, Repo: rc.Repo, RepoName: rc.RepoName})
	applyRallyContext(relaySpan, relayTags, rc)
	defer relaySpan.Finish()

	// Failover: on every relay exit path, make sure summary.jsonl is not left as
	// an uncommitted change. The per-run amend normally folds it into the run's
	// commit, so this should be a no-op; when it fires it is recorded to New
	// Relic so the leftover is visible. Registered after relaySpan so it runs
	// before the span finishes (LIFO), keeping the event inside the transaction.
	defer r.commitLeftoverSummary(ctx, relay, rc)

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
	var fallbackCause *routeFallbackCause
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
					// This is a relay-level state, not a single try: it carries only
					// agent_state=frozen and the relay/global context, with no
					// try_id, attempt, or reset evidence (those zero fields are
					// omitted by FailureStateTags).
					r.tel().CaptureFailure(ctx, fmt.Sprintf("relay %d stalled: all agents frozen", relay.ID),
						failureStateEvent(
							telemetry.Tags(telemetry.EventInfo{RelayID: relay.ID, Repo: rc.Repo, RepoName: rc.RepoName}),
							rc,
							telemetry.FailureState{AgentState: string(StateFrozen)},
						))
					_ = CompleteRelay(r.store, relay.ID)
					return fmt.Errorf("relay failed: all agents frozen")
				}
				if routeErr.Wait <= 0 {
					fmt.Fprintf(log, "relay %d failed: %s\n", relay.ID, routeErr.Error())
					_ = CompleteRelay(r.store, relay.ID)
					return fmt.Errorf("relay failed: %s", routeErr.Error())
				}
				fmt.Fprintf(log, "relay %d all agents paused, waiting %v\n", relay.ID, routeErr.Wait)
				outcome, waitErr := waitWithCountdown(ctx, routeErr.Wait, "agents paused, waiting %s...")
				if waitErr != nil {
					return waitErr
				}
				switch outcome {
				case waitSkipped:
					unpaused, err := routeRuntime.forceUnpauseAll(resilience, relay.ID, routeErr.RouteName, routeErr.EffectiveAssignee)
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
		task.ResolvedRoute = selection.Route.Name
		task.EffectiveAssignee = selection.EffectiveAssignee
		r.prepareExecutorForSelection(relay.ID, runIndex, selection, log)

		// Consume run-scoped message at start of each run
		// First check if there's an already-consumed message from a failed run
		runID := runIndex + 1

		runTags := telemetry.Tags(telemetry.EventInfo{
			RelayID:  relay.ID,
			RunID:    runID,
			Role:     task.promptAssignee(),
			Harness:  selection.Agent.Harness,
			Model:    selection.Agent.Model,
			Repo:     rc.Repo,
			RepoName: rc.RepoName,
			LapID:    task.LapID,
		})
		runCtx, runSpan := r.tel().StartSpan(ctx, "run", fmt.Sprintf("relay-%d-run-%d", relay.ID, runID))
		applyTags(runSpan, runTags)

		// Rotating to a backup runner is a healthy recovery, not an alert. Record
		// it on the routing event stream, not as a try outcome.
		if selection.PreviousAgent != nil &&
			(selection.PreviousAgent.Harness != selection.Agent.Harness ||
				selection.PreviousAgent.Model != selection.Agent.Model) {
			from := telemetry.RunnerLabel(selection.PreviousAgent.Harness, selection.PreviousAgent.Model)
			to := telemetry.RunnerLabel(selection.Agent.Harness, selection.Agent.Model)
			fmt.Fprintf(log, "relay %d run %d route fallback: rotated %s -> %s\n", relay.ID, runID, from, to)
			runSpan.SetTag("route_fallback", "true")
			runSpan.SetData("route_fallback", true)
			runSpan.SetTag("from_runner", from)
			runSpan.SetTag("to_runner", to)
			fields := map[string]interface{}{
				"event":       "route_fallback",
				"relay_id":    relay.ID,
				"run_id":      runID,
				"from_runner": from,
				"to_runner":   to,
				"role":        task.promptAssignee(),
				"repo":        rc.Repo,
				"repo_name":   rc.RepoName,
				"lap_id":      task.LapID,
			}
			if fallbackCause != nil && fallbackCause.fromRunner == from {
				fallbackCause.addTo(fields, runSpan)
			}
			fallbackCause = nil
			r.tel().EmitRouteEvent(runCtx, fields)
		} else if fallbackCause != nil {
			fallbackCause = nil
		}
		if selection.RecoveryCapHit {
			to := telemetry.RunnerLabel(selection.Agent.Harness, selection.Agent.Model)
			from := to
			if selection.PreviousAgent != nil {
				from = telemetry.RunnerLabel(selection.PreviousAgent.Harness, selection.PreviousAgent.Model)
			}
			r.tel().EmitRouteEvent(runCtx, map[string]interface{}{
				"event":                        "route_fallback",
				"relay_id":                     relay.ID,
				"run_id":                       runID,
				"from_runner":                  from,
				"to_runner":                    to,
				"role":                         task.promptAssignee(),
				"repo":                         rc.Repo,
				"repo_name":                    rc.RepoName,
				"lap_id":                       task.LapID,
				"route_name":                   selection.Route.Name,
				"consecutive_recovery_runs":    selection.RecoveryStatus.ConsecutiveRecoveryRuns,
				"recovery_classification":      "needs_user",
				"route_entry_exhausted_reason": "recovery_cap_hit",
			})
			r.tel().CaptureFailure(ctx, fmt.Sprintf("relay %d lap %s recovery cap reached: needs_user", relay.ID, task.LapID),
				failureStateEvent(
					telemetry.Tags(telemetry.EventInfo{RelayID: relay.ID, RunID: runID, Role: task.promptAssignee(), Repo: rc.Repo, RepoName: rc.RepoName, LapID: task.LapID}),
					rc,
					telemetry.FailureState{RecoveryClassification: "needs_user"},
				))
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
		if !res.Success {
			reason := "retry-budget-exhausted"
			switch {
			case res.Category != "":
				reason = "category:" + string(res.Category)
			case r.skipFlag.Load():
				reason = "skip"
			case res.Outcome != "":
				reason = "outcome:" + string(res.Outcome)
			}
			fallbackCause = &routeFallbackCause{
				fromRunner:           telemetry.RunnerLabel(selection.Agent.Harness, selection.Agent.Model),
				triggerRunID:         runID,
				triggerTryID:         r.store.NextTryID() - 1,
				triggerOutcome:       string(res.Outcome),
				triggerFailReason:    res.FailReason,
				triggerFailureClass:  string(res.FailureClass),
				triggerFailureCat:    string(res.Category),
				triggerLapID:         res.LapID,
				routeName:            selection.Route.Name,
				entryExhaustedReason: reason,
			}
		} else {
			fallbackCause = nil
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
		if !res.Success {
			selection.Scheduler.OnAgentFailed(selection.Entry, "retry-budget-exhausted", false)
		}

		// Surface the resolved failure category and any parsed reset deadline
		// from runOne, then act on it: a usage_limit benches the whole quota
		// scope until the reset (below); other categories (auth_or_proxy,
		// invalid_model) are left to the scheduler's normal exhaustion/route-away
		// path. The log line records the resolution for operator triage.
		if !res.Success && res.Category != "" {
			resetNote := "none"
			if res.ResetEvidence != nil {
				if res.ResetEvidence.ResetAt != nil {
					resetNote = "reset_at=" + res.ResetEvidence.ResetAt.UTC().Format(time.RFC3339)
				} else if res.ResetEvidence.ResetAfter > 0 {
					resetNote = "reset_after=" + res.ResetEvidence.ResetAfter.String()
				}
			}
			fmt.Fprintf(log, "relay %d run %d resolved failure category=%s reset=%s\n",
				relay.ID, runIndex+1, res.Category, resetNote)

			// On a usage_limit, bench the entire exhausted quota scope across
			// every lane until the reset deadline so siblings sharing the same
			// account front leave rotation together, then wait it out rather
			// than thrashing the limit. Other terminal categories (auth_or_proxy,
			// invalid_model) are routed away by the scheduler's normal exhaustion
			// path and are not benched here.
			if res.Category == reliability.CategoryUsageLimit {
				resetAt := benchResetDeadline(res.ResetEvidence, time.Now())
				scope := routeRuntime.quotaScope(selection.Agent.Harness, selection.Agent.Model)
				benched, benchErr := routeRuntime.benchQuotaScope(resilience, scope, resetAt, relay.ID, selection.Route.Name, selection.EffectiveAssignee)
				if benchErr != nil {
					return benchErr
				}
				fmt.Fprintf(log, "relay %d run %d benched quota scope %q until %s (%d key(s))\n",
					relay.ID, runIndex+1, scope, resetAt.UTC().Format(time.RFC3339), benched)
			}
		}

		if selection.Probation {
			if res.Success || res.FailureClass == reliability.FailureIncomplete {
				if err := resilience.UnpauseAgent(KeyFromAgent(selection.Agent), relay.ID); err != nil {
					return err
				}
			} else {
				if err := resilience.FreezeAgent(KeyFromAgent(selection.Agent), relay.ID, "probation run failed"); err != nil {
					return err
				}
			}
		} else if selection.HourlyRetry {
			if res.Success {
				if err := resilience.UnpauseAgent(KeyFromAgent(selection.Agent), relay.ID); err != nil {
					return err
				}
			} else if res.FailureClass == reliability.FailureInfra && res.InfraFailures > 1 {
				if err := resilience.RecordHourlyFailure(KeyFromAgent(selection.Agent), relay.ID); err != nil {
					return err
				}
			}
		} else {
			if !res.Success && res.FailureClass == reliability.FailureInfra && res.InfraFailures > 1 {
				if err := resilience.PauseAgent(KeyFromAgent(selection.Agent), relay.ID); err != nil {
					return err
				}
			}
		}

		if res.Success {
			relay.CompletedIterations++
			runIndex++
			if consumedMsg != nil && res.Addressed {
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
			if relayMsg != nil && res.Addressed && relayMsg.Status == "pending" {
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
	passCount, failCount, cancelledCount := tallyRuns(r.store.AllTries(), relay.ID)
	totalRuns := passCount + failCount + cancelledCount
	if totalRuns > 0 {
		totalDuration := time.Since(r.relayStart)
		summary := style.RenderSummary(totalRuns, passCount, failCount, totalDuration, cancelledCount)
		fmt.Println(summary)
	}

	return nil
}

// commitLeftoverSummary is the end-of-relay failover for summary.jsonl: if the
// tracked .rally/summary.jsonl is still dirty when the relay exits, it commits
// just that file and records a RallyDiagnostic so the leftover is visible in
// New Relic. With the per-run amend fold in place this should rarely fire; a
// firing means a run finished without folding its summary, which is the signal
// worth surfacing. Best-effort: git/telemetry failures are logged, not fatal.
func (r *Runner) commitLeftoverSummary(ctx context.Context, relay *store.RelayRecord, rc telemetry.RallyContext) {
	if relay == nil {
		return
	}
	dir := r.cfg.WorkspaceDir
	if _, inGit, err := gitx.GitRepoRoot(dir); err != nil || !inGit {
		return
	}

	const rel = ".rally/summary.jsonl"
	out, err := gitx.GitOutput(dir, "status", "--porcelain", "--", rel)
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return // clean — nothing left over
	}

	if _, err := gitx.GitOutput(dir, "add", "--", rel); err != nil {
		r.logf("relay %d leftover summary stage warning: %v\n", relay.ID, err)
		return
	}
	// Confirm something is actually staged (the add can be a no-op if the path
	// is operator-ignored).
	if _, err := gitx.GitOutput(dir, "diff", "--cached", "--quiet", "--", rel); err == nil {
		return
	}

	args := append(gitx.GitUserFallbackConfig(dir), "commit", "--no-verify", "-m", "rally: commit leftover summary", "--", rel)
	if _, err := gitx.GitOutput(dir, args...); err != nil {
		r.logf("relay %d leftover summary commit warning: %v\n", relay.ID, err)
		return
	}
	r.logf("relay %d committed leftover summary.jsonl via end-of-relay failover\n", relay.ID)

	r.tel().CaptureEvent(ctx, "relay left summary.jsonl uncommitted; committed via failover", telemetry.Event{
		Level: telemetry.LevelWarning,
		Tags:  telemetry.Tags(telemetry.EventInfo{RelayID: relay.ID, Repo: rc.Repo, RepoName: rc.RepoName}),
	})
}

// logf writes to the relay log when one is open; it is a no-op otherwise.
func (r *Runner) logf(format string, args ...interface{}) {
	if r.log != nil {
		fmt.Fprintf(r.log, format, args...)
	}
}

// tallyRuns aggregates try records into run-level pass/fail/cancelled counts
// for the given relay. Each run (identified by RunID) is counted exactly once:
// it passes if any attempt ultimately completed, is cancelled if no attempt
// completed and an operator-cancelled attempt resolved the run, and fails only
// when every attempt exhausted without completion or cancellation.
func tallyRuns(tries []store.TryRecord, relayID int) (passCount, failCount, cancelledCount int) {
	type runState struct {
		completed bool
		cancelled bool
	}
	byRun := make(map[int]runState)
	order := make([]int, 0)
	for _, tr := range tries {
		if tr.RelayID != relayID {
			continue
		}
		state, seen := byRun[tr.RunID]
		if !seen {
			order = append(order, tr.RunID)
		}
		if tr.Completed || tr.Outcome.IsSuccess() {
			state.completed = true
		}
		if tr.Outcome == reliability.OutcomeCancelled {
			state.cancelled = true
		}
		byRun[tr.RunID] = state
	}
	for _, runID := range order {
		state := byRun[runID]
		switch {
		case state.completed:
			passCount++
		case state.cancelled:
			cancelledCount++
		default:
			failCount++
		}
	}
	return passCount, failCount, cancelledCount
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
		fmt.Fprintf(&buf, "Run %d (%s): %s summary=%s\n", t.RunID, t.AgentType, recentContextStatus(t), summary)
	}
	if overallLimit > 0 && buf.Len() > overallLimit {
		result := buf.String()
		headSize := overallLimit / 2
		tailSize := overallLimit - headSize
		return result[:headSize] + textutil.HeadTailTruncationMarker + result[len(result)-tailSize:]
	}
	return buf.String()
}

func recentContextStatus(t store.TryRecord) string {
	if t.Outcome == "" {
		return fmt.Sprintf("completed=%v", t.Completed)
	}
	status := "outcome=" + string(t.Outcome)
	if t.Outcome == reliability.OutcomeCancelled && strings.TrimSpace(t.CancellationSource) != "" {
		status += " source=" + strings.TrimSpace(t.CancellationSource)
	}
	return status
}

// forceKillGroup escalates the cancel drain to an immediate group-wide SIGKILL,
// routing through the injectable hook so tests can observe the escalation.
func (r *Runner) forceKillGroup(pgid int) error {
	if r.forceKillFunc != nil {
		return r.forceKillFunc(pgid)
	}
	return reliability.ForceKillProcessGroup(pgid)
}

// drainTimedOut handles a wall-clock timeout arm in the action loop: it cancels
// the running attempt, records the timeout (and whether the run budget was the
// trigger), then blocks for the cancelled attempt's result so the goroutine that
// owns tryCh does not leak. It mirrors the pause/skip drain of a single tryCh
// receive after cancelAttempt.
func (r *Runner) drainTimedOut(d actionLoopDeps, out *actionLoopResult, runBudget bool) {
	d.cancelAttempt()
	out.timedOut = true
	out.runBudgetExhausted = runBudget
	res := <-d.tryCh
	out.result = res.result
	out.execErr = res.err
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
	SetArmed(msg string, ttl time.Duration)
	SetActing(msg string)
}

// actionLoopDeps bundles the channels and collaborators the in-try action loop
// selects over. Splitting them out lets [Runner.runActionLoop] be driven by a
// fake executor/try channel and simulated keyboard.Action values in tests.
type actionLoopDeps struct {
	tryCh     <-chan tryResult
	pidCh     <-chan int
	actionCh  <-chan keyboard.Press
	stallTick <-chan time.Time
	// runBudgetCh fires when the per-run wall-clock budget is exhausted. It is
	// constructed ONCE before the attempt loop and the same channel is passed
	// into every runActionLoop invocation, so it measures cumulative time across
	// all retries rather than resetting per attempt. A nil channel disables the
	// run budget (blocks forever in select).
	runBudgetCh <-chan time.Time
	// tryDeadline fires when the per-attempt cap is exhausted. Unlike runBudgetCh
	// it MAY be created fresh each attempt (mirroring stallTick), so it bounds a
	// single attempt without consuming the shared run budget. A nil channel
	// disables the per-try cap.
	tryDeadline     <-chan time.Time
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
	// timedOut is set when a wall-clock bound (run budget or per-try cap)
	// cancelled the attempt, distinguishing it from a stall, an operator action,
	// or an ordinary agent error in post-loop handling.
	timedOut bool
	// runBudgetExhausted distinguishes a run-budget timeout (stop retrying,
	// proceed to the bounded handoff) from a per-try cap firing with run budget
	// still remaining (the attempt may retry). Only meaningful when timedOut.
	runBudgetExhausted bool
	// cancellationSource records the operator-initiated cancellation that ended
	// this attempt. When non-empty, the attempt is classified as
	// OutcomeCancelled rather than failed/success, and all failure taxonomy,
	// retry scheduling, and resilience counter updates are skipped.
	cancellationSource CancellationSource
}

// CancellationSource identifies the explicit operator action that cancelled an
// attempt. It is intentionally separate from actionTaken/skipFlag/stopFlag so
// outcome derivation can honor operator intent before executor exit handling.
type CancellationSource string

const (
	CancellationSourceNone         CancellationSource = ""
	CancellationSourceSkip         CancellationSource = "skip"
	CancellationSourceGracefulStop CancellationSource = "graceful_stop"
	CancellationSourceQuitNow      CancellationSource = "quit_now"
)

func (s CancellationSource) String() string {
	return string(s)
}

// runActionLoop is the in-try select-based state machine. It blocks until the
// attempt resolves on tryCh, or an operator action redirects it:
//
//   - Ctrl+S (skip): cancel the attempt, record source=skip, then drain tryCh.
//   - Ctrl+P (pause): cancel the attempt, then drain tryCh.
//   - Ctrl+X (stop): cancel the attempt, record source=graceful_stop, drain
//     tryCh, and halt the relay afterwards.
//   - Ctrl+C (quit now): set stopFlag, cancel the attempt immediately, and drain
//     tryCh while staying responsive — a second Ctrl+C within the grace window
//     escalates to a group-wide SIGKILL via forceKillGroup rather than waiting
//     the window out.
//
// While stop/quit drains, the monitor shows a "stopping…" indicator so the UI
// does not look frozen. Stall detection (stallTick) and late pid reports
// (pidCh) are handled alongside the action cases.
//
// Two wall-clock bounds join the select as their own arms, mirroring stallTick:
// runBudgetCh (the shared per-run budget across retries) and tryDeadline (the
// per-attempt cap). On fire either cancels the attempt, marks the result
// timed-out, drains tryCh, and breaks — so whichever of run budget, per-try cap,
// or stall fires first wins.
func (r *Runner) runActionLoop(d actionLoopDeps) actionLoopResult {
	var out actionLoopResult
actionLoop:
	for {
		select {
		case res := <-d.tryCh:
			out.result = res.result
			out.execErr = res.err
			break actionLoop
		case <-d.runBudgetCh:
			// Per-run budget exhausted: cancel the active attempt, mark it a
			// run-budget timeout so the loop stops retrying and proceeds to the
			// bounded handoff, then drain the cancelled attempt's result.
			r.drainTimedOut(d, &out, true)
			break actionLoop
		case <-d.tryDeadline:
			// Per-try cap exhausted with run budget possibly remaining: cancel
			// the attempt and mark it a (non-run-budget) timeout; the loop may
			// start a fresh retry if budget and retries remain.
			r.drainTimedOut(d, &out, false)
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
		case press := <-d.actionCh:
			if !press.Confirmed {
				// First press only arms the action: surface a "press X again"
				// hint on the live status line so the operator sees it
				// registered and what a second press will do.
				d.mon.SetArmed(keyboard.ArmMessage(press.Action), keyboard.ConfirmWindow)
				continue
			}
			switch press.Action {
			case keyboard.ActionSkip:
				d.mon.SetActing(keyboard.ActMessage(press.Action))
				d.cancelAttempt()
				r.skipFlag.Store(true)
				out.actionTaken = true
				out.cancellationSource = CancellationSourceSkip
				r.drainOperatorCancellation(d, &out)
				break actionLoop
			case keyboard.ActionPause:
				d.mon.SetActing(keyboard.ActMessage(press.Action))
				d.cancelAttempt()
				out.actionTaken = true
				res := <-d.tryCh
				out.result = res.result
				out.execErr = res.err
				break actionLoop
			case keyboard.ActionStop:
				// Graceful stop (Ctrl+X): cancel the running try, drain the
				// result, set stopFlag so the relay halts after recording the
				// cancelled attempt. Unlike the old passive-wait behaviour,
				// the attempt is cancelled immediately so the operator sees a
				// clean OutcomeCancelled record rather than whatever the agent
				// happens to be doing when the relay eventually stops.
				r.stopFlag.Store(true)
				d.cancelAttempt()
				d.mon.SetStopping(true)
				out.actionTaken = true
				out.cancellationSource = CancellationSourceGracefulStop
				r.drainOperatorCancellation(d, &out)
				break actionLoop
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
				out.cancellationSource = CancellationSourceQuitNow
				r.drainOperatorCancellation(d, &out)
				break actionLoop
			}
		}
	}
	return out
}

// drainOperatorCancellation waits for the cancelled attempt's result while
// retaining the quit-now escalation path. If the operator escalates with
// Ctrl+C while a skip or graceful-stop cancellation is draining, the source is
// promoted to quit_now and the relay stop flag is set.
func (r *Runner) drainOperatorCancellation(d actionLoopDeps, out *actionLoopResult) {
drainLoop:
	for {
		select {
		case res := <-d.tryCh:
			out.result = res.result
			out.execErr = res.err
			break drainLoop
		case pid := <-d.pidCh:
			// A late OnStart can land here if the try hadn't reported its pid
			// yet; capture it so a quit escalation can target the right group.
			out.attemptPGID = pid
		case press := <-d.actionCh:
			// Only a confirmed (double-press) quit escalates the drain to an
			// immediate force-kill; a lone arming press is ignored here since the
			// drain is already cancelling.
			if press.Action != keyboard.ActionQuit || !press.Confirmed {
				continue
			}
			r.stopFlag.Store(true)
			if d.mon != nil {
				d.mon.SetStopping(true)
			}
			out.cancellationSource = CancellationSourceQuitNow
			// SIGKILL is idempotent (a re-signalled or already exited group
			// resolves to nil), so this cannot race the WaitDelay/Cmd.Cancel
			// escalation into an error.
			if out.attemptPGID > 0 {
				_ = r.forceKillGroup(out.attemptPGID)
			}
		}
	}
}

// benchResetDeadline derives the bench-window end from parsed reset evidence,
// preferring an absolute ResetAt, then a relative ResetAfter, and finally a
// conservative BenchDefaultDuration fallback when a usage_limit carried no
// parsed deadline.
func benchResetDeadline(ev *reliability.FailureEvidence, now time.Time) time.Time {
	if ev != nil {
		if ev.ResetAt != nil {
			return *ev.ResetAt
		}
		if ev.ResetAfter > 0 {
			return now.Add(ev.ResetAfter)
		}
	}
	return now.Add(BenchDefaultDuration)
}

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
		maxAttempts = HourlyRetryMaxAttempts
	}
	if isProbation {
		maxAttempts = HourlyRetryMaxAttempts
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

// noHandoffResumeReason explains, for a budget-cancelled implementation try that
// could not start a bounded handoff-only continuation, which resume precondition
// was missing. The reason is persisted on the resolving handoff_timeout try so
// recovery routing and operator triage can tell a no-resume harness from a
// no-session attempt (task 4.3).
func noHandoffResumeReason(exec agent.Executor, sessionID string) string {
	if exec == nil || !exec.ResumeSupported() {
		return "run timeout; harness cannot resume for handoff"
	}
	if sessionID == "" {
		return "run timeout; no session captured for handoff"
	}
	return "run timeout"
}

func buildHandoffOnlyPrompt(opts agent.RunOptions) string {
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
	picked agent.ResolvedAgent,
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
) (reliability.TryOutcome, *agent.TryResult, bool, bool, error) {
	startedAt := time.Now().UTC()

	// Persist the captured session id so the resume-capable harness re-attaches to
	// the same conversation (mirrors the in-loop retry resume wiring).
	if rs, err := progress.LoadRunState(r.cfg.WorkspaceDir); err == nil && rs != nil {
		rs.SessionID = sessionID
		_ = progress.SaveRunState(r.cfg.WorkspaceDir, rs)
	}

	opts := agent.RunOptions{
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
		result   *agent.TryResult
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
		result = &agent.TryResult{}
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

func lastOutputAge(path string, at time.Time) (time.Duration, bool) {
	if path == "" || at.IsZero() {
		return 0, false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return 0, false
	}
	age := at.Sub(info.ModTime())
	if age < 0 {
		age = 0
	}
	return age, true
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
		return runTask{}, fmt.Errorf("claim head lap: %w", err)
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

func hasDirtyChangesSince(before, after map[string]string) bool {
	for path, afterXY := range after {
		beforeXY, existed := before[path]
		if !existed || beforeXY != afterXY {
			return true
		}
	}
	return false
}

func handoffCreatedLapIDs(handoff *progress.HandoffEntry) []string {
	if handoff == nil || len(handoff.CreatedLapIDs) == 0 {
		return nil
	}
	return append([]string(nil), handoff.CreatedLapIDs...)
}

func recoveryClassificationForRun(task runTask, entry *progress.RunEntry) string {
	if entry == nil || !strings.EqualFold(strings.TrimSpace(task.promptAssignee()), store.RecoveryRouteName) {
		return ""
	}
	value := strings.TrimSpace(entry.Classification)
	switch value {
	case "continue", "discard", "course_correct", "repair_plan", "needs_user":
		return value
	default:
		return ""
	}
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
		out = append(out, progressRunEntryLapIDs(entry)...)
	}
	return out
}

func progressRunEntryLapIDs(entry progress.RunEntry) []string {
	var out []string
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
	return out
}

func pinnedLapCompleteElsewhere(workspaceDir, runID, lapID string, recordedLaps []string) bool {
	if strings.TrimSpace(lapID) == "" {
		return false
	}
	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err == nil {
		for _, entry := range entries {
			if entry.RunID == runID {
				continue
			}
			if stringSliceContains(progressRunEntryLapIDs(entry), lapID) {
				return true
			}
		}
	}
	if stringSliceContains(recordedLaps, lapID) {
		return false
	}
	done, known := lapDoneInLapsState(workspaceDir, lapID)
	return known && done
}

func lapDoneInLapsState(workspaceDir, lapID string) (bool, bool) {
	data, err := os.ReadFile(filepath.Join(workspaceDir, ".laps", "laps.json"))
	if err != nil {
		return false, false
	}
	var state struct {
		Tasks []struct {
			ID          string  `json:"id"`
			IsDone      bool    `json:"isDone"`
			CompletedAt *string `json:"completedAt"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return false, false
	}
	for _, task := range state.Tasks {
		if task.ID != lapID {
			continue
		}
		if task.IsDone {
			return true, true
		}
		if task.CompletedAt != nil && strings.TrimSpace(*task.CompletedAt) != "" {
			return true, true
		}
		return false, true
	}
	return false, false
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func recordedHandoffEntryForRun(workspaceDir, runID string, firstNewEntry int) *progress.HandoffEntry {
	return handoffEntryFromRunEntry(recordedRunEntryForRun(workspaceDir, runID, firstNewEntry))
}

func handoffEntryFromRunEntry(entry *progress.RunEntry) *progress.HandoffEntry {
	if entry == nil {
		return nil
	}
	return entry.Handoff
}

func recordedRunEntryForRun(workspaceDir, runID string, firstNewEntry int) *progress.RunEntry {
	if runID == "" {
		return nil
	}
	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		return nil
	}
	if firstNewEntry < 0 {
		firstNewEntry = 0
	}
	for i := len(entries) - 1; i >= firstNewEntry; i-- {
		entry := entries[i]
		if entry.RunID == runID {
			return &entry
		}
	}
	return nil
}

func tryOutcomeForAttempt(failed, incomplete, interrupted, hasDurableHandoff bool) reliability.TryOutcome {
	if interrupted {
		return reliability.OutcomeInterrupted
	}
	if !failed {
		if hasDurableHandoff {
			return reliability.OutcomeHandoffRequested
		}
		return reliability.OutcomeCompleted
	}
	if incomplete {
		return reliability.OutcomeIncomplete
	}
	return reliability.OutcomeFailed
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

// firstNonEmpty returns the first argument whose trimmed value is non-empty,
// or "" when none qualify. It resolves the model used in the runner telemetry
// tag: the executor's ResolvedModel (authoritative for bare-alias routes) wins
// over the route-resolved picked.Model, but the route-resolved model remains
// the fallback when the executor did not populate ResolvedModel.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func resolvedRunnerModel(result *agent.TryResult, picked agent.ResolvedAgent) string {
	if result == nil {
		return firstNonEmpty(picked.Model)
	}
	return firstNonEmpty(result.ResolvedModel, picked.Model)
}
