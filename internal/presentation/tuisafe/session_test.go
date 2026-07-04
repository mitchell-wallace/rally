package tuisafe

import (
	"context"
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

func TestStatusWriterExtractsMonitorStatus(t *testing.T) {
	var got []tea.Msg
	w := &statusWriter{send: func(msg tea.Msg) {
		got = append(got, msg)
	}}

	frame := "\x1b[2A\r\x1b[2K⏱ 1m 05s  │  📁 2 files  │  last activity: < 1m ago\x1b[2B\r"
	if _, err := w.Write([]byte(frame)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	msg, ok := got[0].(statusFrameMsg)
	if !ok {
		t.Fatalf("message type = %T, want statusFrameMsg", got[0])
	}
	want := "⏱ 1m 05s  │  📁 2 files  │  last activity: < 1m ago"
	if msg.line != want {
		t.Fatalf("status line = %q, want %q", msg.line, want)
	}
}

func TestSinkPreservesEmitOrder(t *testing.T) {
	s := NewSession(Options{Title: "test"})
	const n = 200
	msgs := make(chan tea.Msg, n)
	s.setSend(func(msg tea.Msg) {
		msgs <- msg
	})
	defer s.setSend(nil)

	sink := s.Sink()
	for i := 0; i < n; i++ {
		sink.Emit(context.Background(), runtimeevent.TryStatusSnapshot{Status: fmt.Sprintf("status %d", i)})
	}

	for i := 0; i < n; i++ {
		select {
		case msg := <-msgs:
			got, ok := msg.(eventMsg)
			if !ok {
				t.Fatalf("message %d type = %T, want eventMsg", i, msg)
			}
			want := fmt.Sprintf("status %d", i)
			if status := got.event.(runtimeevent.TryStatusSnapshot).Status; status != want {
				t.Fatalf("message %d = %q, want %q (delivery must be FIFO)", i, status, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for message %d", i)
		}
	}
}

func TestSinkForwardsEvents(t *testing.T) {
	s := NewSession(Options{Title: "test"})
	msgs := make(chan tea.Msg, 1)
	s.setSend(func(msg tea.Msg) {
		msgs <- msg
	})
	defer s.setSend(nil)

	event := runtimeevent.RelayStarted{RelayID: 7, TargetIterations: 9, AgentMix: "mix"}
	s.Sink().Emit(context.Background(), event)

	select {
	case msg := <-msgs:
		got, ok := msg.(eventMsg)
		if !ok {
			t.Fatalf("message type = %T, want eventMsg", msg)
		}
		if got.event != event {
			t.Fatalf("event = %#v, want %#v", got.event, event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for forwarded event")
	}
}
