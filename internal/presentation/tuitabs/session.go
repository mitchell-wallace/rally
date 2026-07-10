package tuitabs

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
	Title        string
	DoneHint     string
	Seed         []tuicore.FeedItem
	Agents       []tuicore.AgentStatusItem
	Laps         tuicore.LapsSnapshot
	FetchLaps    func(context.Context) (tuicore.LapsSnapshot, error)
	Config       tuicore.ConfigSnapshot
	FetchConfig  func(context.Context) (tuicore.ConfigSnapshot, error)
	UpdateConfig func(context.Context, tuicore.ConfigMutation) (tuicore.ConfigSnapshot, error)
}

type Session struct {
	title        string
	doneHint     string
	seed         []tuicore.FeedItem
	agents       []tuicore.AgentStatusItem
	laps         tuicore.LapsSnapshot
	fetchLaps    func(context.Context) (tuicore.LapsSnapshot, error)
	config       tuicore.ConfigSnapshot
	fetchConfig  configLoader
	updateConfig configUpdater
	controls     *controls

	mu       sync.Mutex
	send     func(tea.Msg)
	queue    []tea.Msg
	draining bool
}

func NewSession(opts Options) *Session {
	title := opts.Title
	if title == "" {
		title = "rally tui"
	}
	configSnapshot := tuicore.CloneConfigSnapshot(opts.Config)
	if configSnapshot.Path == "" {
		configSnapshot = tuicore.DemoConfigSnapshot()
	}
	return &Session{
		title:        title,
		doneHint:     opts.DoneHint,
		seed:         cloneFeedItems(opts.Seed),
		agents:       cloneAgents(opts.Agents),
		laps:         tuicore.CloneLapsSnapshot(opts.Laps),
		fetchLaps:    opts.FetchLaps,
		config:       configSnapshot,
		fetchConfig:  opts.FetchConfig,
		updateConfig: opts.UpdateConfig,
		controls:     newControls(),
	}
}

func (s *Session) Sink() runtimeevent.Sink {
	return sinkFunc(func(_ context.Context, event runtimeevent.Event) {
		if event != nil {
			s.sendAsync(eventMsg{event: event})
		}
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

func (s *Session) Enrich(outingIndex int, summary, classification string, followups []string) {
	s.sendAsync(enrichMsg{outingIndex: outingIndex, summary: summary, classification: classification, followups: append([]string(nil), followups...)})
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
	m.doneHint = s.doneHint
	m.dashboard = m.dashboard.Seed(s.seed)
	m.agents.Seed(s.agents)
	m.laps = newLapsModel(s.fetchLaps).WithSnapshot(s.laps)
	m.config = newConfigModel(s.fetchConfig, s.updateConfig).WithSnapshot(s.config)
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
	s.seed = tuicore.DemoFeedSeed()
	s.agents = tuicore.DemoAgentStatuses()
	s.laps = tuicore.DemoLapsSnapshot()
	return s.Run(ctx, func(ctx context.Context) error {
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

		enrichments := demoEnrichments()
		currentOuting := -1
		for _, step := range tuicore.DemoScript() {
			if err := sleepContext(ctx, step.Delay); err != nil {
				return err
			}
			if header, ok := step.Event.(runtimeevent.OutingHeaderReady); ok {
				currentOuting = header.OutingIndex
			}
			s.Sink().Emit(ctx, step.Event)
			switch step.Event.(type) {
			case runtimeevent.AttemptFinished, runtimeevent.AttemptCancelled, runtimeevent.HandoffAttemptFinished:
				if enrichment, ok := enrichments[currentOuting]; ok {
					s.Enrich(currentOuting, enrichment.summary, enrichment.classification, enrichment.followups)
				}
			}
		}
		cancelStatus()
		<-done
		return nil
	})
}

func (s *Session) OutingView(ctx context.Context, events []runtimeevent.Event) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m := newModel(s.title, s.controls)
	m.doneHint = s.doneHint
	m.dashboard = m.dashboard.Seed(s.seed)
	m.agents.Seed(s.agents)
	m.laps = newLapsModel(s.fetchLaps).WithSnapshot(s.laps)
	m.config = newConfigModel(s.fetchConfig, s.updateConfig).WithSnapshot(s.config)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	s.setSend(p.Send)
	defer s.setSend(nil)

	go func() {
		for _, event := range events {
			s.Sink().Emit(ctx, event)
		}
		s.sendAsync(doneMsg{})
	}()

	finalModel, runErr := p.Run()
	if runErr != nil && ctx.Err() == nil && !isOperatorQuit(finalModel) {
		return runErr
	}
	return nil
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
		if line == "" || line == "[2K" || strings.HasPrefix(line, "[") {
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

type demoEnrichment struct {
	summary        string
	classification string
	followups      []string
}

func demoEnrichments() map[int]demoEnrichment {
	return map[int]demoEnrichment{
		0: {summary: "Captured the baseline exported surface and confirmed the tabbed prototype can stay presentation-local.", classification: "analysis"},
		1: {summary: "Reused the dashboard panel component while fanning events into the transcript reducer.", classification: "implementation"},
		2: {summary: "The verification lap exhausted its retry budget on provider usage limits before producing code changes.", classification: "quota-limit"},
		3: {summary: "Kept messages and agent statuses as read-only view models seeded by the CLI.", classification: "implementation"},
	}
}

func cloneFeedItems(items []tuicore.FeedItem) []tuicore.FeedItem {
	out := make([]tuicore.FeedItem, len(items))
	for i, item := range items {
		item.Followups = append([]string(nil), item.Followups...)
		out[i] = item
	}
	return out
}

func cloneAgents(items []tuicore.AgentStatusItem) []tuicore.AgentStatusItem {
	out := make([]tuicore.AgentStatusItem, len(items))
	copy(out, items)
	return out
}
