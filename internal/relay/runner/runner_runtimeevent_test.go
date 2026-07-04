package runner

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/store"
)

// TestRunnerEventSinkNilSafe pins the nil-safe defaulting contract for
// Config.EventSink (design Decision 6): a nil sink behaves as a no-op so emit
// sites added in phase 3.2 can stay branch-free, and an injected sink is
// returned verbatim. A nil Controls is the no-operator-input value (headless /
// library construction); there is no no-op ControlSource, so the field is
// returned unchanged and consumers nil-check it.
func TestRunnerEventSinkNilSafe(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	t.Run("nil sink defaults to no-op", func(t *testing.T) {
		r := NewRunner(s, Config{}, nil)
		if _, ok := r.eventSink().(runtimeevent.NoopSink); !ok {
			t.Fatalf("nil EventSink must surface as NoopSink, got %T", r.eventSink())
		}
		// The no-op must be callable inline without panicking; a headless runner
		// emits nothing observable.
		r.eventSink().Emit(context.Background(), runtimeevent.RelayStarted{})
	})

	t.Run("injected sink is returned verbatim", func(t *testing.T) {
		rec := runtimeevent.NewRecordingSink()
		r := NewRunner(s, Config{EventSink: rec}, nil)
		if r.eventSink() != rec {
			t.Fatalf("injected EventSink must be returned verbatim, got %T", r.eventSink())
		}
		r.eventSink().Emit(context.Background(), runtimeevent.RelayStarted{})
		if rec.Len() != 1 {
			t.Fatalf("emitted event must reach the injected sink, got %d events", rec.Len())
		}
	})

	t.Run("nil controls means no operator input", func(t *testing.T) {
		r := NewRunner(s, Config{}, nil)
		// Controls has no no-op stand-in: nil is the headless value and is left
		// for consumers to nil-check (phase 5 wires the keyboard sessions).
		if r.cfg.Controls != nil {
			t.Fatalf("Controls must default to nil (no operator input), got %T", r.cfg.Controls)
		}
	})
}

func TestRunnerStatusWriterDefaulting(t *testing.T) {
	s, err := store.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	t.Run("nil writer defaults to stdout", func(t *testing.T) {
		r := NewRunner(s, Config{}, nil)
		if got := r.statusWriter(); got != os.Stdout {
			t.Fatalf("nil StatusWriter must default to os.Stdout, got %T", got)
		}
	})

	t.Run("injected writer is returned verbatim", func(t *testing.T) {
		buf := &bytes.Buffer{}
		r := NewRunner(s, Config{StatusWriter: buf}, nil)
		if got := r.statusWriter(); got != buf {
			t.Fatalf("injected StatusWriter must be returned verbatim, got %T", got)
		}
	})
}
