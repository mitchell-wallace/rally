package runner

import (
	"os"
	"strings"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
)

const (
	finalSnippetFallbackRuneLimit = 1000
	finalSnippetTailMarker        = "... [tail truncated] ..."
	noFinalSnippetIndicator       = "(agent produced no final summary)"
)

// normalizeFinalSnippet selects the one summary value used by retry prompts,
// try records, and synthesized summary entries. An explicit progress wrapup is
// authoritative; executor summaries are the next-best structured source.
func (r *Runner) normalizeFinalSnippet(runID, tryLogPath string, summaryEntryCountBefore int, result *harnessapi.TryResult, execErr error) string {
	if summary := recordedWrapupSummaryForRun(r.cfg.WorkspaceDir, runID, summaryEntryCountBefore); summary != "" {
		return summary
	}
	if result != nil && strings.TrimSpace(result.Summary) != "" {
		return result.Summary
	}
	if tail := boundedFinalSnippetTail(readTryLog(tryLogPath), finalSnippetFallbackRuneLimit); tail != "" {
		return tail
	}
	if execErr != nil {
		return finalSnippetErrorIndicator(execErr)
	}
	return noFinalSnippetIndicator
}

func progressSummaryEntryCount(workspaceDir string) int {
	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		return 0
	}
	return len(entries)
}

func recordedWrapupSummaryForRun(workspaceDir, runID string, firstNewEntry int) string {
	if runID == "" {
		return ""
	}
	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		return ""
	}
	if firstNewEntry < 0 {
		firstNewEntry = 0
	}
	for i := len(entries) - 1; i >= firstNewEntry; i-- {
		entry := entries[i]
		if entry.RunID != runID {
			continue
		}
		if strings.TrimSpace(entry.Summary) != "" {
			return entry.Summary
		}
		if entry.Handoff != nil && strings.TrimSpace(entry.Handoff.Summary) != "" {
			return entry.Handoff.Summary
		}
	}
	return ""
}

func readTryLog(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func boundedFinalSnippetTail(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if text == "" || maxRunes <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}

	marker := []rune(finalSnippetTailMarker)
	if maxRunes <= len(marker) {
		return string(marker[:maxRunes])
	}
	tailSize := maxRunes - len(marker)
	return string(marker) + string(runes[len(runes)-tailSize:])
}

func finalSnippetErrorIndicator(err error) string {
	const prefix = "harness error: "
	detail := strings.Join(strings.Fields(err.Error()), " ")
	if detail == "" {
		return strings.TrimSpace(prefix)
	}
	return prefix + boundedFinalSnippetTail(detail, finalSnippetFallbackRuneLimit-len([]rune(prefix)))
}

func readLastNLines(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
