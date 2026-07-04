package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/presentation/tuitabs"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/spf13/cobra"
)

func newTuiCmd(opts RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "tui",
		Short:        "Run a relay in the terminal UI",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTui(cmd, args, opts)
		},
	}
	cmd.Flags().IntP("iterations", "i", 0, "Number of iterations (default 50 unless laps-backed)")
	cmd.Flags().StringArrayP("agent", "a", nil, "Agent mix (repeatable; comma- or space-separated, e.g. \"cc:2,cx:1\" or \"cc:2 cx:1\")")
	cmd.Flags().StringArrayP("mix", "m", nil, "Legacy synonym for --agent")
	cmd.Flags().Bool("resume", false, "Resume the last unfinished batch explicitly")
	cmd.Flags().Bool("new", false, "Start a new batch explicitly, discarding unfinished batch state")
	cmd.Flags().Bool("demo", false, "Run synthetic TUI demo playback without a .rally workspace")
	cmd.Flags().String("view", "", "View a historical relay (latest, or a relay ID)")
	cmd.Flags().Lookup("view").NoOptDefVal = "latest"
	return cmd
}

func runTui(cmd *cobra.Command, args []string, opts RootOptions) error {
	demo, _ := cmd.Flags().GetBool("demo")
	view, _ := cmd.Flags().GetString("view")
	resume, _ := cmd.Flags().GetBool("resume")
	newBatch, _ := cmd.Flags().GetBool("new")
	if view != "" {
		var conflicts []string
		if demo {
			conflicts = append(conflicts, "--demo")
		}
		if resume {
			conflicts = append(conflicts, "--resume")
		}
		if newBatch {
			conflicts = append(conflicts, "--new")
		}
		if len(conflicts) > 0 {
			return fmt.Errorf("--view cannot be used with %s", strings.Join(conflicts, ", "))
		}
		workspaceDir, err := resolveWorkspaceDir()
		if err != nil {
			return err
		}
		events, relayID, err := synthesizeRelayEvents(workspaceDir, view)
		if err != nil {
			return err
		}
		session := tuitabs.NewSession(tuitabs.Options{
			Title:    "rally tui",
			DoneHint: fmt.Sprintf("historical view relay #%d - q to exit", relayID),
		})
		return session.RunView(context.Background(), events)
	}

	session := tuitabs.NewSession(tuitabs.Options{Title: "rally tui"})
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
	agents, err := loadTuiState(ro.WorkspaceDir)
	if err != nil {
		return fmt.Errorf("load TUI state: %w", err)
	}
	session = tuitabs.NewSession(tuitabs.Options{Title: "rally tui", Seed: seed, Agents: agents})
	ro.EventSink = session.Sink()
	ro.Controls = session.Controls()
	ro.StatusWriter = session.StatusWriter()
	ro.Out = session.TranscriptWriter()
	ro.Err = session.TranscriptWriter()
	return session.Run(context.Background(), func(ctx context.Context) error {
		return app.StartRelay(ctx, ro)
	})
}

func loadTuiState(workspaceDir string) ([]tuicore.AgentStatusItem, error) {
	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		return nil, err
	}
	return agentStatusEventsToItems(s.AllAgentStatus()), nil
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
