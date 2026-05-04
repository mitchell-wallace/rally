package keyboard

import (
	"bytes"
	"context"
	"io"
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

func TestSinglePressShowsConfirmation(t *testing.T) {
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
	time.Sleep(20 * time.Millisecond)

	msg := kb.LastConfirmationMessage()
	if msg != "Press Ctrl+C again to exit\n" {
		t.Fatalf("expected confirmation message, got %q", msg)
	}

	_ = ch
}

func TestDoublePressEmitsAction(t *testing.T) {
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
	time.Sleep(20 * time.Millisecond)
	pi.send(ctrlS)

	select {
	case a := <-ch:
		if a != ActionSkip {
			t.Fatalf("expected ActionSkip, got %v", a)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for action")
	}
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
	time.Sleep(80 * time.Millisecond) // wait for timeout
	pi.send(ctrlP)                    // new first press after timeout
	time.Sleep(20 * time.Millisecond)
	pi.send(ctrlP) // second press within window -> should emit

	select {
	case a := <-ch:
		if a != ActionPause {
			t.Fatalf("expected ActionPause after reset, got %v", a)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for action")
	}
}

func TestDifferentShortcutsDontInterfere(t *testing.T) {
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
	time.Sleep(20 * time.Millisecond)
	pi.send(ctrlX)

	// Should NOT emit anything because second press is different shortcut
	select {
	case a := <-ch:
		t.Fatalf("unexpected action %v", a)
	case <-time.After(150 * time.Millisecond):
		// expected
	}
}

func TestAllShortcuts(t *testing.T) {
	tests := []struct {
		key    byte
		want   Action
		msg    string
	}{
		{ctrlC, ActionQuit, "Press Ctrl+C again to exit\n"},
		{ctrlS, ActionSkip, "Press Ctrl+S again to skip\n"},
		{ctrlP, ActionPause, "Press Ctrl+P again to pause\n"},
		{ctrlX, ActionStop, "Press Ctrl+X again to stop\n"},
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
		time.Sleep(20 * time.Millisecond)
		pi.send(tc.key)

		select {
		case a := <-ch:
			if a != tc.want {
				t.Fatalf("key 0x%02x: expected %v, got %v", tc.key, tc.want, a)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("key 0x%02x: timed out waiting for action", tc.key)
		}

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
	case a := <-ch:
		t.Fatalf("unexpected action for normal key: %v", a)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}
