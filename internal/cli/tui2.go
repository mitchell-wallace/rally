package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/presentation/tuipanels"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/spf13/cobra"
)

func newTui2Cmd(opts RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "tui-2",
		Short:        "TUI prototype 2: single-tab multi-panel relay view",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTui2(cmd, args, opts)
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

func runTui2(cmd *cobra.Command, args []string, opts RootOptions) error {
	demo, _ := cmd.Flags().GetBool("demo")
	session := tuipanels.NewSession(tuipanels.Options{Title: "rally tui-2"})
	if demo {
		return session.RunDemo(context.Background())
	}

	ro, err := prepareRelayStart(cmd, args, opts)
	if err != nil {
		return err
	}
	seed, err := loadTui2Seed(ro.WorkspaceDir)
	if err != nil {
		return fmt.Errorf("load TUI seed: %w", err)
	}
	session = tuipanels.NewSession(tuipanels.Options{Title: "rally tui-2", Seed: seed})
	ro.EventSink = session.Sink()
	ro.Controls = session.Controls()
	ro.StatusWriter = session.StatusWriter()
	ro.Out = session.TranscriptWriter()
	ro.Err = session.TranscriptWriter()
	return session.Run(context.Background(), func(ctx context.Context) error {
		return app.StartRelay(ctx, ro)
	})
}

func loadTui2Seed(workspaceDir string) ([]tuicore.FeedItem, error) {
	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		return nil, err
	}
	tries := s.RecentTries(20)
	summaries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		return nil, err
	}
	summaryByRunID := make(map[string]progress.RunEntry, len(summaries))
	for _, entry := range summaries {
		if strings.TrimSpace(entry.RunID) == "" {
			continue
		}
		summaryByRunID[entry.RunID] = entry
	}

	items := make([]tuicore.FeedItem, 0, len(tries))
	for _, tr := range tries {
		if !isFinishedTry(tr) {
			continue
		}
		item := tryRecordToFeedItem(tr)
		if entry, ok := summaryByRunID[strconv.Itoa(tr.RunID)]; ok {
			enrichFeedItem(&item, entry)
		} else if tr.LapID != "" {
			if entry, ok := summaryByRunID[tr.LapID]; ok {
				enrichFeedItem(&item, entry)
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func isFinishedTry(tr store.TryRecord) bool {
	return tr.Completed || tr.Outcome != ""
}

func tryRecordToFeedItem(tr store.TryRecord) tuicore.FeedItem {
	started := parseRFC3339(tr.StartedAt)
	duration := time.Duration(tr.RuntimeMs) * time.Millisecond
	if duration <= 0 {
		ended := parseRFC3339(tr.EndedAt)
		if !started.IsZero() && !ended.IsZero() && ended.After(started) {
			duration = ended.Sub(started)
		}
	}
	title := tr.Summary
	if title == "" {
		title = tr.LapID
	}
	if title == "" {
		title = fmt.Sprintf("run %d", tr.RunID)
	}
	return tuicore.FeedItem{
		RunIndex:       tr.RunID,
		Agent:          tr.AgentType,
		RoleLabel:      tr.LapAssignee,
		Title:          tui2FirstLine(title),
		StartedAt:      started,
		Outcome:        outcomeFromTry(tr),
		Duration:       duration,
		Files:          len(tr.FilesChanged),
		CommitHash:     tr.CommitHash,
		FailReason:     tr.FailReason,
		Attempt:        tr.AttemptNumber,
		MaxAttempts:    tr.AttemptNumber,
		Summary:        tr.Summary,
		Classification: tr.RecoveryClassification,
	}
}

func outcomeFromTry(tr store.TryRecord) string {
	if tr.HandoffOnly || tr.Outcome == reliability.OutcomeHandoffRequested {
		return tuicore.OutcomeHandoff
	}
	switch tr.Outcome {
	case reliability.OutcomeCompleted:
		return tuicore.OutcomePassed
	case reliability.OutcomeCancelled, reliability.OutcomeInterrupted:
		return tuicore.OutcomeCancelled
	case "":
		if tr.Completed {
			return tuicore.OutcomePassed
		}
		return tuicore.OutcomeFailed
	default:
		return tuicore.OutcomeFailed
	}
}

func enrichFeedItem(item *tuicore.FeedItem, entry progress.RunEntry) {
	item.Summary = entry.Summary
	item.Classification = entry.Classification
	if entry.Handoff != nil {
		item.Outcome = tuicore.OutcomeHandoff
		if entry.Handoff.Summary != "" {
			item.Summary = entry.Handoff.Summary
		}
		item.Followups = append([]string(nil), entry.Handoff.Followups...)
	}
}

func parseRFC3339(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

func tui2FirstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.IndexByte(value, '\n'); idx >= 0 {
		return strings.TrimSpace(value[:idx])
	}
	return value
}
