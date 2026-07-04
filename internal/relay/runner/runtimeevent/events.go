package runtimeevent

import "time"

// This file defines the concrete data-only payload types for the [Event]
// vocabulary (design Decision 3). Every payload carries data — names,
// durations, counts, and message strings — and never ANSI escape sequences or
// lipgloss style values. A presentation adapter renders the bytes.
//
// Each payload implements [Event] via a value-receiver Kind method returning its
// own [Kind]. Consumers may dispatch on [Event.Kind] or via a Go type switch on
// the concrete type.

// --- Lifecycle markers (data-only; a terminal sink no-ops these) ------------

// RelayStarted is a data-only lifecycle marker emitted when a relay begins. It
// has no current operator-facing print; a terminal sink SHALL no-op it. It
// exists so an alternate presentation (e.g. a future TUI) can render a relay
// start. The literal "Relay complete." line stays owned by the app layer.
type RelayStarted struct {
	RelayID          int
	TargetIterations int
	AgentMix         string
}

// Kind implements [Event].
func (RelayStarted) Kind() Kind { return KindRelayStarted }

// RelayCompleted is a data-only lifecycle marker emitted when a relay ends. It
// has no current operator-facing print; a terminal sink SHALL no-op it. The
// rendered relay summary block is a separate event ([RelaySummaryReady]).
type RelayCompleted struct {
	RelayID       int
	TotalOutings  int
	Passed        int
	Failed        int
	Cancelled     int
	TotalDuration time.Duration
}

// Kind implements [Event].
func (RelayCompleted) Kind() Kind { return KindRelayCompleted }

// --- Warnings ----------------------------------------------------------------

// RouteWarning carries a route/selection warning written to stderr today. The
// message is the operator-facing warning text; a sink renders it (the route
// fallback line is mirrored to the relay log separately and is not an event).
type RouteWarning struct {
	Message string
}

// Kind implements [Event].
func (RouteWarning) Kind() Kind { return KindRouteWarning }

// TaskFileWarning carries a laps-instructions or free-run-prompt file warning
// written to stderr today. The message is the operator-facing warning text.
type TaskFileWarning struct {
	Message string
}

// Kind implements [Event].
func (TaskFileWarning) Kind() Kind { return KindTaskFileWarning }

// --- Outing header -----------------------------------------------------------

// OutingHeaderReady carries the data for an outing header (replaces style.RenderHeader
// today). The fields are the data-only mirror of the header options; a sink
// renders the separator/label block from them.
type OutingHeaderReady struct {
	OutingIndex  int
	TotalOutings int
	AgentName    string
	Attempt      int
	StartTime    time.Time
	IsLapsBacked bool
	LapTitle     string
	LapsStarted  int
	LapsTotal    int
	Model        string
	RoleLabel    string
}

// Kind implements [Event].
func (OutingHeaderReady) Kind() Kind { return KindOutingHeaderReady }

// --- Status / shortcut hint --------------------------------------------------

// TryStatusSnapshot carries the initial try status line (the first mon.Tick()
// status today). Status is the unstyled status text; a sink renders it with a
// line clear + newline.
type TryStatusSnapshot struct {
	Status string
}

// Kind implements [Event].
func (TryStatusSnapshot) Kind() Kind { return KindTryStatusSnapshot }

// ShortcutHintReady signals that the shortcut-hint line should be shown
// (replaces style.ShortcutHint() today). Width is the terminal width the hint
// should be sized for; zero means the sink derives the width itself (as the
// terminal sink does today).
type ShortcutHintReady struct {
	Width int
}

// Kind implements [Event].
func (ShortcutHintReady) Kind() Kind { return KindShortcutHintReady }

// --- Try footers (shared data) -----------------------------------------------

// FooterData carries the per-attempt outcome fields a sink needs to render a try
// footer. It is the data-only mirror of style.FooterOptions; runtimeevent must
// not import style (stdlib-only), so the fields are duplicated here by value
// rather than by reference. The four footer events below embed it and are
// distinguished by their [Kind].
type FooterData struct {
	Passed             bool
	Cancelled          bool
	Duration           time.Duration
	FilesChanged       int
	CommitHash         string
	CommitTitle        string
	FailReason         string
	CancellationSource string
	// Interim marks a within-budget retry: a sink renders the single neutral
	// (in-place redraw) line instead of the coloured terminal outcome block.
	Interim     bool
	Attempt     int
	MaxAttempts int
}

// RetryFooterUpdated carries the interim footer drawn during a within-budget
// retry (an in-place redraw today). Interim is true.
type RetryFooterUpdated struct {
	FooterData
}

// Kind implements [Event].
func (RetryFooterUpdated) Kind() Kind { return KindRetryFooterUpdated }

// AttemptFinished carries the terminal pass/fail footer for an attempt.
type AttemptFinished struct {
	FooterData
}

// Kind implements [Event].
func (AttemptFinished) Kind() Kind { return KindAttemptFinished }

// AttemptCancelled carries the cancelled footer for an attempt (Cancelled true).
type AttemptCancelled struct {
	FooterData
}

// Kind implements [Event].
func (AttemptCancelled) Kind() Kind { return KindAttemptCancelled }

// HandoffAttemptFinished carries the handoff-only footer for an attempt.
type HandoffAttemptFinished struct {
	FooterData
}

// Kind implements [Event].
func (HandoffAttemptFinished) Kind() Kind { return KindHandoffAttemptFinished }

// --- Rate limit --------------------------------------------------------------

// RateLimitWaitStarted carries the rate-limit cooldown notice (a dim "waiting
// <dur> for rate limit..." line today). Wait is the cooldown the runner is about
// to sleep; a sink renders the message from it.
type RateLimitWaitStarted struct {
	Wait time.Duration
}

// Kind implements [Event].
func (RateLimitWaitStarted) Kind() Kind { return KindRateLimitWaitStarted }

// --- Wait countdown ----------------------------------------------------------

// WaitStarted signals that a wait-countdown phase has begun and carries its
// first frame. Message is the rendered countdown line and Hint is the shortcut
// legend or armed-action hint text; both are unstyled (a sink applies styles).
type WaitStarted struct {
	Message   string
	Hint      string
	Total     time.Duration
	Remaining time.Duration
}

// Kind implements [Event].
func (WaitStarted) Kind() Kind { return KindWaitStarted }

// WaitTick carries a subsequent countdown frame redrawn on each tick. Message is
// the rendered countdown line and Hint is the shortcut legend or armed-action
// hint text; both are unstyled.
type WaitTick struct {
	Message   string
	Hint      string
	Remaining time.Duration
}

// Kind implements [Event].
func (WaitTick) Kind() Kind { return KindWaitTick }

// WaitFinished signals that a wait-countdown phase has ended and its frame
// should be cleared. It carries no data.
type WaitFinished struct{}

// Kind implements [Event].
func (WaitFinished) Kind() Kind { return KindWaitFinished }

// --- Operator-action feedback ------------------------------------------------

// OperatorActionArmed signals that an operator double-press shortcut has armed
// (first press) during a wait phase — the wait-loop "press X again" hint today.
// During an active try the arm feedback flows through the runner-driven monitor
// instead, so a terminal sink SHALL no-op this event for active tries; it exists
// as data for alternate presentations. Message is the armed-hint text.
type OperatorActionArmed struct {
	Action  OperatorAction
	Message string
}

// Kind implements [Event].
func (OperatorActionArmed) Kind() Kind { return KindOperatorActionArmed }

// OperatorActionApplied signals that an operator double-press shortcut has fired
// (confirmed press). A terminal sink SHALL no-op this event for active tries
// (the monitor shows the acting indicator); it exists as data for alternate
// presentations. Message is the present-progressive echo text.
type OperatorActionApplied struct {
	Action  OperatorAction
	Message string
}

// Kind implements [Event].
func (OperatorActionApplied) Kind() Kind { return KindOperatorActionApplied }

// --- Pause -------------------------------------------------------------------

// PausePromptShown carries the pause prompt ("Paused — press Enter to resume"
// today). Message is the prompt text; the blocking resume read is owned by the
// [ControlSource] (WaitResume), not by this event.
type PausePromptShown struct {
	Message string
}

// Kind implements [Event].
func (PausePromptShown) Kind() Kind { return KindPausePromptShown }

// --- Relay summary -----------------------------------------------------------

// RelaySummaryReady carries the data for the relay summary block (replaces
// style.RenderSummary today). A sink renders the separator/counts block from it.
type RelaySummaryReady struct {
	TotalOutings  int
	Passed        int
	Failed        int
	Cancelled     int
	TotalDuration time.Duration
}

// Kind implements [Event].
func (RelaySummaryReady) Kind() Kind { return KindRelaySummaryReady }
