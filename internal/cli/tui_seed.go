package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func loadTuiFeedSeed(workspaceDir string) ([]tuicore.FeedItem, error) {
	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		return nil, err
	}
	tries := s.RecentTries(20)
	summaries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		return nil, err
	}
	summaryByOutingID := make(map[string]progress.OutingEntry, len(summaries))
	for _, entry := range summaries {
		if strings.TrimSpace(entry.OutingID) == "" {
			continue
		}
		summaryByOutingID[entry.OutingID] = entry
	}

	items := make([]tuicore.FeedItem, 0, len(tries))
	for _, tr := range tries {
		if !isFinishedTry(tr) {
			continue
		}
		item := tryRecordToFeedItem(tr)
		if entry, ok := summaryByOutingID[strconv.Itoa(tr.OutingID)]; ok {
			enrichFeedItem(&item, entry)
		} else if tr.LapID != "" {
			if entry, ok := summaryByOutingID[tr.LapID]; ok {
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
	title := feedTitleFromTry(tr)
	if title == "" {
		title = fmt.Sprintf("outing %d", tr.OutingID)
	}
	return tuicore.FeedItem{
		OutingIndex:    tr.OutingID,
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

func enrichFeedItem(item *tuicore.FeedItem, entry progress.OutingEntry) {
	item.Summary = entry.Summary
	item.Classification = entry.Classification
	if title := tui2FirstLine(entry.Summary); title != "" {
		item.Title = title
	}
	if entry.Handoff != nil {
		item.Outcome = tuicore.OutcomeHandoff
		if entry.Handoff.Summary != "" {
			item.Summary = entry.Handoff.Summary
			if title := tui2FirstLine(entry.Handoff.Summary); title != "" {
				item.Title = title
			}
		}
		item.Followups = append([]string(nil), entry.Handoff.Followups...)
	}
}

func feedTitleFromTry(tr store.TryRecord) string {
	if title := tui2FirstLine(tr.Summary); title != "" {
		return title
	}
	if tr.LapID != "" {
		return tr.LapID
	}
	return ""
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
