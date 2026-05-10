package reliability

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestDetectorLinuxClassicFreeze(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d := NewDetectorForPlatform("linux", 3*time.Minute)

	// Log silent + no connections exceeding threshold → freeze.
	// IO activity does NOT prevent the classic freeze; background processes
	// (GC, opencode TUI) produce sporadic disk writes that would otherwise
	// cause the freeze to never fire.
	if frozen := d.Evaluate(SignalSnapshot{
		Now:          base,
		LogSilentFor: 4 * time.Minute,
		Connections:  0,
		IOBytes:      10,
	}); !frozen {
		t.Fatal("expected freeze: log silent >= threshold and no connections")
	}

	d = NewDetectorForPlatform("linux", 3*time.Minute)
	if frozen := d.Evaluate(SignalSnapshot{
		Now:          base,
		LogSilentFor: 4 * time.Minute,
		Connections:  1,
		IOBytes:      10,
	}); frozen {
		t.Fatal("nonzero connections should prevent classic freeze")
	}

	// Log silent below threshold → no freeze.
	d = NewDetectorForPlatform("linux", 3*time.Minute)
	if frozen := d.Evaluate(SignalSnapshot{
		Now:          base,
		LogSilentFor: 2 * time.Minute,
		Connections:  0,
		IOBytes:      10,
	}); frozen {
		t.Fatal("log silent below threshold should not freeze")
	}
}

func TestDetectorLinuxConnectedNoTrafficFreeze(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	threshold := 3 * time.Minute
	d := NewDetectorForPlatform("linux", threshold)

	// Connections open but no syscall I/O for NetworkSilentThreshold → freeze.
	// Simulates a rate-limited agent that keeps a TCP connection alive without
	// sending any data.
	d.Evaluate(SignalSnapshot{
		Now:          base,
		LogSilentFor: threshold + time.Second,
		Connections:  2,
		SyscallBytes: 1000,
	})
	// SyscallBytes unchanged for NetworkSilentThreshold → connectedFrozen
	if frozen := d.Evaluate(SignalSnapshot{
		Now:          base.Add(NetworkSilentThreshold),
		LogSilentFor: threshold + time.Second,
		Connections:  2,
		SyscallBytes: 1000, // no change
	}); !frozen {
		t.Fatal("expected freeze: connections open but no syscall I/O for NetworkSilentThreshold")
	}

	// Fresh syscall I/O resets the timer.
	d2 := NewDetectorForPlatform("linux", threshold)
	d2.Evaluate(SignalSnapshot{
		Now:          base,
		LogSilentFor: threshold + time.Second,
		Connections:  2,
		SyscallBytes: 1000,
	})
	if frozen := d2.Evaluate(SignalSnapshot{
		Now:          base.Add(NetworkSilentThreshold),
		LogSilentFor: threshold + time.Second,
		Connections:  2,
		SyscallBytes: 2000, // changed
	}); frozen {
		t.Fatal("fresh syscall I/O should prevent connected-frozen")
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

func TestDetectorLinuxFlagsAmbiguousProbeWindow(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d := NewDetectorForPlatform("linux", 3*time.Minute)

	_ = d.Assess(SignalSnapshot{
		Now:          base,
		LogSilentFor: 5 * time.Second,
		Connections:  1,
		IOBytes:      10,
	})

	assessment := d.Assess(SignalSnapshot{
		Now:          base.Add(61 * time.Second),
		LogSilentFor: 5 * time.Second,
		Connections:  1,
		IOBytes:      10,
	})
	if !assessment.Ambiguous {
		t.Fatal("expected probe ambiguity once log activity continues but IO stays stalled for 60s")
	}
	if assessment.Frozen {
		t.Fatal("ambiguous state should not trip passive freeze detection")
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

func TestFreezeControllerProbeSuccessClearsAmbiguousFreeze(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	probeCalls := 0
	killerCalls := 0
	snapshots := []SignalSnapshot{
		{Now: base, LogSilentFor: 5 * time.Second, Connections: 1, IOBytes: 10},
		{Now: base.Add(61 * time.Second), LogSilentFor: 5 * time.Second, Connections: 1, IOBytes: 10},
	}
	index := 0

	controller := &freezeController{
		detector: NewDetectorForPlatform("linux", 3*time.Minute),
		probe: NewLivenessProbe(5*time.Second, func(context.Context) (bool, error) {
			probeCalls++
			return true, nil
		}),
		killer: processGroupKiller{
			send: func(int, processSignal) error {
				killerCalls++
				return nil
			},
			isRunning: func(int) (bool, error) { return false, nil },
		},
		pgid: 42,
		takeSnapshot: func() (SignalSnapshot, error) {
			snapshot := snapshots[index]
			if index < len(snapshots)-1 {
				index++
			}
			return snapshot, nil
		},
	}

	if _, err := controller.Check(context.Background()); err != nil {
		t.Fatalf("warm-up Check() error = %v", err)
	}

	frozen, err := controller.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if frozen {
		t.Fatal("probe success should keep the try running")
	}
	if probeCalls != 1 {
		t.Fatalf("probe calls = %d, want 1", probeCalls)
	}
	if killerCalls != 0 {
		t.Fatalf("killer calls = %d, want 0", killerCalls)
	}
}

func TestFreezeControllerProbeFailureConfirmsFreeze(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	killerCalls := 0
	snapshots := []SignalSnapshot{
		{Now: base, LogSilentFor: 5 * time.Second, Connections: 1, IOBytes: 10},
		{Now: base.Add(61 * time.Second), LogSilentFor: 5 * time.Second, Connections: 1, IOBytes: 10},
	}
	index := 0

	controller := &freezeController{
		detector: NewDetectorForPlatform("linux", 3*time.Minute),
		probe: NewLivenessProbe(5*time.Second, func(context.Context) (bool, error) {
			return false, nil
		}),
		killer: processGroupKiller{
			send: func(int, processSignal) error {
				killerCalls++
				return nil
			},
			isRunning: func(int) (bool, error) { return false, nil },
		},
		pgid: 42,
		takeSnapshot: func() (SignalSnapshot, error) {
			snapshot := snapshots[index]
			if index < len(snapshots)-1 {
				index++
			}
			return snapshot, nil
		},
	}

	if _, err := controller.Check(context.Background()); err != nil {
		t.Fatalf("warm-up Check() error = %v", err)
	}

	frozen, err := controller.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !frozen {
		t.Fatal("probe failure should confirm freeze")
	}
	if killerCalls != 1 {
		t.Fatalf("killer calls = %d, want 1", killerCalls)
	}
}

func TestFreezeControllerProbeTimeoutConfirmsFreeze(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	killerCalls := 0
	snapshots := []SignalSnapshot{
		{Now: base, LogSilentFor: 5 * time.Second, Connections: 1, IOBytes: 10},
		{Now: base.Add(61 * time.Second), LogSilentFor: 5 * time.Second, Connections: 1, IOBytes: 10},
	}
	index := 0

	controller := &freezeController{
		detector: NewDetectorForPlatform("linux", 3*time.Minute),
		probe: NewLivenessProbe(10*time.Millisecond, func(ctx context.Context) (bool, error) {
			<-ctx.Done()
			return false, ctx.Err()
		}),
		killer: processGroupKiller{
			send: func(int, processSignal) error {
				killerCalls++
				return nil
			},
			isRunning: func(int) (bool, error) { return false, nil },
		},
		pgid: 42,
		takeSnapshot: func() (SignalSnapshot, error) {
			snapshot := snapshots[index]
			if index < len(snapshots)-1 {
				index++
			}
			return snapshot, nil
		},
	}

	if _, err := controller.Check(context.Background()); err != nil {
		t.Fatalf("warm-up Check() error = %v", err)
	}

	frozen, err := controller.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !frozen {
		t.Fatal("probe timeout should confirm freeze")
	}
	if killerCalls != 1 {
		t.Fatalf("killer calls = %d, want 1", killerCalls)
	}
}
