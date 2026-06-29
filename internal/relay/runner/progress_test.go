package runner

import (
	"context"
	"os"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestDetectLapsMarkerInText(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		want    string
	}{
		{"empty", "", ""},
		{"leading laps done", "laps done\n\n**What landed**\n- foo", "laps done"},
		{"line laps handoff", "Some intro\nlaps handoff\nrest", "laps handoff"},
		{"laps done as space-separated word in prose", "the agent says laps done at the end", ""},
		{"trailing laps done", "All work complete.\n\nlaps done", "laps done"},
		{"mixed case", "LAPS DONE\nfoo", "laps done"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectLapsMarkerInText(tc.summary)
			if got != tc.want {
				t.Errorf("detectLapsMarkerInText(%q) = %q, want %q", tc.summary, got, tc.want)
			}
		})
	}
}

var footerAnsiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripFooterAnsi(s string) string { return footerAnsiRe.ReplaceAllString(s, "") }

func TestStubEntryOnIncompleteRun(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) { return laps.Lap{Title: "test"}, nil }
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "agent stopped early"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Millisecond,
		HourlyRetriesBeforeFreeze: 0,
		NowFunc:                   time.Now,
	}

	_ = r.Run(context.Background())

	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		t.Fatalf("LoadSummaryEntries error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one stub entry in summary.jsonl")
	}
	found := false
	for _, entry := range entries {
		if entry.Summary == "agent stopped early" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a stub entry with summary 'agent stopped early', got %v", entries)
	}

	if _, err := os.Stat(progress.RunStatePath(workspaceDir)); !os.IsNotExist(err) {
		t.Fatal("expected run-state.json to be cleared")
	}
}

func TestProgressLapsCompletedForRunReadsSummaryJSONL(t *testing.T) {
	workspaceDir := t.TempDir()
	if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
		RunID:         "run-1",
		Summary:       "first",
		LapsCompleted: []string{"lap-a", "lap-b"},
	}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}
	if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
		RunID:         "run-2",
		Summary:       "other",
		LapsCompleted: []string{"lap-c"},
	}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}
	if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
		RunID:         "run-1",
		Summary:       "second",
		LapsCompleted: "lap-d",
	}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}

	got := progressLapsCompletedForRun(workspaceDir, "run-1")
	want := []string{"lap-a", "lap-b", "lap-d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("progressLapsCompletedForRun() = %v, want %v", got, want)
	}
}

func TestLapPinValidation_NormalPassThrough(t *testing.T) {
	reason, mismatch := validatePinnedLap("lap-1", []string{"lap-1"})
	if mismatch {
		t.Fatalf("expected no mismatch for normal pass-through, got reason=%q", reason)
	}
}

func TestLapPinValidation_WrongLapConsumed(t *testing.T) {
	reason, mismatch := validatePinnedLap("lap-1", []string{"lap-9"})
	if !mismatch {
		t.Fatal("expected mismatch when consumed lap differs from pinned")
	}
	if reason != "wrong_lap_consumed" {
		t.Fatalf("reason = %q, want %q", reason, "wrong_lap_consumed")
	}
}

func TestLapPinValidation_MultiLapConsumed(t *testing.T) {
	reason, mismatch := validatePinnedLap("lap-1", []string{"lap-1", "lap-2"})
	if !mismatch {
		t.Fatal("expected mismatch when multiple laps consumed")
	}
	if reason != "multi_lap_consumed" {
		t.Fatalf("reason = %q, want %q", reason, "multi_lap_consumed")
	}
}

func TestLapPinValidation_EmptyPinnedID(t *testing.T) {
	reason, mismatch := validatePinnedLap("", []string{"lap-1"})
	if mismatch {
		t.Fatalf("expected no mismatch when pinned lap ID is empty, got reason=%q", reason)
	}
}

func TestLapPinValidation_NoRecordedLaps(t *testing.T) {
	reason, mismatch := validatePinnedLap("lap-1", nil)
	if mismatch {
		t.Fatalf("expected no mismatch when no laps recorded, got reason=%q", reason)
	}
	reason, mismatch = validatePinnedLap("lap-1", []string{})
	if mismatch {
		t.Fatalf("expected no mismatch when empty laps recorded, got reason=%q", reason)
	}
}

func TestLapPinValidation_DuplicateSameLap(t *testing.T) {
	reason, mismatch := validatePinnedLap("lap-1", []string{"lap-1", "lap-1"})
	if mismatch {
		t.Fatalf("expected no mismatch for duplicate same lap, got reason=%q", reason)
	}
}
