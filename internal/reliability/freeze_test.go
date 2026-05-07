package reliability

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestDetectorLinuxRequiresAllSignals(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d := NewDetectorForPlatform("linux", 3*time.Minute)

	if frozen := d.Evaluate(SignalSnapshot{
		Now:          base,
		LogSilentFor: 4 * time.Minute,
		Connections:  0,
		IOBytes:      10,
	}); frozen {
		t.Fatal("first observation should not freeze without an IO-silence window")
	}

	if frozen := d.Evaluate(SignalSnapshot{
		Now:          base.Add(4 * time.Minute),
		LogSilentFor: 4 * time.Minute,
		Connections:  1,
		IOBytes:      10,
	}); frozen {
		t.Fatal("nonzero connections should prevent freeze")
	}

	if frozen := d.Evaluate(SignalSnapshot{
		Now:          base.Add(4 * time.Minute),
		LogSilentFor: 4 * time.Minute,
		Connections:  0,
		IOBytes:      10,
	}); !frozen {
		t.Fatal("expected freeze once log silence, zero connections, and IO silence all exceed threshold")
	}

	d = NewDetectorForPlatform("linux", 3*time.Minute)
	_ = d.Evaluate(SignalSnapshot{
		Now:          base,
		LogSilentFor: 4 * time.Minute,
		Connections:  0,
		IOBytes:      10,
	})
	if frozen := d.Evaluate(SignalSnapshot{
		Now:          base.Add(4 * time.Minute),
		LogSilentFor: 4 * time.Minute,
		Connections:  0,
		IOBytes:      11,
	}); frozen {
		t.Fatal("fresh IO activity should prevent freeze")
	}
}

func TestDetectorMacOSUsesLogSilenceOnly(t *testing.T) {
	d := NewDetectorForPlatform("darwin", 90*time.Second)
	frozen := d.Evaluate(SignalSnapshot{
		Now:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		LogSilentFor: 2 * time.Minute,
		Connections:  3,
		IOBytes:      99,
	})
	if !frozen {
		t.Fatal("expected macOS freeze detection to use log silence alone")
	}
}

func TestDetectorWindowsDisabled(t *testing.T) {
	d := NewDetectorForPlatform("windows", 30*time.Second)
	frozen := d.Evaluate(SignalSnapshot{
		Now:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		LogSilentFor: 10 * time.Minute,
		Connections:  0,
		IOBytes:      0,
	})
	if frozen {
		t.Fatal("windows freeze detection should be disabled")
	}
}

func TestProcessGroupKillerGracefulDrain(t *testing.T) {
	var signals []processSignal
	var sleeps []time.Duration
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	runningChecks := 0

	killer := processGroupKiller{
		drain: 5 * time.Second,
		poll:  time.Second,
		now:   func() time.Time { return now },
		sleep: func(d time.Duration) {
			sleeps = append(sleeps, d)
			now = now.Add(d)
		},
		send: func(_ int, sig processSignal) error {
			signals = append(signals, sig)
			return nil
		},
		isRunning: func(int) (bool, error) {
			runningChecks++
			return runningChecks < 3, nil
		},
	}

	if err := killer.Kill(context.Background(), 42); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}

	if !reflect.DeepEqual(signals, []processSignal{signalTerminate}) {
		t.Fatalf("signals = %v, want [%v]", signals, signalTerminate)
	}
	if got, want := sleeps, []time.Duration{time.Second, time.Second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sleeps = %v, want %v", got, want)
	}
}

func TestProcessGroupKillerEscalatesAfterDrain(t *testing.T) {
	var signals []processSignal
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base

	killer := processGroupKiller{
		drain: 5 * time.Second,
		poll:  time.Second,
		now:   func() time.Time { return now },
		sleep: func(d time.Duration) {
			now = now.Add(d)
		},
		send: func(_ int, sig processSignal) error {
			signals = append(signals, sig)
			return nil
		},
		isRunning: func(int) (bool, error) {
			return true, nil
		},
	}

	if err := killer.Kill(context.Background(), 42); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}

	if got, want := signals, []processSignal{signalTerminate, signalKill}; !reflect.DeepEqual(got, want) {
		t.Fatalf("signals = %v, want %v", got, want)
	}
}
