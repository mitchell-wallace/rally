package runtimeevent_test

import (
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/keyboard"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

// These tests are the guardrail that keeps the operator-action vocabulary moved
// into runtimeevent byte-identical to its source in internal/keyboard. They call
// keyboard's real ArmMessage/ActMessage/ConfirmWindow and compare against
// runtimeevent's copies — they never retype the literal strings, since a retyped
// copy would only prove the two copies agree, not that the move was faithful.

// actionPair ties a runtimeevent OperatorAction to the keyboard Action it must
// reproduce. The pairs are ordered to mirror keyboard's iota declaration so the
// ordering-parity check below also walks them in source order.
var actionPairs = []struct {
	runtime runtimeevent.OperatorAction
	kb      keyboard.Action
}{
	{runtimeevent.OperatorActionNone, keyboard.ActionNone},
	{runtimeevent.OperatorActionQuit, keyboard.ActionQuit},
	{runtimeevent.OperatorActionSkip, keyboard.ActionSkip},
	{runtimeevent.OperatorActionPause, keyboard.ActionPause},
	{runtimeevent.OperatorActionStop, keyboard.ActionStop},
}

// TestOperatorActionMirrorsKeyboardOrder asserts the two enumerations have the
// same underlying numeric values in the same order. runtimeevent's doc promises
// the values mirror keyboard's Action 1:1; if that ever drifts, the int-based
// mapping used elsewhere would silently mis-translate presses.
func TestOperatorActionMirrorsKeyboardOrder(t *testing.T) {
	for _, p := range actionPairs {
		if int(p.runtime) != int(p.kb) {
			t.Errorf("numeric value mismatch: %v = %d, %v = %d",
				p.runtime, int(p.runtime), p.kb, int(p.kb))
		}
	}
}

// TestArmMessageParity asserts runtimeevent.ArmMessage reproduces
// keyboard.ArmMessage byte-for-byte for every action (including the empty
// default for OperatorActionNone). Byte-equality is checked via string equality
// plus a length cross-check so a trailing-byte or encoding drift is unambiguous.
func TestArmMessageParity(t *testing.T) {
	for _, p := range actionPairs {
		want := keyboard.ArmMessage(p.kb)
		got := runtimeevent.ArmMessage(p.runtime)
		if got != want {
			t.Errorf("ArmMessage(%v): runtimeevent %q (len %d) != keyboard %q (len %d)",
				p.runtime, got, len(got), want, len(want))
			continue
		}
		if len(got) != len(want) {
			t.Errorf("ArmMessage(%v): length drift %d != %d", p.runtime, len(got), len(want))
		}
	}
}

// TestActMessageParity asserts runtimeevent.ActMessage reproduces
// keyboard.ActMessage byte-for-byte for every action.
func TestActMessageParity(t *testing.T) {
	for _, p := range actionPairs {
		want := keyboard.ActMessage(p.kb)
		got := runtimeevent.ActMessage(p.runtime)
		if got != want {
			t.Errorf("ActMessage(%v): runtimeevent %q (len %d) != keyboard %q (len %d)",
				p.runtime, got, len(got), want, len(want))
			continue
		}
		if len(got) != len(want) {
			t.Errorf("ActMessage(%v): length drift %d != %d", p.runtime, len(got), len(want))
		}
	}
}

// TestConfirmWindowParity asserts the double-press confirmation window in
// runtimeevent is bit-identical to keyboard's (today, four seconds). The
// constant is the canonical value the terminal adapter and any alternate
// presentation must agree on, so a divergence here would silently change
// shortcut timing for one surface but not the other.
func TestConfirmWindowParity(t *testing.T) {
	if runtimeevent.ConfirmWindow != keyboard.ConfirmWindow {
		t.Errorf("ConfirmWindow: runtimeevent %v != keyboard %v",
			runtimeevent.ConfirmWindow, keyboard.ConfirmWindow)
	}
	const want = 4 * time.Second
	if runtimeevent.ConfirmWindow != want {
		t.Errorf("ConfirmWindow = %v, want %v", runtimeevent.ConfirmWindow, want)
	}
	if keyboard.ConfirmWindow != want {
		t.Errorf("keyboard.ConfirmWindow = %v, want %v", keyboard.ConfirmWindow, want)
	}
}

// TestArmMessageCoversAllShortcuts is a non-parity guard: it documents that the
// four real shortcuts each carry a non-empty arm hint, and that the zero value
// (OperatorActionNone, which is never delivered in a Press) yields the empty
// hint. This catches a future wiring change that drops a case from the switch.
func TestArmMessageCoversAllShortcuts(t *testing.T) {
	armed := []runtimeevent.OperatorAction{
		runtimeevent.OperatorActionQuit,
		runtimeevent.OperatorActionSkip,
		runtimeevent.OperatorActionPause,
		runtimeevent.OperatorActionStop,
	}
	for _, a := range armed {
		if runtimeevent.ArmMessage(a) == "" {
			t.Errorf("ArmMessage(%v) unexpectedly empty", a)
		}
		if runtimeevent.ActMessage(a) == "" {
			t.Errorf("ActMessage(%v) unexpectedly empty", a)
		}
	}
	if got := runtimeevent.ArmMessage(runtimeevent.OperatorActionNone); got != "" {
		t.Errorf("ArmMessage(OperatorActionNone) = %q, want empty", got)
	}
	if got := runtimeevent.ActMessage(runtimeevent.OperatorActionNone); got != "" {
		t.Errorf("ActMessage(OperatorActionNone) = %q, want empty", got)
	}
}
