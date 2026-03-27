package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/rally/messages"
	"github.com/mitchell-wallace/rally/internal/rally/progress"
	"github.com/mitchell-wallace/rally/internal/rally/runner"
	"github.com/mitchell-wallace/rally/internal/rally/state"

	tea "github.com/charmbracelet/bubbletea"
)

type Config struct {
	WorkspaceDir     string
	DataDir          string
	RepoProgressPath string
	Iterations       int
	AgentSpecs       []string
	AutoStart        bool
	ExitWhenIdle     bool
	BeadsMode        string
	ClaudeModel      string
	CodexModel       string
	GeminiModel      string
	OpenCodeModel    string
}

type model struct {
	cfg          Config
	runner       *runner.Runner
	stateStore   *state.Store
	messageStore *messages.Store
	width        int
	height       int
	status       string
	err          error
	running      bool
}

type runCompleteMsg struct {
	err error
}

type tickMsg time.Time

func Run(cfg Config) error {
	r := runner.New(runner.Config{
		WorkspaceDir:     cfg.WorkspaceDir,
		DataDir:          cfg.DataDir,
		RepoProgressPath: cfg.RepoProgressPath,
		AgentSpecs:       cfg.AgentSpecs,
		Iterations:       cfg.Iterations,
		Stdout:           os.Stdout,
		Stderr:           os.Stderr,
		BeadsMode:        cfg.BeadsMode,
		ClaudeModel:      cfg.ClaudeModel,
		CodexModel:       cfg.CodexModel,
		GeminiModel:      cfg.GeminiModel,
		OpenCodeModel:    cfg.OpenCodeModel,
	})
	if err := r.EnsureInitialized(); err != nil {
		return err
	}
	m := model{
		cfg:          cfg,
		runner:       r,
		stateStore:   state.NewStore(cfg.DataDir),
		messageStore: messages.NewStore(cfg.DataDir),
		status:       "Press s to start a batch, +/- to resize, x to stop after current, r to repair progress, q to quit.",
	}
	prog := tea.NewProgram(m, tea.WithAltScreen())
	_, err := prog.Run()
	return err
}

func (m model) Init() tea.Cmd {
	if m.cfg.AutoStart {
		return tea.Batch(m.startRun(), tick())
	}
	return tick()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "s":
			if !m.running {
				return m, m.startRun()
			}
		case "+":
			if err := m.adjustBatch(1); err != nil {
				m.err = err
			}
		case "-":
			if err := m.adjustBatch(-1); err != nil {
				m.err = err
			}
		case "x":
			if err := m.runner.RequestStopAfterCurrent(); err != nil {
				m.err = err
			} else {
				m.status = "Stop requested after current session."
			}
		case "r":
			if err := m.repair(); err != nil {
				m.err = err
			} else {
				m.status = "Rebuilt repo progress from session records."
			}
		}
	case runCompleteMsg:
		m.running = false
		m.err = msg.err
		if msg.err == nil {
			m.status = "Batch run finished."
		}
		if m.cfg.ExitWhenIdle {
			return m, tea.Quit
		}
	case tickMsg:
		return m, tick()
	}
	return m, nil
}

func (m model) View() string {
	st, _ := m.stateStore.Load()
	repo, _ := progress.RebuildRepoProgress(m.cfg.DataDir, m.cfg.RepoProgressPath, activeBatchMap(st.ActiveBatch))
	events, _ := m.messageStore.Load()
	folded := messages.Fold(events)
	lines := []string{
		"Rally",
		"",
		fmt.Sprintf("Data dir: %s", m.cfg.DataDir),
		fmt.Sprintf("Repo progress: %s", m.cfg.RepoProgressPath),
	}
	if st.ActiveBatch != nil {
		lines = append(lines,
			fmt.Sprintf("Active batch: #%d", st.ActiveBatch.BatchID),
			fmt.Sprintf("Iterations: %d/%d", st.ActiveBatch.CompletedIterations, st.ActiveBatch.TargetIterations),
			fmt.Sprintf("Stop after current: %t", st.StopAfterCurrent),
		)
	} else {
		lines = append(lines, "Active batch: none")
	}
	lines = append(lines, "")
	lines = append(lines, "Recent sessions:")
	if len(repo.RecentSessions) == 0 {
		lines = append(lines, "  (none)")
	} else {
		for _, session := range repo.RecentSessions {
			lines = append(lines, fmt.Sprintf("  #%d batch=%d iter=%d agent=%s status=%s", session.SessionID, session.BatchID, session.IterationIndex, session.Agent, session.Status))
		}
	}
	lines = append(lines, "", "Messages:")
	items := messages.OrderedMessages(folded)
	if len(items) == 0 {
		lines = append(lines, "  (none)")
	} else {
		for _, item := range items {
			stateLabel := "pending"
			if item.Consumed {
				stateLabel = "consumed"
			}
			if item.Canceled {
				stateLabel = "cancelled"
			}
			lines = append(lines, fmt.Sprintf("  #%d [%s] %s: %s", item.MessageID, stateLabel, item.Scope, item.Body))
		}
	}
	lines = append(lines, "", m.status)
	if m.err != nil {
		lines = append(lines, "ERROR: "+m.err.Error())
	}
	return strings.Join(lines, "\n")
}

func (m model) startRun() tea.Cmd {
	m.running = true
	m.status = "Running batch..."
	return func() tea.Msg {
		_, err := m.runner.Run(context.Background())
		return runCompleteMsg{err: err}
	}
}

func (m model) adjustBatch(delta int) error {
	st, err := m.stateStore.Load()
	if err != nil {
		return err
	}
	target := m.cfg.Iterations
	if st.ActiveBatch != nil {
		target = st.ActiveBatch.TargetIterations
	}
	target += delta
	if target < 1 {
		target = 1
	}
	m.cfg.Iterations = target
	if err := m.runner.ResizeBatch(target); err != nil {
		return err
	}
	m.status = fmt.Sprintf("Batch target set to %d iterations.", target)
	return nil
}

func (m model) repair() error {
	st, err := m.stateStore.Load()
	if err != nil {
		return err
	}
	_, err = progress.RebuildRepoProgress(m.cfg.DataDir, m.cfg.RepoProgressPath, activeBatchMap(st.ActiveBatch))
	return err
}

func activeBatchMap(batch *state.BatchState) map[string]any {
	if batch == nil {
		return nil
	}
	return map[string]any{
		"batch_id":             batch.BatchID,
		"target_iterations":    batch.TargetIterations,
		"completed_iterations": batch.CompletedIterations,
		"agent_mix":            batch.AgentMix,
		"started_at":           batch.StartedAt,
		"ended_at":             batch.EndedAt,
	}
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}
