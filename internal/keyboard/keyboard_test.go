package keyboard

import (
	"bytes"
	"context"
	"io"
	"os"
	"runtime"
	"testing"
	"time"
)

// pipeInput wraps an io.PipeWriter so tests can send bytes.
type pipeInput struct {
	w *io.PipeWriter
}

func newPipeInput() (*io.PipeReader, *pipeInput) {
	r, w := io.Pipe()
	return r, &pipeInput{w: w}
}

func (p *pipeInput) send(b byte) {
	p.w.Write([]byte{b})
}

func TestSinglePressArmsButDoesNotConfirm(t *testing.T) {
	pr, pi := newPipeInput()
	defer pi.w.Close()
	out := &bytes.Buffer{}
	kb := NewKeyboard(pr, out)
	kb.SetCloseOnStop(true)
	kb.SetConfirmWindow(100 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := kb.Start(ctx)
	defer kb.Stop()

	pi.send(ctrlC)

	// First press must emit an arming (unconfirmed) event and must not write to
	// out — feedback is rendered by consumers, not the keyboard.
	select {
	case p := <-ch:
		if p.Action != ActionQuit {
			t.Fatalf("armed action = %v, want ActionQuit", p.Action)
		}
		if p.Confirmed {
			t.Fatal("first press must not be confirmed")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for arming press")
	}
	if out.Len() != 0 {
		t.Fatalf("expected keyboard to write nothing, got %q", out.String())
	}
}

func TestDoublePressConfirmsAction(t *testing.T) {
	pr, pi := newPipeInput()
	defer pi.w.Close()
	out := &bytes.Buffer{}
	kb := NewKeyboard(pr, out)
	kb.SetCloseOnStop(true)
	kb.SetConfirmWindow(100 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := kb.Start(ctx)
	defer kb.Stop()

	pi.send(ctrlS)
	mustPress(t, ch, ActionSkip, false)
	pi.send(ctrlS)
	mustPress(t, ch, ActionSkip, true)
}

func TestTimeoutResetsState(t *testing.T) {
	pr, pi := newPipeInput()
	defer pi.w.Close()
	out := &bytes.Buffer{}
	kb := NewKeyboard(pr, out)
	kb.SetCloseOnStop(true)
	kb.SetConfirmWindow(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := kb.Start(ctx)
	defer kb.Stop()

	pi.send(ctrlP)
	mustPress(t, ch, ActionPause, false)
	time.Sleep(80 * time.Millisecond) // wait for window to lapse
	pi.send(ctrlP)                    // new first press after timeout
	mustPress(t, ch, ActionPause, false)
	pi.send(ctrlP) // second press within window -> confirms
	mustPress(t, ch, ActionPause, true)
}

func TestDifferentShortcutsDontConfirm(t *testing.T) {
	pr, pi := newPipeInput()
	defer pi.w.Close()
	out := &bytes.Buffer{}
	kb := NewKeyboard(pr, out)
	kb.SetCloseOnStop(true)
	kb.SetConfirmWindow(100 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := kb.Start(ctx)
	defer kb.Stop()

	pi.send(ctrlC)
	mustPress(t, ch, ActionQuit, false)
	pi.send(ctrlX)
	// The different second shortcut arms its own action; it must not confirm.
	mustPress(t, ch, ActionStop, false)
}

func TestAllShortcuts(t *testing.T) {
	tests := []struct {
		key  byte
		want Action
	}{
		{ctrlC, ActionQuit},
		{ctrlS, ActionSkip},
		{ctrlP, ActionPause},
		{ctrlX, ActionStop},
	}

	for _, tc := range tests {
		pr, pi := newPipeInput()
		defer pi.w.Close()
		out := &bytes.Buffer{}
		kb := NewKeyboard(pr, out)
		kb.SetCloseOnStop(true)
		kb.SetConfirmWindow(100 * time.Millisecond)

		ctx, cancel := context.WithCancel(context.Background())
		ch := kb.Start(ctx)

		pi.send(tc.key)
		mustPress(t, ch, tc.want, false)
		pi.send(tc.key)
		mustPress(t, ch, tc.want, true)

		cancel()
		kb.Stop()
	}
}

func TestIgnoredBytes(t *testing.T) {
	pr, pi := newPipeInput()
	defer pi.w.Close()
	out := &bytes.Buffer{}
	kb := NewKeyboard(pr, out)
	kb.SetCloseOnStop(true)
	kb.SetConfirmWindow(100 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := kb.Start(ctx)
	defer kb.Stop()

	pi.send('a')
	pi.send('b')
	pi.send('\n')

	select {
	case p := <-ch:
		t.Fatalf("unexpected event for normal key: %v", p)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestArmMessage(t *testing.T) {
	cases := map[Action]string{
		ActionQuit:  "press Ctrl+C again to quit now",
		ActionSkip:  "press Ctrl+S again to skip",
		ActionPause: "press Ctrl+P again to pause",
		ActionStop:  "press Ctrl+X again to graceful-stop",
		ActionNone:  "",
	}
	for a, want := range cases {
		if got := ArmMessage(a); got != want {
			t.Errorf("ArmMessage(%v) = %q, want %q", a, got, want)
		}
	}
}

// TestNoReaderLeakAcrossCycles guards the root cause of flaky shortcuts: before
// the cancelable-reader fix, every Start/Stop cycle leaked a goroutine parked on
// Read that later stole a keystroke, so shortcuts fired only "sometimes" as the
// leaks piled up across a relay. A real OS pipe is used (an *os.File, so the
// real epoll/kqueue/select cancelreader runs rather than the test fallback) and
// goroutines must return to baseline after many no-input cycles.
func TestNoReaderLeakAcrossCycles(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer w.Close()
	defer r.Close()

	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	base := runtime.NumGoroutine()

	for i := 0; i < 40; i++ {
		kb := NewKeyboard(r, io.Discard)
		ctx, cancel := context.WithCancel(context.Background())
		_ = kb.Start(ctx)
		// No bytes are sent, so the read is parked; Stop must unblock it without
		// closeOnStop and without leaving the goroutine behind.
		_ = kb.Stop()
		cancel()
	}

	deadline := time.Now().Add(3 * time.Second)
	var n int
	for time.Now().Before(deadline) {
		runtime.GC()
		n = runtime.NumGoroutine()
		if n <= base+3 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: baseline %d, after 40 cycles %d (want ~baseline)", base, n)
}

// mustPress asserts the next event on ch matches the expected action and
// confirmation state within a short timeout.
func mustPress(t *testing.T, ch <-chan Press, want Action, confirmed bool) {
	t.Helper()
	select {
	case p := <-ch:
		if p.Action != want || p.Confirmed != confirmed {
			t.Fatalf("press = %+v, want {Action:%v Confirmed:%v}", p, want, confirmed)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("timed out waiting for press {Action:%v Confirmed:%v}", want, confirmed)
	}
}
