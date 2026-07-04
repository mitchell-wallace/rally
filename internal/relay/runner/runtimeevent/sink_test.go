package runtimeevent_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

// Compile-time assertions that both sinks satisfy the Sink contract. NoopSink's
// Emit has a value receiver, so it satisfies Sink without a pointer; the
// RecordingSink satisfies via its pointer receiver.
var (
	_ runtimeevent.Sink = runtimeevent.NoopSink{}
	_ runtimeevent.Sink = (*runtimeevent.RecordingSink)(nil)
)

// sampleEvents returns a fixed, deliberately-varied sequence of events spanning
// lifecycle markers, warnings, and operator-action feedback. Ordering tests lean
// on this exact sequence, so the kinds are kept distinct and recognizable.
func sampleEvents() []runtimeevent.Event {
	return []runtimeevent.Event{
		runtimeevent.RelayStarted{RelayID: 1, TargetIterations: 3, AgentMix: "junior"},
		runtimeevent.RouteWarning{Message: "route fell back"},
		runtimeevent.OutingHeaderReady{OutingIndex: 1, TotalOutings: 3, AgentName: "opencode"},
		runtimeevent.OperatorActionArmed{Action: runtimeevent.OperatorActionSkip, Message: "press Ctrl+S again to skip"},
		runtimeevent.AttemptFinished{FooterData: runtimeevent.FooterData{Passed: true, Attempt: 1, MaxAttempts: 3}},
		runtimeevent.OperatorActionApplied{Action: runtimeevent.OperatorActionSkip, Message: "skipping…"},
	}
}

// wantKinds is the Kind sequence sampleEvents produces, kept beside the builder
// so any reordering is caught at the assertion site, not by re-deriving kinds.
func wantKinds() []runtimeevent.Kind {
	return []runtimeevent.Kind{
		runtimeevent.KindRelayStarted,
		runtimeevent.KindRouteWarning,
		runtimeevent.KindOutingHeaderReady,
		runtimeevent.KindOperatorActionArmed,
		runtimeevent.KindAttemptFinished,
		runtimeevent.KindOperatorActionApplied,
	}
}

// TestNoopSinkEmitsAreDiscarded asserts the no-op sink accepts every event kind
// without panicking and records nothing. A nil runner sink is treated as
// NoopSink by callers, so NoopSink must be safe to hammer with any payload.
func TestNoopSinkEmitsAreDiscarded(t *testing.T) {
	sink := runtimeevent.NoopSink{}
	ctx := context.Background()

	for _, ev := range sampleEvents() {
		// Emit must not panic and returns no value to inspect; the contract is
		// "fast and side-effect-free", so we simply exercise it across kinds.
		sink.Emit(ctx, ev)
	}

	// NoopSink carries no recorded state, so there is nothing to query. The
	// meaningful assertion is that the calls above complete without panicking;
	// reaching this line is the pass condition.
}

// TestNoopSinkHandlesNilContextAndExtremePayloads guards the no-op path against
// unusual-but-legal inputs the relay may pass: a nil context (defensive) and
// zero-value payloads. NoopSink must never depend on either.
func TestNoopSinkHandlesNilContextAndExtremePayloads(t *testing.T) {
	sink := runtimeevent.NoopSink{}
	sink.Emit(nil, runtimeevent.WaitFinished{})
	sink.Emit(nil, runtimeevent.PausePromptShown{})
	sink.Emit(context.Background(), runtimeevent.RelayCompleted{})
}

// TestRecordingSinkCapturesEventsInOrder is the core ordering-semantics test:
// events must be retrievable in the exact delivery order, both as concrete
// payloads (Events) and as discriminators (Kinds). Phase-3 order tests will lean
// on this property, so it is pinned explicitly here.
func TestRecordingSinkCapturesEventsInOrder(t *testing.T) {
	sink := runtimeevent.NewRecordingSink()
	ctx := context.Background()

	for _, ev := range sampleEvents() {
		sink.Emit(ctx, ev)
	}

	got := sink.Events()
	if len(got) != len(sampleEvents()) {
		t.Fatalf("Events() len = %d, want %d", len(got), len(sampleEvents()))
	}

	wantKindsSlice := wantKinds()
	gotKinds := sink.Kinds()
	if len(gotKinds) != len(wantKindsSlice) {
		t.Fatalf("Kinds() len = %d, want %d", len(gotKinds), len(wantKindsSlice))
	}
	for i, want := range wantKindsSlice {
		if gotKinds[i] != want {
			t.Errorf("Kinds()[%d] = %v, want %v", i, gotKinds[i], want)
		}
	}

	// Cross-check that each concrete payload reports the same Kind as Kinds(),
	// so order is consistent across the two accessors.
	for i, ev := range got {
		if ev.Kind() != gotKinds[i] {
			t.Errorf("Events()[%d].Kind() = %v but Kinds()[%d] = %v", i, ev.Kind(), i, gotKinds[i])
		}
	}
}

// TestRecordingSinkPreservesPayloadData asserts the recorded events are the same
// values that were emitted (not zero-value placeholders), by spot-checking
// fields across a couple of distinct kinds. This catches a recording bug where
// ordering holds but the captured payload is wrong.
func TestRecordingSinkPreservesPayloadData(t *testing.T) {
	sink := runtimeevent.NewRecordingSink()
	ctx := context.Background()

	armed := runtimeevent.OperatorActionArmed{Action: runtimeevent.OperatorActionQuit, Message: "press Ctrl+C again to quit now"}
	rl := runtimeevent.RateLimitWaitStarted{Wait: 42 * time.Second}
	sink.Emit(ctx, armed)
	sink.Emit(ctx, rl)

	got := sink.Events()
	if len(got) != 2 {
		t.Fatalf("Events() len = %d, want 2", len(got))
	}
	gotArmed, ok := got[0].(runtimeevent.OperatorActionArmed)
	if !ok {
		t.Fatalf("Events()[0] = %T, want OperatorActionArmed", got[0])
	}
	if gotArmed != armed {
		t.Errorf("recorded armed = %+v, want %+v", gotArmed, armed)
	}
	gotRL, ok := got[1].(runtimeevent.RateLimitWaitStarted)
	if !ok {
		t.Fatalf("Events()[1] = %T, want RateLimitWaitStarted", got[1])
	}
	if gotRL != rl {
		t.Errorf("recorded rate-limit = %+v, want %+v", gotRL, rl)
	}
}

// TestRecordingSinkEventsReturnsIndependentCopy asserts Events() returns a slice
// the caller may freely mutate without corrupting future captures or subsequent
// reads. RecordingSink is shared across a relay, so aliasing would be a bug.
func TestRecordingSinkEventsReturnsIndependentCopy(t *testing.T) {
	sink := runtimeevent.NewRecordingSink()
	ctx := context.Background()
	sink.Emit(ctx, runtimeevent.RouteWarning{Message: "first"})
	sink.Emit(ctx, runtimeevent.RouteWarning{Message: "second"})

	first := sink.Events()
	if len(first) != 2 {
		t.Fatalf("first Events() len = %d, want 2", len(first))
	}
	// Mutate the returned copy aggressively.
	first[0] = runtimeevent.RouteWarning{Message: "tampered"}
	first = append(first, runtimeevent.RouteWarning{Message: "extra"})

	second := sink.Events()
	if len(second) != 2 {
		t.Fatalf("after mutation Events() len = %d, want 2 (copy was not independent)", len(second))
	}
	got0, ok := second[0].(runtimeevent.RouteWarning)
	if !ok || got0.Message != "first" {
		t.Errorf("Events()[0] mutated to %q, want %q (copy was not independent)", got0.Message, "first")
	}
}

// TestRecordingSinkLenAndReset asserts Len reports the live count and Reset
// clears the recorded sequence. Reset must be safe on an empty sink (no-op).
func TestRecordingSinkLenAndReset(t *testing.T) {
	sink := runtimeevent.NewRecordingSink()

	if sink.Len() != 0 {
		t.Fatalf("fresh sink Len() = %d, want 0", sink.Len())
	}
	sink.Reset() // resetting an empty sink must be safe
	if sink.Len() != 0 {
		t.Fatalf("Len() after empty Reset = %d, want 0", sink.Len())
	}

	ctx := context.Background()
	for _, ev := range sampleEvents() {
		sink.Emit(ctx, ev)
	}
	if want := len(sampleEvents()); sink.Len() != want {
		t.Fatalf("Len() = %d, want %d", sink.Len(), want)
	}

	sink.Reset()
	if sink.Len() != 0 {
		t.Fatalf("Len() after Reset = %d, want 0", sink.Len())
	}
	if got := sink.Events(); len(got) != 0 {
		t.Fatalf("Events() after Reset len = %d, want 0", len(got))
	}
	if got := sink.Kinds(); len(got) != 0 {
		t.Fatalf("Kinds() after Reset len = %d, want 0", len(got))
	}

	// The sink must remain usable after a reset.
	sink.Emit(ctx, runtimeevent.RelayCompleted{})
	if sink.Len() != 1 {
		t.Fatalf("Len() after post-reset emit = %d, want 1", sink.Len())
	}
}

// TestRecordingSinkConcurrentEmitsAreSafe exercises the mutex that guards the
// recorded slice. Emit is documented as synchronous and callable from the
// emitting goroutine; the accessors are mutex-guarded "so a test goroutine may
// read them concurrently". Many concurrent emitters plus concurrent readers must
// not race (run under -race) and must record exactly the emitted count.
func TestRecordingSinkConcurrentEmitsAreSafe(t *testing.T) {
	sink := runtimeevent.NewRecordingSink()
	ctx := context.Background()

	const writers = 8
	const perWriter = 200
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				sink.Emit(ctx, runtimeevent.WaitTick{Message: "tick"})
			}
		}()
	}

	// Concurrent reader while writers are active: must not panic or race.
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for i := 0; i < 100; i++ {
			_ = sink.Kinds()
			_ = sink.Len()
			_ = sink.Events()
		}
	}()

	wg.Wait()
	readerWG.Wait()

	if want := writers * perWriter; sink.Len() != want {
		t.Errorf("Len() after concurrent emits = %d, want %d (events were lost or duplicated)", sink.Len(), want)
	}
}

// TestRecordingSinkEmptyAccessors asserts a fresh sink returns empty (not nil)
// slices from Events and Kinds, so callers can range without a nil check.
func TestRecordingSinkEmptyAccessors(t *testing.T) {
	sink := runtimeevent.NewRecordingSink()
	if got := sink.Events(); got == nil {
		t.Error("Events() on fresh sink = nil, want non-nil empty slice")
	} else if len(got) != 0 {
		t.Errorf("Events() on fresh sink len = %d, want 0", len(got))
	}
	if got := sink.Kinds(); got == nil {
		t.Error("Kinds() on fresh sink = nil, want non-nil empty slice")
	} else if len(got) != 0 {
		t.Errorf("Kinds() on fresh sink len = %d, want 0", len(got))
	}
}
