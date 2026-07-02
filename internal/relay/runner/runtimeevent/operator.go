package runtimeevent

import (
	"context"
	"time"
)

// This file defines the operator-control half of the runtime-event boundary
// (design Decision 4): the action vocabulary, the double-press confirmation
// window, the message helpers, the Press type, and the ControlSource interface.
//
// ArmMessage, ActMessage, and ConfirmWindow are copied verbatim from
// internal/keyboard so that operator-facing text and timing are bit-identical
// before and after the runner stops importing keyboard directly. This is not a
// place to reword; parity is held by reproduction.

// OperatorAction enumerates the operator-facing control actions a
// [ControlSource] can deliver. The value order mirrors internal/keyboard's
// Action 1:1 (None, Quit, Skip, Pause, Stop) so ArmMessage/ActMessage produce
// identical text. OperatorActionNone is the zero value and is never carried by a
// [Press]: a control source that decodes a non-shortcut byte drops it rather
// than emitting OperatorActionNone.
type OperatorAction int

const (
	// OperatorActionNone is the zero value; it is never delivered in a Press.
	OperatorActionNone OperatorAction = iota
	OperatorActionQuit
	OperatorActionSkip
	OperatorActionPause
	OperatorActionStop
)

// ConfirmWindow is the span within which the second press of a double-press
// shortcut must arrive to fire. It is the verbatim copy of
// internal/keyboard.ConfirmWindow: four seconds. The runtime-event contract
// owns the canonical value so the terminal adapter and any alternate
// presentation agree with today's keyboard semantics. Do not reword the
// duration.
const ConfirmWindow = 4 * time.Second

// Press is a decoded operator-control event delivered to the runner. The first
// press of a double-press shortcut arrives with Confirmed false (it arms the
// action); the second press within [ConfirmWindow] arrives with Confirmed true
// and is the one the runner acts on. Action is never OperatorActionNone.
type Press struct {
	Action    OperatorAction
	Confirmed bool
}

// ArmMessage returns the "press X again to <verb>" hint shown after the first
// press of a double-press shortcut, so the operator always sees that the press
// registered and what a second press will do. The text is copied verbatim from
// internal/keyboard.ArmMessage; this is not a place to reword.
func ArmMessage(a OperatorAction) string {
	switch a {
	case OperatorActionQuit:
		return "press Ctrl+C again to quit now"
	case OperatorActionSkip:
		return "press Ctrl+S again to skip"
	case OperatorActionPause:
		return "press Ctrl+P again to pause"
	case OperatorActionStop:
		return "press Ctrl+X again to graceful-stop"
	default:
		return ""
	}
}

// ActMessage returns the present-progressive echo shown when a double-press
// shortcut fires, so the operator sees exactly which action is being taken. The
// text is copied verbatim from internal/keyboard.ActMessage.
func ActMessage(a OperatorAction) string {
	switch a {
	case OperatorActionQuit:
		return "quitting…"
	case OperatorActionSkip:
		return "skipping…"
	case OperatorActionPause:
		return "pausing…"
	case OperatorActionStop:
		return "stopping…"
	default:
		return ""
	}
}

// ControlSource is the operator-input contract the runner consumes. It models
// per-phase input sessions: today the runner constructs one keyboard per phase
// (the wait countdown and the active try), each with its own raw-mode
// enter/leave cycle. Routing input through ControlSource keeps raw-mode state
// and reader cleanup inside the implementation rather than the runner.
//
// Implementations must be safe for sequential Start/Stop sessions (the runner
// opens and closes one session per phase) and must not leak a blocked reader
// across Stop, mirroring the no-leaked-reader property today's keyboard pins.
type ControlSource interface {
	// Start begins a control session, returning a channel of Press events. The
	// session owns its terminal/raw-mode state until Stop is called. An unconfirmed
	// Press arms an action; a confirmed Press (second press within ConfirmWindow)
	// is the one the runner acts on.
	Start(ctx context.Context) (<-chan Press, error)

	// Stop ends the current session, restoring terminal state and releasing any
	// blocked reader. It is safe to call Start again after Stop returns; the next
	// session is independent.
	Stop()

	// WaitResume blocks until the operator resumes (the pause path). Today this
	// performs a blocking read of a newline after leaving raw mode. It returns
	// ctx.Err() if the context is cancelled before the operator resumes.
	WaitResume(ctx context.Context) error
}
