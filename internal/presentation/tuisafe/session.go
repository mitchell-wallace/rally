package tuisafe

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

type Options struct {
	Title string
}

type Session struct {
	title    string
	controls *controls

	// send delivery is FIFO through a single drainer goroutine: p.Send blocks
	// on the program's unbuffered msg channel, and per-message goroutines would
	// let transcript events overtake each other. sendAsync must never block the
	// emitter (the runner's control loop), so messages queue under mu and one
	// drainer at a time forwards them in order.
	mu       sync.Mutex
	send     func(tea.Msg)
	queue    []tea.Msg
	draining bool
}

func NewSession(opts Options) *Session {
	title := opts.Title
	if title == "" {
		title = "rally tui-1"
	}
	return &Session{
		title:    title,
		controls: newControls(),
	}
}

func (s *Session) Sink() runtimeevent.Sink {
	return sinkFunc(func(_ context.Context, event runtimeevent.Event) {
		if event == nil {
			return
		}
		s.sendAsync(eventMsg{event: event})
	})
}

func (s *Session) Controls() runtimeevent.ControlSource {
	return s.controls
}

func (s *Session) StatusWriter() io.Writer {
	return &statusWriter{send: s.sendAsync}
}

func (s *Session) TranscriptWriter() io.Writer {
	return &lineWriter{send: s.sendAsync}
}

func (s *Session) Run(ctx context.Context, work func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if work == nil {
		work = func(context.Context) error { return nil }
	}

	workCtx, cancelWork := context.WithCancel(ctx)
	defer cancelWork()

	doneCh := make(chan error, 1)
	m := newModel(s.title, s.controls)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	s.setSend(p.Send)
	defer s.setSend(nil)

	go func() {
		err := work(workCtx)
		doneCh <- err
		s.sendAsync(doneMsg{err: err})
	}()

	finalModel, runErr := p.Run()
	if runErr != nil && ctx.Err() == nil {
		cancelWork()
	}

	var workErr error
	select {
	case workErr = <-doneCh:
	default:
		cancelWork()
		workErr = <-doneCh
	}

	if runErr != nil && ctx.Err() == nil && !isOperatorQuit(finalModel) {
		return runErr
	}
	return workErr
}

func (s *Session) RunDemo(ctx context.Context) error {
	return s.Run(ctx, func(ctx context.Context) error {
		script := tuicore.DemoScript()
		statuses := tuicore.DemoStatusFrames()

		statusCtx, cancelStatus := context.WithCancel(ctx)
		defer cancelStatus()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		done := make(chan struct{})
		go func() {
			defer close(done)
			if len(statuses) == 0 {
				return
			}
			i := 0
			for {
				select {
				case <-statusCtx.Done():
					return
				case <-ticker.C:
					s.sendAsync(statusFrameMsg{line: statuses[i%len(statuses)]})
					i++
				}
			}
		}()

		for _, step := range script {
			if err := sleepContext(ctx, step.Delay); err != nil {
				return err
			}
			s.Sink().Emit(ctx, step.Event)
		}
		cancelStatus()
		<-done
		return nil
	})
}

func (s *Session) setSend(send func(tea.Msg)) {
	s.mu.Lock()
	s.send = send
	if send == nil {
		s.queue = nil
	}
	s.mu.Unlock()
}

func (s *Session) sendAsync(msg tea.Msg) {
	s.mu.Lock()
	if s.send == nil {
		s.mu.Unlock()
		return
	}
	s.queue = append(s.queue, msg)
	if s.draining {
		s.mu.Unlock()
		return
	}
	s.draining = true
	s.mu.Unlock()
	go s.drain()
}

// drain forwards queued messages in FIFO order. Only one drainer runs at a
// time (the draining flag), so ordering is preserved even though p.Send may
// block until the program's event loop picks each message up.
func (s *Session) drain() {
	for {
		s.mu.Lock()
		if len(s.queue) == 0 || s.send == nil {
			s.queue = nil
			s.draining = false
			s.mu.Unlock()
			return
		}
		msg := s.queue[0]
		s.queue = s.queue[1:]
		send := s.send
		s.mu.Unlock()
		send(msg)
	}
}

type sinkFunc func(context.Context, runtimeevent.Event)

func (f sinkFunc) Emit(ctx context.Context, event runtimeevent.Event) {
	f(ctx, event)
}

type statusWriter struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	send func(tea.Msg)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	clean := ansi.Strip(w.buf.String())
	if line := extractStatusLine(clean); line != "" {
		w.send(statusFrameMsg{line: line})
	}
	if w.buf.Len() > 4096 {
		w.buf.Reset()
		w.buf.WriteString(clean)
	}
	return len(p), nil
}

type lineWriter struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	send func(tea.Msg)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	data := w.buf.String()
	for {
		idx := strings.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSuffix(data[:idx], "\r")
		w.send(transcriptLineMsg{line: line})
		data = data[idx+1:]
	}
	w.buf.Reset()
	w.buf.WriteString(data)
	return len(p), nil
}

func extractStatusLine(frame string) string {
	frame = strings.ReplaceAll(frame, "\r", "\n")
	var last string
	for _, line := range strings.Split(frame, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "[2K" || strings.HasPrefix(line, "[") {
			continue
		}
		last = line
	}
	return last
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isOperatorQuit(final tea.Model) bool {
	if m, ok := final.(model); ok {
		return m.quitting
	}
	return false
}
