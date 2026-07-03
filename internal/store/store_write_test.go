package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/reliability"
)

func TestTryRecordPersistsOutcomeAndHandoffOnly(t *testing.T) {
	rallyDir, s := setupTempStore(t)

	if err := s.AppendTry(TryRecord{
		ID:                     1,
		RunID:                  1,
		AgentType:              "codex",
		Completed:              true,
		Outcome:                reliability.OutcomeHandoffRequested,
		HandoffOnly:            true,
		ResolvedRoute:          "senior",
		DirtyHandoff:           true,
		RecoveryClassification: "repair_plan",
		HandoffCreatedLapIDs:   []string{"lap-followup"},
		Category:               "",
	}); err != nil {
		t.Fatalf("AppendTry: %v", err)
	}

	read, err := readJSONL[TryRecord](filepath.Join(rallyDir, "state", "tries.jsonl"))
	if err != nil {
		t.Fatalf("read tries: %v", err)
	}
	if len(read) != 1 {
		t.Fatalf("tries = %d, want 1", len(read))
	}
	if read[0].Outcome != reliability.OutcomeHandoffRequested {
		t.Fatalf("Outcome = %q, want %q", read[0].Outcome, reliability.OutcomeHandoffRequested)
	}
	if !read[0].HandoffOnly {
		t.Fatal("HandoffOnly = false, want true")
	}
	if read[0].ResolvedRoute != "senior" {
		t.Fatalf("ResolvedRoute = %q, want senior", read[0].ResolvedRoute)
	}
	if !read[0].DirtyHandoff {
		t.Fatal("DirtyHandoff = false, want true")
	}
	if read[0].RecoveryClassification != "repair_plan" {
		t.Fatalf("RecoveryClassification = %q, want repair_plan", read[0].RecoveryClassification)
	}
	if len(read[0].HandoffCreatedLapIDs) != 1 || read[0].HandoffCreatedLapIDs[0] != "lap-followup" {
		t.Fatalf("HandoffCreatedLapIDs = %v, want [lap-followup]", read[0].HandoffCreatedLapIDs)
	}
	if read[0].Category != "" {
		t.Fatalf("Category = %q, want empty for non-failed outcome", read[0].Category)
	}
}

func TestTryRecordCancelledOutcomeAndSource(t *testing.T) {
	sources := []string{"skip", "graceful_stop", "quit_now"}
	for _, src := range sources {
		t.Run(src, func(t *testing.T) {
			rallyDir, s := setupTempStore(t)

			if err := s.AppendTry(TryRecord{
				ID:                 1,
				RunID:              1,
				AgentType:          "claude",
				Completed:          false,
				Outcome:            reliability.OutcomeCancelled,
				CancellationSource: src,
				Summary:            "operator cancelled",
			}); err != nil {
				t.Fatalf("AppendTry: %v", err)
			}

			read, err := readJSONL[TryRecord](filepath.Join(rallyDir, "state", "tries.jsonl"))
			if err != nil {
				t.Fatalf("read tries: %v", err)
			}
			if len(read) != 1 {
				t.Fatalf("tries = %d, want 1", len(read))
			}
			if read[0].Outcome != reliability.OutcomeCancelled {
				t.Fatalf("Outcome = %q, want %q", read[0].Outcome, reliability.OutcomeCancelled)
			}
			if read[0].CancellationSource != src {
				t.Fatalf("CancellationSource = %q, want %q", read[0].CancellationSource, src)
			}
			if read[0].Completed {
				t.Fatal("Completed = true, want false for cancelled outcome")
			}
			if read[0].Category != "" {
				t.Fatalf("Category = %q, want empty for cancelled outcome", read[0].Category)
			}
		})
	}
}

func TestAppendTryCapsFinalSnippetFields(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	longSummary := strings.Repeat("界", FinalSnippetRuneLimit) + "middle" + strings.Repeat("終", FinalSnippetRuneLimit)
	longRemainingWork := strings.Repeat("前", FinalSnippetRuneLimit) + "middle" + strings.Repeat("後", FinalSnippetRuneLimit)
	smallSummary := "short summary\nkept verbatim"
	smallRemainingWork := "small remaining work"

	if err := store.AppendTry(TryRecord{
		ID:            1,
		Summary:       longSummary,
		RemainingWork: longRemainingWork,
	}); err != nil {
		t.Fatalf("AppendTry oversized record: %v", err)
	}
	if err := store.AppendTry(TryRecord{
		ID:            2,
		Summary:       smallSummary,
		RemainingWork: smallRemainingWork,
	}); err != nil {
		t.Fatalf("AppendTry small record: %v", err)
	}

	stored, err := readJSONL[TryRecord](filepath.Join(rallyDir, "state", "tries.jsonl"))
	if err != nil {
		t.Fatalf("read tries.jsonl: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored try count = %d, want 2", len(stored))
	}

	assertCappedFinalSnippet(t, stored[0].Summary, "界", "終")
	assertCappedFinalSnippet(t, stored[0].RemainingWork, "前", "後")
	if stored[1].Summary != smallSummary {
		t.Fatalf("small summary = %q, want verbatim %q", stored[1].Summary, smallSummary)
	}
	if stored[1].RemainingWork != smallRemainingWork {
		t.Fatalf("small remaining work = %q, want verbatim %q", stored[1].RemainingWork, smallRemainingWork)
	}

	cached := store.AllTries()
	if cached[0].Summary != stored[0].Summary || cached[0].RemainingWork != stored[0].RemainingWork {
		t.Fatal("cached try fields do not match the capped persisted values")
	}
}

func TestTryCommitHistory(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Single commit -> populates CommitHistory with one element
	try1 := TryRecord{ID: 1, CommitHash: "singlehash"}
	if err := s.AppendTry(try1); err != nil {
		t.Fatal(err)
	}

	// 2. Multiple commits -> backward compat sets CommitHash to last element
	try2 := TryRecord{ID: 2, CommitHistory: []string{"hash1", "hash2", "hash3"}}
	if err := s.AppendTry(try2); err != nil {
		t.Fatal(err)
	}

	// Verify
	tries := s.RecentTries(10)
	if len(tries) != 2 {
		t.Fatalf("expected 2 tries, got %d", len(tries))
	}

	// try1 (returned first because RecentTries does not reverse)
	t1 := tries[0]
	if t1.CommitHash != "singlehash" {
		t.Errorf("try1 CommitHash = %q, want singlehash", t1.CommitHash)
	}
	if len(t1.CommitHistory) != 1 || t1.CommitHistory[0] != "singlehash" {
		t.Errorf("try1 CommitHistory = %v, want [singlehash]", t1.CommitHistory)
	}

	// try2 (returned second)
	t2 := tries[1]
	if t2.CommitHash != "hash3" {
		t.Errorf("try2 CommitHash = %q, want hash3", t2.CommitHash)
	}
	if len(t2.CommitHistory) != 3 || t2.CommitHistory[0] != "hash1" {
		t.Errorf("try2 CommitHistory = %v", t2.CommitHistory)
	}
}
