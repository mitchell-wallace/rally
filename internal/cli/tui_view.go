package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func synthesizeRelayEvents(workspaceDir string, relayNum string) ([]runtimeevent.Event, int, error) {
	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		return nil, 0, fmt.Errorf("load store: %w", err)
	}

	relay, err := resolveRelayForView(s, relayNum)
	if err != nil {
		return nil, 0, err
	}

	tries := s.AllTries()
	var relayTries []store.TryRecord
	for _, tr := range tries {
		if tr.RelayID == relay.ID {
			relayTries = append(relayTries, tr)
		}
	}
	sort.SliceStable(relayTries, func(i, j int) bool {
		return relayTries[i].ID < relayTries[j].ID
	})

	events := []runtimeevent.Event{
		runtimeevent.RelayStarted{
			RelayID:          relay.ID,
			TargetIterations: relay.TargetIterations,
			AgentMix:         relay.AgentMix,
		},
	}

	lapTitles := loadLapTitles(workspaceDir)
	runs := groupTriesByRun(relayTries)
	for runIndex, run := range runs {
		if len(run) == 0 {
			continue
		}
		first := run[0]
		events = append(events, runtimeevent.RunHeaderReady{
			RunIndex:     runIndex,
			TotalRuns:    relay.TargetIterations,
			AgentName:    first.AgentType,
			Attempt:      first.AttemptNumber,
			StartTime:    parseTime(first.StartedAt),
			IsLapsBacked: first.LapID != "",
			LapTitle:     lapTitle(first, lapTitles),
			LapsStarted:  lapsStarted(first, runIndex),
			Model:        "",
			RoleLabel:    roleLabel(first),
		})

		maxAttempts := maxAttemptNumber(run)
		runDuration := sumRunDuration(run)
		for i, tr := range run {
			footer := footerDataFromTry(tr, maxAttempts)
			final := i == len(run)-1
			if !final {
				footer.Interim = true
				events = append(events, runtimeevent.RetryFooterUpdated{FooterData: footer})
				continue
			}
			footer.Duration = runDuration
			switch {
			case isCancelledTry(tr):
				footer.Cancelled = true
				footer.Passed = false
				events = append(events, runtimeevent.AttemptCancelled{FooterData: footer})
			case tr.HandoffOnly:
				events = append(events, runtimeevent.HandoffAttemptFinished{FooterData: footer})
			default:
				events = append(events, runtimeevent.AttemptFinished{FooterData: footer})
			}
		}
	}

	passed, failed, cancelled := tallySynthesizedRuns(runs)
	totalRuns := passed + failed + cancelled
	if relay.CompletedIterations > totalRuns {
		totalRuns = relay.CompletedIterations
	}
	totalDuration := relayDuration(relay)
	if totalDuration == 0 {
		for _, run := range runs {
			totalDuration += sumRunDuration(run)
		}
	}
	if totalRuns > 0 {
		events = append(events, runtimeevent.RelaySummaryReady{
			TotalRuns:     totalRuns,
			Passed:        passed,
			Failed:        failed,
			Cancelled:     cancelled,
			TotalDuration: totalDuration,
		})
	}
	events = append(events, runtimeevent.RelayCompleted{
		RelayID:       relay.ID,
		TotalRuns:     totalRuns,
		Passed:        passed,
		Failed:        failed,
		Cancelled:     cancelled,
		TotalDuration: totalDuration,
	})
	return events, relay.ID, nil
}

func resolveRelayForView(s *store.Store, relayNum string) (store.RelayRecord, error) {
	relayNum = strings.TrimSpace(relayNum)
	if relayNum == "" || strings.EqualFold(relayNum, "latest") {
		relays := s.RecentRelays(1)
		if len(relays) == 0 {
			return store.RelayRecord{}, fmt.Errorf("no relays found in .rally store")
		}
		return relays[0], nil
	}
	id, err := strconv.Atoi(relayNum)
	if err != nil || id <= 0 {
		return store.RelayRecord{}, fmt.Errorf("--view must be latest or a positive relay ID")
	}
	relay := s.GetRelay(id)
	if relay == nil {
		return store.RelayRecord{}, fmt.Errorf("relay #%d not found in .rally store", id)
	}
	return *relay, nil
}

func groupTriesByRun(tries []store.TryRecord) [][]store.TryRecord {
	byRun := make(map[int][]store.TryRecord)
	order := make([]int, 0)
	seen := make(map[int]bool)
	for _, tr := range tries {
		if !seen[tr.OutingID] {
			seen[tr.OutingID] = true
			order = append(order, tr.OutingID)
		}
		byRun[tr.OutingID] = append(byRun[tr.OutingID], tr)
	}
	out := make([][]store.TryRecord, 0, len(order))
	for _, runID := range order {
		run := byRun[runID]
		sort.SliceStable(run, func(i, j int) bool {
			return run[i].ID < run[j].ID
		})
		out = append(out, run)
	}
	return out
}

func footerDataFromTry(tr store.TryRecord, maxAttempts int) runtimeevent.FooterData {
	return runtimeevent.FooterData{
		Passed:             tr.Completed || tr.Outcome.IsSuccess(),
		Cancelled:          isCancelledTry(tr),
		Duration:           tryDuration(tr),
		FilesChanged:       len(tr.FilesChanged),
		CommitHash:         shortHash(tr.CommitHash),
		CommitTitle:        "",
		FailReason:         tr.FailReason,
		CancellationSource: tr.CancellationSource,
		Attempt:            tr.AttemptNumber,
		MaxAttempts:        maxAttempts,
	}
}

func tallySynthesizedRuns(runs [][]store.TryRecord) (passCount, failCount, cancelledCount int) {
	for _, run := range runs {
		if len(run) == 0 {
			continue
		}
		final := run[len(run)-1]
		switch {
		case final.Completed || final.Outcome.IsSuccess():
			passCount++
		case isCancelledTry(final):
			cancelledCount++
		default:
			failCount++
		}
	}
	return passCount, failCount, cancelledCount
}

func maxAttemptNumber(run []store.TryRecord) int {
	max := 1
	for _, tr := range run {
		if tr.AttemptNumber > max {
			max = tr.AttemptNumber
		}
	}
	return max
}

func sumRunDuration(run []store.TryRecord) time.Duration {
	var total time.Duration
	for _, tr := range run {
		total += tryDuration(tr)
	}
	return total
}

func tryDuration(tr store.TryRecord) time.Duration {
	if tr.RuntimeMs > 0 {
		return time.Duration(tr.RuntimeMs) * time.Millisecond
	}
	startedAt := parseTime(tr.StartedAt)
	endedAt := parseTime(tr.EndedAt)
	if !startedAt.IsZero() && !endedAt.IsZero() && endedAt.After(startedAt) {
		return endedAt.Sub(startedAt)
	}
	return 0
}

func relayDuration(relay store.RelayRecord) time.Duration {
	startedAt := parseTime(relay.StartedAt)
	endedAt := parseTime(relay.EndedAt)
	if !startedAt.IsZero() && !endedAt.IsZero() && endedAt.After(startedAt) {
		return endedAt.Sub(startedAt)
	}
	return 0
}

func parseTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

func isCancelledTry(tr store.TryRecord) bool {
	return tr.CancellationSource != "" || tr.Outcome == reliability.OutcomeCancelled
}

func shortHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

func roleLabel(tr store.TryRecord) string {
	if tr.LapAssignee != "" {
		return tr.LapAssignee
	}
	return tr.ResolvedRoute
}

func lapsStarted(tr store.TryRecord, runIndex int) int {
	if tr.LapID == "" {
		return 0
	}
	return runIndex + 1
}

func lapTitle(tr store.TryRecord, lapTitles map[string]string) string {
	if tr.LapID != "" {
		if title := strings.TrimSpace(lapTitles[tr.LapID]); title != "" {
			return title
		}
		if title := firstLine(tr.Summary); title != "" {
			return title
		}
		return tr.LapID
	}
	return firstLine(tr.Summary)
}

func firstLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func loadLapTitles(workspaceDir string) map[string]string {
	data, err := os.ReadFile(filepath.Join(workspaceDir, ".laps", "laps.json"))
	if err != nil {
		return nil
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	titles := make(map[string]string)
	collectLapTitles(payload, titles)
	return titles
}

func collectLapTitles(value any, titles map[string]string) {
	switch v := value.(type) {
	case map[string]any:
		id, _ := v["id"].(string)
		title, _ := v["title"].(string)
		if id != "" && title != "" {
			titles[id] = title
		}
		for _, child := range v {
			collectLapTitles(child, titles)
		}
	case []any:
		for _, child := range v {
			collectLapTitles(child, titles)
		}
	}
}
