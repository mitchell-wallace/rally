package terminal

import (
	"bytes"
	"context"
	"io"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

const (
	testCtrlS = 0x13
	testCtrlX = 0x18
)

func TestControlsTranslateKeyboardPresses(t *testing.T) {
	r, w := mustPipe(t)
	defer w.Close()
	defer r.Close()

	controls := NewControls(r, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := controls.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer controls.Stop()

	writeByte(t, w, testCtrlS)
	mustControlPress(t, ch, runtimeevent.OperatorActionSkip, false)
	writeByte(t, w, testCtrlS)
	mustControlPress(t, ch, runtimeevent.OperatorActionSkip, true)
}

// TestControlsOperatorActionAllShortcuts drives every branch of operatorAction,
// not just Skip/Stop. A swapped case here (e.g. ActionQuit → OperatorActionStop)
// would otherwise slip past operator_parity_test, which pins the enum mapping,
// not this adapter translation function.
func TestControlsOperatorActionAllShortcuts(t *testing.T) {
	tests := []struct {
		name string
		byte byte
		want runtimeevent.OperatorAction
	}{
		{"quit", 0x03, runtimeevent.OperatorActionQuit},
		{"skip", 0x13, runtimeevent.OperatorActionSkip},
		{"pause", 0x10, runtimeevent.OperatorActionPause},
		{"stop", 0x18, runtimeevent.OperatorActionStop},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, w := mustPipe(t)
			defer w.Close()
			defer r.Close()
			controls := NewControls(r, io.Discard)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ch, err := controls.Start(ctx)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer controls.Stop()

			writeByte(t, w, tt.byte)
			mustControlPress(t, ch, tt.want, false)
		})
	}
}

func TestControlsStartStopCyclesDoNotLeakReaders(t *testing.T) {
	r, w := mustPipe(t)
	defer w.Close()
	defer r.Close()

	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	base := runtime.NumGoroutine()

	controls := NewControls(r, io.Discard)
	for i := 0; i < 40; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		if _, err := controls.Start(ctx); err != nil {
			t.Fatalf("Start cycle %d: %v", i, err)
		}
		controls.Stop()
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

func TestControlsWaitResumeStopsSessionThenReadsEnter(t *testing.T) {
	r, w := mustPipe(t)
	defer w.Close()
	defer r.Close()

	controls := NewControls(r, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := controls.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- controls.WaitResume(ctx)
	}()

	waitControlsStopped(t, controls)
	writeByte(t, w, '\n')
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitResume: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for WaitResume")
	}

	next, err := controls.Start(ctx)
	if err != nil {
		t.Fatalf("restart after WaitResume: %v", err)
	}
	defer controls.Stop()

	writeByte(t, w, testCtrlX)
	mustControlPress(t, next, runtimeevent.OperatorActionStop, false)
}

func waitControlsStopped(t *testing.T, controls *Controls) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		controls.mu.Lock()
		stopped := controls.kb == nil
		controls.mu.Unlock()
		if stopped {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for controls to stop")
}

func mustPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	return r, w
}

func writeByte(t *testing.T, w io.Writer, b byte) {
	t.Helper()
	if _, err := w.Write([]byte{b}); err != nil {
		t.Fatalf("write %q: %v", b, err)
	}
}

func mustControlPress(t *testing.T, ch <-chan runtimeevent.Press, want runtimeevent.OperatorAction, confirmed bool) {
	t.Helper()
	select {
	case press := <-ch:
		if press.Action != want || press.Confirmed != confirmed {
			t.Fatalf("press = %+v, want {Action:%v Confirmed:%v}", press, want, confirmed)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("timed out waiting for press {Action:%v Confirmed:%v}", want, confirmed)
	}
}

func TestNewControlsDefaultsNilWriters(t *testing.T) {
	controls := NewControls(bytes.NewReader([]byte("\n")), nil)
	if err := controls.WaitResume(context.Background()); err != nil {
		t.Fatalf("WaitResume: %v", err)
	}
}
