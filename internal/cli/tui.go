package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/laps"
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
	cmd.Flags().StringArrayP("agent", "a", nil, "Agent mix (repeatable; comma- or space-separated, e.g. \"cl:2,cx:1\" or \"cl:2 cx:1\")")
	cmd.Flags().StringArrayP("mix", "m", nil, "Legacy synonym for --agent")
	cmd.Flags().Bool("resume", false, "Resume the last unfinished batch explicitly")
	cmd.Flags().Bool("new", false, "Start a new batch explicitly, discarding unfinished batch state")
	cmd.Flags().Bool("demo", false, "Run synthetic TUI demo playback without a .rally workspace")
	cmd.Flags().String("view", "", "View a historical relay (latest, or a relay ID)")
	cmd.Flags().Lookup("view").NoOptDefVal = "latest"
	return cmd
}

func runTui(cmd *cobra.Command, args []string, opts RootOptions) error {
	ctx := context.Background()
	configBindings, err := newTUIConfigBindings(ctx)
	if err != nil {
		return fmt.Errorf("load TUI config: %w", err)
	}
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
		fetchLaps := makeTuiLapsFetcher(workspaceDir)
		events, relayID, err := synthesizeRelayEvents(workspaceDir, view)
		if err != nil {
			return err
		}
		sessionOpts := tuitabs.Options{
			Title:     "rally tui",
			DoneHint:  fmt.Sprintf("historical view relay #%d - q to exit", relayID),
			FetchLaps: fetchLaps,
		}
		configBindings.apply(&sessionOpts)
		session := tuitabs.NewSession(sessionOpts)
		return session.OutingView(ctx, events)
	}

	sessionOpts := tuitabs.Options{Title: "rally tui"}
	configBindings.apply(&sessionOpts)
	session := tuitabs.NewSession(sessionOpts)
	if demo {
		return session.RunDemo(ctx)
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
	sessionOpts = tuitabs.Options{
		Title:     "rally tui",
		Seed:      seed,
		Agents:    agents,
		FetchLaps: makeTuiLapsFetcher(ro.WorkspaceDir),
		DoneHintFunc: func() string {
			if h := app.RelayEndHint(ro.WorkspaceDir); h != "" {
				return h + " · q to exit"
			}
			return ""
		},
	}
	configBindings.apply(&sessionOpts)
	session = tuitabs.NewSession(sessionOpts)
	ro.EventSink = session.Sink()
	ro.Controls = session.Controls()
	ro.StatusWriter = session.StatusWriter()
	ro.Out = session.TranscriptWriter()
	ro.Err = session.TranscriptWriter()
	return session.Run(ctx, func(ctx context.Context) error {
		return app.StartRelay(ctx, ro)
	})
}

func makeTuiLapsFetcher(workspaceDir string) func(context.Context) (tuicore.LapsSnapshot, error) {
	return func(ctx context.Context) (tuicore.LapsSnapshot, error) {
		// Bound the laps subprocesses: a hung fetch would otherwise leave the
		// tab loading forever (FetchCmd coalesces on the in-flight flag).
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		snapshot, err := (&laps.Adapter{WorkspaceDir: workspaceDir}).QueueSnapshot(ctx)
		if err != nil {
			return tuicore.LapsSnapshot{}, err
		}
		return lapsSnapshotToTui(snapshot), nil
	}
}

func lapsSnapshotToTui(snapshot laps.QueueSnapshot) tuicore.LapsSnapshot {
	out := tuicore.LapsSnapshot{
		Missing:     snapshot.Missing,
		State:       snapshot.State,
		Counts:      tuicore.LapsCounts{Todo: snapshot.Counts.Todo, Done: snapshot.Counts.Done, Total: snapshot.Counts.Total},
		Claim:       tuicore.LapsClaim{Valid: snapshot.Claim.Valid, Lap: snapshot.Claim.Lap, File: snapshot.Claim.File, ClaimedAt: snapshot.Claim.ClaimedAt, AgeSeconds: snapshot.Claim.AgeSeconds},
		ActiveStint: snapshot.ActiveStint,
		Entries:     lapsEntriesToTui(snapshot.Entries),
	}
	if snapshot.Gate != nil {
		out.Gate = &tuicore.LapsGate{
			State:   snapshot.Gate.State,
			Stint:   snapshot.Gate.Stint,
			Scope:   snapshot.Gate.Scope,
			File:    snapshot.Gate.File,
			Message: snapshot.Gate.Message,
		}
	}
	return out
}

func lapsEntriesToTui(entries []laps.QueueEntry) []tuicore.LapsEntry {
	out := make([]tuicore.LapsEntry, len(entries))
	for i, entry := range entries {
		out[i] = tuicore.LapsEntry{
			Kind:     entry.Kind,
			ID:       entry.ID,
			Ref:      entry.Ref,
			Title:    entry.Title,
			Assignee: entry.Assignee,
			IsDone:   entry.IsDone,
			Order:    entry.Order,
			Laps:     lapsEntriesToTui(entry.Laps),
		}
		if entry.Stint != nil {
			out[i].Stint = &tuicore.LapsStint{
				Name:     entry.Stint.Name,
				Scope:    entry.Stint.Scope,
				File:     entry.Stint.File,
				Todo:     entry.Stint.Todo,
				Done:     entry.Stint.Done,
				Total:    entry.Stint.Total,
				Queued:   entry.Stint.Queued,
				Archived: entry.Stint.Archived,
				Active:   entry.Stint.Active,
				Laps:     lapsEntriesToTui(entry.Stint.Laps),
			}
		}
	}
	return out
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
