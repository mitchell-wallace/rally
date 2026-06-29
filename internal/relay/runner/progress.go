package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func newProgressRunState(runID, lapID string) *progress.RunState {
	return &progress.RunState{RunID: runID, PinnedLapID: lapID, RecordedLaps: []string{}}
}

func storeLapAttempts(in []progress.LapAttempt) []store.LapAttempt {
	out := make([]store.LapAttempt, 0, len(in))
	for _, attempt := range in {
		out = append(out, store.LapAttempt{LapID: attempt.LapID, Timestamp: attempt.Timestamp})
	}
	return out
}

func mergeStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, value := range append(a, b...) {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func hasDirtyChangesSince(before, after map[string]string) bool {
	for path, afterXY := range after {
		beforeXY, existed := before[path]
		if !existed || beforeXY != afterXY {
			return true
		}
	}
	return false
}

func handoffCreatedLapIDs(handoff *progress.HandoffEntry) []string {
	if handoff == nil || len(handoff.CreatedLapIDs) == 0 {
		return nil
	}
	return append([]string(nil), handoff.CreatedLapIDs...)
}

func recoveryClassificationForRun(task runTask, entry *progress.RunEntry) string {
	if entry == nil || !strings.EqualFold(strings.TrimSpace(task.promptAssignee()), store.RecoveryRouteName) {
		return ""
	}
	value := strings.TrimSpace(entry.Classification)
	switch value {
	case "continue", "discard", "course_correct", "repair_plan", "needs_user":
		return value
	default:
		return ""
	}
}

func progressLapsCompletedForRun(workspaceDir, runID string) []string {
	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.RunID != runID {
			continue
		}
		out = append(out, progressRunEntryLapIDs(entry)...)
	}
	return out
}

func progressRunEntryLapIDs(entry progress.RunEntry) []string {
	var out []string
	switch lapsCompleted := entry.LapsCompleted.(type) {
	case string:
		if lapsCompleted != "" && lapsCompleted != "none" {
			out = append(out, lapsCompleted)
		}
	case []string:
		out = append(out, lapsCompleted...)
	case []interface{}:
		for _, value := range lapsCompleted {
			if s, ok := value.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func pinnedLapCompleteElsewhere(workspaceDir, runID, lapID string, recordedLaps []string) bool {
	if strings.TrimSpace(lapID) == "" {
		return false
	}
	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err == nil {
		for _, entry := range entries {
			if entry.RunID == runID {
				continue
			}
			if stringSliceContains(progressRunEntryLapIDs(entry), lapID) {
				return true
			}
		}
	}
	if stringSliceContains(recordedLaps, lapID) {
		return false
	}
	done, known := lapDoneInLapsState(workspaceDir, lapID)
	return known && done
}

func lapDoneInLapsState(workspaceDir, lapID string) (bool, bool) {
	data, err := os.ReadFile(filepath.Join(workspaceDir, ".laps", "laps.json"))
	if err != nil {
		return false, false
	}
	var state struct {
		Tasks []struct {
			ID          string  `json:"id"`
			IsDone      bool    `json:"isDone"`
			CompletedAt *string `json:"completedAt"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return false, false
	}
	for _, task := range state.Tasks {
		if task.ID != lapID {
			continue
		}
		if task.IsDone {
			return true, true
		}
		if task.CompletedAt != nil && strings.TrimSpace(*task.CompletedAt) != "" {
			return true, true
		}
		return false, true
	}
	return false, false
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func recordedHandoffEntryForRun(workspaceDir, runID string, firstNewEntry int) *progress.HandoffEntry {
	return handoffEntryFromRunEntry(recordedRunEntryForRun(workspaceDir, runID, firstNewEntry))
}

func handoffEntryFromRunEntry(entry *progress.RunEntry) *progress.HandoffEntry {
	if entry == nil {
		return nil
	}
	return entry.Handoff
}

func recordedRunEntryForRun(workspaceDir, runID string, firstNewEntry int) *progress.RunEntry {
	if runID == "" {
		return nil
	}
	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		return nil
	}
	if firstNewEntry < 0 {
		firstNewEntry = 0
	}
	for i := len(entries) - 1; i >= firstNewEntry; i-- {
		entry := entries[i]
		if entry.RunID == runID {
			return &entry
		}
	}
	return nil
}

func tryOutcomeForAttempt(failed, incomplete, interrupted, hasDurableHandoff bool) reliability.TryOutcome {
	if interrupted {
		return reliability.OutcomeInterrupted
	}
	if !failed {
		if hasDurableHandoff {
			return reliability.OutcomeHandoffRequested
		}
		return reliability.OutcomeCompleted
	}
	if incomplete {
		return reliability.OutcomeIncomplete
	}
	return reliability.OutcomeFailed
}

func validatePinnedLap(pinnedLapID string, recordedLaps []string) (string, bool) {
	if pinnedLapID == "" || len(recordedLaps) == 0 {
		return "", false
	}
	unique := mergeStrings(nil, recordedLaps)
	if len(unique) > 1 {
		return "multi_lap_consumed", true
	}
	if unique[0] != pinnedLapID {
		return "wrong_lap_consumed", true
	}
	return "", false
}

// detectLapsMarkerInText returns "laps done" / "laps handoff" when the agent's
// summary contains it on its own line or as a leading marker — a strong signal
// the model emitted the command as text instead of invoking the shell tool.
func detectLapsMarkerInText(summary string) string {
	if summary == "" {
		return ""
	}
	lower := strings.ToLower(summary)
	// Check leading line and any line that begins with the marker.
	for _, raw := range strings.Split(lower, "\n") {
		line := strings.TrimSpace(raw)
		if line == "laps done" || strings.HasPrefix(line, "laps done\n") || strings.HasPrefix(line, "laps done ") {
			return "laps done"
		}
		if line == "laps handoff" || strings.HasPrefix(line, "laps handoff\n") || strings.HasPrefix(line, "laps handoff ") {
			return "laps handoff"
		}
	}
	return ""
}

// maybeWriteStubAndClearState writes a stub progress entry when the agent left
// run-state on disk (i.e. it never finalized via `laps done`/`laps handoff`).
// It returns wroteUnfinalized=true whenever it had to synthesize that stub so
// the caller can surface the recognized "agent exited without finalizing"
// failure to telemetry even if the model produced a partial summary first.
func (r *Runner) maybeWriteStubAndClearState(lastOutput string) (bool, error) {
	rs, err := progress.LoadRunState(r.cfg.WorkspaceDir)
	if err != nil {
		return false, err
	}
	// If no run-state file exists, LoadRunState returns a fresh empty state.
	// We only write a stub if the file actually existed on disk.
	if _, err := os.Stat(progress.RunStatePath(r.cfg.WorkspaceDir)); os.IsNotExist(err) {
		return false, nil
	}

	var lapsCompleted interface{}
	if r.cfg.LapsEnabled {
		if len(rs.RecordedLaps) > 0 {
			lapsCompleted = rs.RecordedLaps
		} else {
			lapsCompleted = "none"
		}
	}

	summary := lastOutput
	if summary == "" {
		summary = "(agent exited without finalizing)"
	}

	entry := progress.RunEntry{
		RunID:         rs.RunID,
		Summary:       summary,
		LapsCompleted: lapsCompleted,
	}
	_ = progress.AppendRunEntry(r.cfg.WorkspaceDir, entry)
	_ = progress.ClearRunState(r.cfg.WorkspaceDir)
	return true, nil
}
