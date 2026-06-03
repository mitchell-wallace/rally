package progress

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/textutil"
)

func TestSummaryPath(t *testing.T) {
	got := SummaryPath("/tmp/ws")
	want := store.SummaryPath("/tmp/ws")
	if got != want {
		t.Errorf("SummaryPath() = %q, want %q", got, want)
	}
}

func TestProgressPathReturnsSummaryPath(t *testing.T) {
	got := ProgressPath("/tmp/ws")
	want := store.SummaryPath("/tmp/ws")
	if got != want {
		t.Errorf("ProgressPath() = %q, want %q", got, want)
	}
}

func TestLoadSummaryEntriesMissingFile(t *testing.T) {
	tmp := t.TempDir()
	entries, err := LoadSummaryEntries(tmp)
	if err != nil {
		t.Fatalf("LoadSummaryEntries unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %v, want empty", entries)
	}
}

func TestAppendRunEntryWritesParseableJSONL(t *testing.T) {
	tmp := t.TempDir()
	if err := AppendRunEntry(tmp, RunEntry{RunID: "run-1", Summary: "s1"}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}
	if err := AppendRunEntry(tmp, RunEntry{
		RunID:         "run-2",
		Summary:       "s2",
		LapsCompleted: []string{"lap-a", "lap-b"},
		Handoff: &HandoffEntry{
			Summary:       "blocked",
			Followups:     []string{"fix auth"},
			CreatedLapIDs: []string{"lap-new"},
		},
	}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}

	data, err := os.ReadFile(SummaryPath(tmp))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("line count = %d, want 2; data:\n%s", len(lines), string(data))
	}
	for i, line := range lines {
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("line %d is not parseable JSON: %v", i+1, err)
		}
		if _, err := time.Parse(time.RFC3339, raw["updated_at"].(string)); err != nil {
			t.Fatalf("line %d updated_at is not RFC3339: %v", i+1, err)
		}
	}

	entries, err := LoadSummaryEntries(tmp)
	if err != nil {
		t.Fatalf("LoadSummaryEntries error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].RunID != "run-1" || entries[1].RunID != "run-2" {
		t.Errorf("entry run ids = %q, %q; want run-1, run-2", entries[0].RunID, entries[1].RunID)
	}
	laps, ok := entries[1].LapsCompleted.([]interface{})
	if !ok || len(laps) != 2 || laps[0] != "lap-a" || laps[1] != "lap-b" {
		t.Errorf("LapsCompleted = %#v, want [lap-a lap-b]", entries[1].LapsCompleted)
	}
	if entries[1].Handoff == nil {
		t.Fatal("expected handoff")
	}
	if entries[1].Handoff.CreatedLapIDs[0] != "lap-new" {
		t.Errorf("CreatedLapIDs = %v, want [lap-new]", entries[1].Handoff.CreatedLapIDs)
	}
}

func TestAppendRunEntryCapsFinalSnippetFields(t *testing.T) {
	tmp := t.TempDir()

	longRunSummary := strings.Repeat("始", store.FinalSnippetRuneLimit) + "middle" + strings.Repeat("終", store.FinalSnippetRuneLimit)
	longHandoffSummary := strings.Repeat("前", store.FinalSnippetRuneLimit) + "middle" + strings.Repeat("後", store.FinalSnippetRuneLimit)
	longFollowup := strings.Repeat("次", store.FinalSnippetRuneLimit) + "middle" + strings.Repeat("完", store.FinalSnippetRuneLimit)
	smallRunSummary := "short run summary\nkept verbatim"
	smallHandoffSummary := "short handoff summary"
	smallFollowup := "short followup"

	if err := AppendRunEntry(tmp, RunEntry{
		RunID:   "run-long",
		Summary: longRunSummary,
		Handoff: &HandoffEntry{
			Summary:   longHandoffSummary,
			Followups: []string{longFollowup},
		},
	}); err != nil {
		t.Fatalf("AppendRunEntry oversized record: %v", err)
	}
	if err := AppendRunEntry(tmp, RunEntry{
		RunID:   "run-small",
		Summary: smallRunSummary,
		Handoff: &HandoffEntry{
			Summary:   smallHandoffSummary,
			Followups: []string{smallFollowup},
		},
	}); err != nil {
		t.Fatalf("AppendRunEntry small record: %v", err)
	}

	entries, err := LoadSummaryEntries(tmp)
	if err != nil {
		t.Fatalf("LoadSummaryEntries error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entry count = %d, want 2", len(entries))
	}

	assertCappedSummaryText(t, entries[0].Summary, "始", "終")
	if entries[0].Handoff == nil {
		t.Fatal("oversized entry handoff is nil")
	}
	assertCappedSummaryText(t, entries[0].Handoff.Summary, "前", "後")
	assertCappedSummaryText(t, entries[0].Handoff.Followups[0], "次", "完")

	if entries[1].Summary != smallRunSummary {
		t.Fatalf("small run summary = %q, want verbatim %q", entries[1].Summary, smallRunSummary)
	}
	if entries[1].Handoff == nil {
		t.Fatal("small entry handoff is nil")
	}
	if entries[1].Handoff.Summary != smallHandoffSummary {
		t.Fatalf("small handoff summary = %q, want verbatim %q", entries[1].Handoff.Summary, smallHandoffSummary)
	}
	if entries[1].Handoff.Followups[0] != smallFollowup {
		t.Fatalf("small followup = %q, want verbatim %q", entries[1].Handoff.Followups[0], smallFollowup)
	}
}

func assertCappedSummaryText(t *testing.T, got, wantHead, wantTail string) {
	t.Helper()

	if !utf8.ValidString(got) {
		t.Fatalf("capped text is not valid UTF-8: %q", got)
	}
	if gotRunes := len([]rune(got)); gotRunes != store.FinalSnippetRuneLimit {
		t.Fatalf("capped text rune length = %d, want %d", gotRunes, store.FinalSnippetRuneLimit)
	}
	if !strings.Contains(got, textutil.HeadTailTruncationMarker) {
		t.Fatalf("capped text is missing marker %q", textutil.HeadTailTruncationMarker)
	}
	if !strings.HasPrefix(got, wantHead) {
		t.Fatalf("capped text does not preserve head %q", wantHead)
	}
	if !strings.HasSuffix(got, wantTail) {
		t.Fatalf("capped text does not preserve tail %q", wantTail)
	}
}

func TestAppendRunEntryDoesNotWriteProgressYAML(t *testing.T) {
	tmp := t.TempDir()
	progressPath := filepath.Join(tmp, ".rally", "progress.yaml")
	if err := AppendRunEntry(tmp, RunEntry{RunID: "run-1", Summary: "s"}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}
	if _, err := os.Stat(SummaryPath(tmp)); err != nil {
		t.Fatalf("expected summary.jsonl to exist: %v", err)
	}
	if _, err := os.Stat(progressPath); !os.IsNotExist(err) {
		t.Fatalf("expected progress.yaml not to exist, stat err = %v", err)
	}
}

func TestAppendRunEntryLeavesExistingProgressYAMLUntouched(t *testing.T) {
	tmp := t.TempDir()
	progressPath := filepath.Join(tmp, ".rally", "progress.yaml")
	if err := os.MkdirAll(filepath.Dir(progressPath), 0o755); err != nil {
		t.Fatalf("mkdir .rally: %v", err)
	}
	legacy := []byte("legacy: true\n")
	if err := os.WriteFile(progressPath, legacy, 0o644); err != nil {
		t.Fatalf("write legacy progress: %v", err)
	}

	if err := AppendRunEntry(tmp, RunEntry{RunID: "run-1", Summary: "s"}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}

	got, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatalf("read legacy progress: %v", err)
	}
	if string(got) != string(legacy) {
		t.Errorf("legacy progress changed to %q, want %q", string(got), string(legacy))
	}
}

func TestAppendRunEntryDoesNotTrimHistory(t *testing.T) {
	tmp := t.TempDir()
	for i := 1; i <= 60; i++ {
		if err := AppendRunEntry(tmp, RunEntry{RunID: "run"}); err != nil {
			t.Fatalf("AppendRunEntry error: %v", err)
		}
	}

	entries, err := LoadSummaryEntries(tmp)
	if err != nil {
		t.Fatalf("LoadSummaryEntries error: %v", err)
	}
	if len(entries) != 60 {
		t.Fatalf("len(entries) = %d, want 60", len(entries))
	}
}

func TestSummaryJSONShapeOmitEmptyFields(t *testing.T) {
	tmp := t.TempDir()
	if err := AppendRunEntry(tmp, RunEntry{RunID: "run-1", Summary: "summary"}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}

	data, err := os.ReadFile(SummaryPath(tmp))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal summary line: %v", err)
	}
	if _, ok := raw["laps_completed"]; ok {
		t.Fatalf("expected laps_completed omitted, got %s", raw["laps_completed"])
	}
	if _, ok := raw["handoff"]; ok {
		t.Fatalf("expected handoff omitted, got %s", raw["handoff"])
	}
}

func TestSummaryJSONShapeHandoffArrays(t *testing.T) {
	tmp := t.TempDir()
	if err := AppendRunEntry(tmp, RunEntry{
		RunID:   "run-1",
		Summary: "summary",
		Handoff: &HandoffEntry{
			Summary: "blocked",
		},
	}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}

	entries, err := LoadSummaryEntries(tmp)
	if err != nil {
		t.Fatalf("LoadSummaryEntries error: %v", err)
	}
	if entries[0].Handoff == nil {
		t.Fatal("expected handoff")
	}
	if entries[0].Handoff.Followups == nil {
		t.Fatal("expected Followups to round-trip as an empty slice")
	}
	if entries[0].Handoff.CreatedLapIDs == nil {
		t.Fatal("expected CreatedLapIDs to round-trip as an empty slice")
	}
}
