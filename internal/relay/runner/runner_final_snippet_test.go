package runner

import (
	"context"
	"errors"
	"github.com/mitchell-wallace/rally/internal/agent"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestRunOneFinalSnippetUsesRecordedWrapupSummary(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newTestStore(t, rallyDir)

	const (
		runID           = "relay-1-run-1"
		wrapupSummary   = "golden wrapup summary"
		executorSummary = "executor final summary"
	)
	attempt := 0
	retryPreviousSummary := ""
	exec := &funcExecutor{
		fn: func(_ context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			if attempt == 1 {
				if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
					RunID:         runID,
					Summary:       wrapupSummary,
					LapsCompleted: []string{"lap-1"},
				}); err != nil {
					return nil, err
				}
				if err := progress.ClearRunState(workspaceDir); err != nil {
					return nil, err
				}
			} else {
				retryPreviousSummary = opts.PreviousSummary
			}
			return &harnessapi.TryResult{Completed: false, Summary: executorSummary}, nil
		},
	}
	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RetryBudget:  2,
		LapsEnabled:  true,
	}, map[string]harnessapi.Executor{"claude": exec})

	_, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "claude"},
		runTask{Name: "wrapup task", LapID: "lap-1", IsLapsBacked: true},
		nil,
		nil,
		false,
		false,
		nil,
		nil,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}

	if retryPreviousSummary != wrapupSummary {
		t.Fatalf("retry previous summary = %q, want wrapup summary %q", retryPreviousSummary, wrapupSummary)
	}
	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("tries = %d, want 2", len(tries))
	}
	for i, tr := range tries {
		if tr.Summary != wrapupSummary {
			t.Fatalf("try %d summary = %q, want wrapup summary %q", i+1, tr.Summary, wrapupSummary)
		}
	}

	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		t.Fatalf("LoadSummaryEntries error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("summary entries = %d, want 2", len(entries))
	}
	for i, entry := range entries {
		if entry.Summary != wrapupSummary {
			t.Fatalf("summary.jsonl entry %d summary = %q, want %q", i+1, entry.Summary, wrapupSummary)
		}
	}
}

func TestRunOneFinalSnippetExecutorFallbackConsistentAcrossRetryAndRecords(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newTestStore(t, rallyDir)

	executorSummary := strings.Repeat("executor final summary. ", 12) + "done"
	attempt := 0
	retryPreviousSummary := ""
	exec := &funcExecutor{
		fn: func(_ context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			if attempt == 2 {
				retryPreviousSummary = opts.PreviousSummary
			}
			return &harnessapi.TryResult{Completed: false, Summary: executorSummary}, nil
		},
	}
	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RetryBudget:  2,
	}, map[string]harnessapi.Executor{"claude": exec})

	_, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "claude"},
		runTask{Name: "retry task"},
		nil,
		nil,
		false,
		false,
		nil,
		nil,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}

	if retryPreviousSummary != executorSummary {
		t.Fatalf("retry previous summary = %q, want %q", retryPreviousSummary, executorSummary)
	}
	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("tries = %d, want 2", len(tries))
	}
	for i, tr := range tries {
		if tr.Summary != executorSummary {
			t.Fatalf("try %d summary = %q, want %q", i+1, tr.Summary, executorSummary)
		}
	}

	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		t.Fatalf("LoadSummaryEntries error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("summary entries = %d, want 1", len(entries))
	}
	if entries[0].Summary != executorSummary {
		t.Fatalf("summary.jsonl summary = %q, want %q", entries[0].Summary, executorSummary)
	}
}

func TestRunOneNonOpenCodeInvalidStructuredResultDoesNotPersistTranscript(t *testing.T) {
	const rawTranscript = "RAW_TRANSCRIPT_THAT_MUST_NOT_PERSIST"

	tests := []struct {
		name        string
		harness     string
		script      string
		wantSummary string
		newExecutor func() harnessapi.Executor
	}{
		{
			name:    "claude missing result event",
			harness: "claude",
			script: "#!/bin/sh\n" +
				"printf '%s\\n' '{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"" + rawTranscript + "\"}]}}'\n",
			wantSummary: "claude produced no structured result",
			newExecutor: func() harnessapi.Executor { return &agent.ClaudeExecutor{} },
		},
		{
			name:    "claude result missing summary",
			harness: "claude",
			script: "#!/bin/sh\n" +
				"printf '%s\\n' '{\"type\":\"result\",\"result\":{\"transcript\":\"" + rawTranscript + "\"}}'\n",
			wantSummary: "claude structured result contained no summary",
			newExecutor: func() harnessapi.Executor { return &agent.ClaudeExecutor{} },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workspaceDir := t.TempDir()
			rallyDir := store.RallyDir(workspaceDir)
			if err := os.MkdirAll(rallyDir, 0o755); err != nil {
				t.Fatal(err)
			}
			s := newTestStore(t, rallyDir)

			binDir := filepath.Join(t.TempDir(), "bin")
			if err := os.MkdirAll(binDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(binDir, tc.harness), []byte(tc.script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			attempt := 0
			retryPreviousSummary := ""
			delegate := tc.newExecutor()
			exec := &funcExecutor{
				fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
					attempt++
					if attempt == 2 {
						retryPreviousSummary = opts.PreviousSummary
					}
					return delegate.Execute(ctx, opts)
				},
			}
			r := NewRunner(s, Config{
				WorkspaceDir: workspaceDir,
				DataDir:      t.TempDir(),
				RetryBudget:  2,
			}, map[string]harnessapi.Executor{tc.harness: exec})

			_, err := r.runOne(
				context.Background(),
				&store.RelayRecord{ID: 1, TargetIterations: 1},
				0,
				harnessapi.ResolvedAgent{Harness: tc.harness},
				runTask{Name: "missing structured result task"},
				nil,
				nil,
				false,
				false,
				nil,
				nil,
				io.Discard,
			)
			if err != nil {
				t.Fatalf("runOne error = %v", err)
			}

			if retryPreviousSummary != tc.wantSummary {
				t.Fatalf("retry previous summary = %q, want %q", retryPreviousSummary, tc.wantSummary)
			}
			if strings.Contains(retryPreviousSummary, rawTranscript) {
				t.Fatalf("retry context leaked raw transcript: %q", retryPreviousSummary)
			}

			tries := s.AllTries()
			if len(tries) != 2 {
				t.Fatalf("tries = %d, want 2", len(tries))
			}
			logData, err := os.ReadFile(tries[0].LogPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(logData), rawTranscript) {
				t.Fatalf("test setup did not place raw transcript in try log: %q", string(logData))
			}
			for i, tr := range tries {
				if tr.Summary != tc.wantSummary {
					t.Fatalf("try %d summary = %q, want %q", i+1, tr.Summary, tc.wantSummary)
				}
				if strings.Contains(tr.Summary, rawTranscript) {
					t.Fatalf("try %d summary leaked raw transcript: %q", i+1, tr.Summary)
				}
			}

			entries, err := progress.LoadSummaryEntries(workspaceDir)
			if err != nil {
				t.Fatalf("LoadSummaryEntries error = %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("summary entries = %d, want 1", len(entries))
			}
			if entries[0].Summary != tc.wantSummary {
				t.Fatalf("summary.jsonl summary = %q, want %q", entries[0].Summary, tc.wantSummary)
			}
			if strings.Contains(entries[0].Summary, rawTranscript) {
				t.Fatalf("summary.jsonl leaked raw transcript: %q", entries[0].Summary)
			}
		})
	}
}

func TestRunOneFinalSnippetUsesBoundedLogTailFallback(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newTestStore(t, rallyDir)

	logBody := "start-of-run narration that must not persist\n" +
		strings.Repeat("x", finalSnippetFallbackRuneLimit*2) +
		"\nuseful tail"
	exec := &funcExecutor{
		fn: func(_ context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if err := os.WriteFile(opts.LogPath, []byte(logBody), 0o644); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: false}, nil
		},
	}
	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RetryBudget:  1,
	}, map[string]harnessapi.Executor{"claude": exec})

	_, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "claude"},
		runTask{Name: "fallback task"},
		nil,
		nil,
		false,
		false,
		nil,
		nil,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("tries = %d, want 1", len(tries))
	}
	summary := tries[0].Summary
	if got := len([]rune(summary)); got > finalSnippetFallbackRuneLimit {
		t.Fatalf("summary rune length = %d, want <= %d", got, finalSnippetFallbackRuneLimit)
	}
	if !strings.HasPrefix(summary, finalSnippetTailMarker) {
		t.Fatalf("summary = %q, want tail truncation marker", summary)
	}
	if !strings.Contains(summary, "useful tail") {
		t.Fatalf("summary = %q, want useful tail", summary)
	}
	if strings.Contains(summary, "start-of-run narration") {
		t.Fatalf("summary retained start-of-run narration: %q", summary)
	}

	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		t.Fatalf("LoadSummaryEntries error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("summary entries = %d, want 1", len(entries))
	}
	if entries[0].Summary != summary {
		t.Fatalf("summary.jsonl summary = %q, want try summary %q", entries[0].Summary, summary)
	}
}

func TestNormalizeFinalSnippetUsesExplicitIndicatorsWithoutUsableText(t *testing.T) {
	r := &Runner{cfg: Config{WorkspaceDir: t.TempDir()}}

	errText := strings.Repeat("boom ", finalSnippetFallbackRuneLimit)
	got := r.normalizeFinalSnippet("relay-1-run-1", "", 0, nil, errors.New(errText))
	if !strings.HasPrefix(got, "harness error: ") {
		t.Fatalf("error snippet = %q, want harness error prefix", got)
	}
	if len([]rune(got)) > finalSnippetFallbackRuneLimit {
		t.Fatalf("error snippet rune length = %d, want <= %d", len([]rune(got)), finalSnippetFallbackRuneLimit)
	}

	got = r.normalizeFinalSnippet("relay-1-run-1", "", 0, nil, nil)
	if got != noFinalSnippetIndicator {
		t.Fatalf("no-text snippet = %q, want %q", got, noFinalSnippetIndicator)
	}
}

func TestNormalizeFinalSnippetIgnoresOlderRunSummaryEntry(t *testing.T) {
	workspaceDir := t.TempDir()
	if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
		RunID:   "relay-1-run-1",
		Summary: "(agent exited without finalizing)",
	}); err != nil {
		t.Fatal(err)
	}
	entryCountBefore := progressSummaryEntryCount(workspaceDir)
	r := &Runner{cfg: Config{WorkspaceDir: workspaceDir}}

	got := r.normalizeFinalSnippet(
		"relay-1-run-1",
		"",
		entryCountBefore,
		&harnessapi.TryResult{Summary: "current executor summary"},
		nil,
	)
	if got != "current executor summary" {
		t.Fatalf("normalized summary = %q, want current executor summary", got)
	}
}
