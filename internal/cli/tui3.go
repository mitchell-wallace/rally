package cli

import (
	"context"
	"fmt"
	"sort"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/presentation/tuitabs"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/spf13/cobra"
)

func newTui3Cmd(opts RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "tui-3",
		Short:        "TUI prototype 3: multi-tab relay view",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTui3(cmd, args, opts)
		},
	}
	cmd.Flags().IntP("iterations", "i", 0, "Number of iterations (default 50 unless laps-backed)")
	cmd.Flags().StringArrayP("agent", "a", nil, "Agent mix (repeatable; comma- or space-separated, e.g. \"cc:2,cx:1\" or \"cc:2 cx:1\")")
	cmd.Flags().StringArrayP("mix", "m", nil, "Legacy synonym for --agent")
	cmd.Flags().Bool("resume", false, "Resume the last unfinished batch explicitly")
	cmd.Flags().Bool("new", false, "Start a new batch explicitly, discarding unfinished batch state")
	cmd.Flags().Bool("demo", false, "Run synthetic TUI demo playback without a .rally workspace")
	return cmd
}

func runTui3(cmd *cobra.Command, args []string, opts RootOptions) error {
	demo, _ := cmd.Flags().GetBool("demo")
	session := tuitabs.NewSession(tuitabs.Options{Title: "rally tui-3"})
	if demo {
		return session.RunDemo(context.Background())
	}

	ro, err := prepareRelayStart(cmd, args, opts)
	if err != nil {
		return err
	}
	seed, err := loadTuiFeedSeed(ro.WorkspaceDir)
	if err != nil {
		return fmt.Errorf("load TUI feed seed: %w", err)
	}
	messages, agents, err := loadTui3State(ro.WorkspaceDir)
	if err != nil {
		return fmt.Errorf("load TUI state: %w", err)
	}
	session = tuitabs.NewSession(tuitabs.Options{Title: "rally tui-3", Seed: seed, Messages: messages, Agents: agents})
	ro.EventSink = session.Sink()
	ro.Controls = session.Controls()
	ro.StatusWriter = session.StatusWriter()
	ro.Out = session.TranscriptWriter()
	ro.Err = session.TranscriptWriter()
	return session.Run(context.Background(), func(ctx context.Context) error {
		return app.StartRelay(ctx, ro)
	})
}

func loadTui3State(workspaceDir string) ([]tuicore.MessageItem, []tuicore.AgentStatusItem, error) {
	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		return nil, nil, err
	}
	return messageRecordsToItems(s.GetMessages()), agentStatusEventsToItems(s.AllAgentStatus()), nil
}

func messageRecordsToItems(records []store.MessageRecord) []tuicore.MessageItem {
	items := make([]tuicore.MessageItem, 0, len(records))
	for _, record := range records {
		items = append(items, tuicore.MessageItem{
			ID:        record.ID,
			Body:      record.Body,
			Status:    record.Status,
			Position:  record.Position,
			Scope:     record.Scope,
			CreatedAt: parseRFC3339(record.CreatedAt),
		})
	}
	return items
}

func agentStatusEventsToItems(events []store.AgentStatusEvent) []tuicore.AgentStatusItem {
	type key struct {
		agent string
		model string
	}
	latest := make(map[key]tuicore.AgentStatusItem)
	for _, event := range events {
		k := key{agent: event.AgentType, model: event.Model}
		since := parseRFC3339(event.Timestamp)
		existing, ok := latest[k]
		if ok && !existing.Since.IsZero() && !since.After(existing.Since) {
			continue
		}
		latest[k] = tuicore.AgentStatusItem{
			Agent:   event.AgentType,
			Model:   event.Model,
			State:   event.EventType,
			Reason:  event.Reason,
			Since:   since,
			ResetAt: parseRFC3339(event.ResetAt),
		}
	}
	items := make([]tuicore.AgentStatusItem, 0, len(latest))
	for _, item := range latest {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Agent != items[j].Agent {
			return items[i].Agent < items[j].Agent
		}
		return items[i].Model < items[j].Model
	})
	return items
}
