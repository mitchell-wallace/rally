package tuisafe

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

func TestControlsArmConfirmWithinWindow(t *testing.T) {
	c := newControls()
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	ch, err := c.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	c.press(runtimeevent.OperatorActionSkip)
	assertPress(t, ch, runtimeevent.OperatorActionSkip, false)
	now = now.Add(runtimeevent.ConfirmWindow - time.Millisecond)
	c.press(runtimeevent.OperatorActionSkip)
	assertPress(t, ch, runtimeevent.OperatorActionSkip, true)
}

func TestControlsDifferentActionResetsArm(t *testing.T) {
	c := newControls()
	ch, err := c.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	c.press(runtimeevent.OperatorActionSkip)
	assertPress(t, ch, runtimeevent.OperatorActionSkip, false)
	c.press(runtimeevent.OperatorActionPause)
	assertPress(t, ch, runtimeevent.OperatorActionPause, false)
	c.press(runtimeevent.OperatorActionPause)
	assertPress(t, ch, runtimeevent.OperatorActionPause, true)
}

func TestControlsConfirmWindowExpiry(t *testing.T) {
	c := newControls()
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	ch, err := c.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	c.press(runtimeevent.OperatorActionStop)
	assertPress(t, ch, runtimeevent.OperatorActionStop, false)
	now = now.Add(runtimeevent.ConfirmWindow + time.Millisecond)
	c.press(runtimeevent.OperatorActionStop)
	assertPress(t, ch, runtimeevent.OperatorActionStop, false)
}

func TestControlsStartStopLifecycle(t *testing.T) {
	c := newControls()
	ch, err := c.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Start(context.Background()); err == nil {
		t.Fatal("expected second Start to fail")
	}
	c.Stop()
	if _, ok := <-ch; ok {
		t.Fatal("expected Stop to close press channel")
	}
	ch, err = c.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.press(runtimeevent.OperatorActionQuit)
	assertPress(t, ch, runtimeevent.OperatorActionQuit, false)
	c.Stop()
}

func TestControlsStaleContextDoesNotStopLaterSession(t *testing.T) {
	c := newControls()
	ctx1, cancel1 := context.WithCancel(context.Background())
	if _, err := c.Start(ctx1); err != nil {
		t.Fatal(err)
	}
	c.Stop()

	ch2, err := c.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	cancel1()
	time.Sleep(20 * time.Millisecond)

	c.press(runtimeevent.OperatorActionSkip)
	assertPress(t, ch2, runtimeevent.OperatorActionSkip, false)
}

func TestControlsWaitResumeEnter(t *testing.T) {
	c := newControls()
	done := make(chan error, 1)
	go func() {
		done <- c.WaitResume(context.Background())
	}()
	waitResumeRegistered(t, c)
	c.resume()
	if err := <-done; err != nil {
		t.Fatalf("WaitResume returned error: %v", err)
	}
}

func TestControlsWaitResumeContextCancel(t *testing.T) {
	c := newControls()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- c.WaitResume(ctx)
	}()
	waitResumeRegistered(t, c)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitResume err = %v, want context.Canceled", err)
	}
}

func assertPress(t *testing.T, ch <-chan runtimeevent.Press, action runtimeevent.OperatorAction, confirmed bool) {
	t.Helper()
	select {
	case press := <-ch:
		if press.Action != action || press.Confirmed != confirmed {
			t.Fatalf("press = %+v, want action %v confirmed %v", press, action, confirmed)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for press")
	}
}

func waitResumeRegistered(t *testing.T, c *controls) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		c.mu.Lock()
		ready := c.resumeCh != nil
		c.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for WaitResume registration")
		case <-ticker.C:
		}
	}
}
