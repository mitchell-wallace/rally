package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestRetryWithinRun(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			if attempt < 3 {
				return &harnessapi.TryResult{Completed: false, Summary: "fail"}, nil
			}
			// Create a file so the successful try is not a no-op failure.
			f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("success-%d.txt", attempt)))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	tries := s.AllTries()
	if len(tries) != 3 {
		t.Fatalf("got %d tries, want 3", len(tries))
	}
	for i, tr := range tries {
		if tr.OutingID != 1 {
			t.Fatalf("try %d runID = %d, want 1", i, tr.OutingID)
		}
		if tr.AttemptNumber != i+1 {
			t.Fatalf("try %d attempt = %d, want %d", i, tr.AttemptNumber, i+1)
		}
	}
	if tries[2].Completed != true {
		t.Fatal("final try should be completed")
	}
	relays := s.AllRelays()
	if len(relays) != 1 || relays[0].CompletedIterations != 1 {
		t.Fatalf("expected 1 completed iteration, got %+v", relays)
	}
}

func TestRunBenchesOpencodeUsageLimitQuotaScopeNotAgentError(t *testing.T) {
	tests := []struct {
		name           string
		category       reliability.FailureCategory
		routeSpecs     map[string][]string
		wantScope      string
		wantBenched    []ResilienceKey
		wantNotBenched []ResilienceKey
	}{
		{
			name:     "zai usage limit benches zai provider scope",
			category: reliability.CategoryUsageLimit,
			routeSpecs: map[string][]string{
				"default": {
					"opencode:zai-coding-plan/glm-5.2",
					"opencode:zai-coding-plan/glm-5.1",
					"opencode:opencode-go/kimi",
				},
			},
			wantScope: "opencode:zai-coding-plan",
			wantBenched: []ResilienceKey{
				{Harness: "opencode", Model: "zai-coding-plan/glm-5.2"},
				{Harness: "opencode", Model: "zai-coding-plan/glm-5.1"},
			},
			wantNotBenched: []ResilienceKey{
				{Harness: "opencode", Model: "opencode-go/kimi"},
			},
		},
		{
			name:     "opencode-go usage limit benches opencode-go provider scope",
			category: reliability.CategoryUsageLimit,
			routeSpecs: map[string][]string{
				"default": {
					"opencode:opencode-go/kimi",
					"opencode:opencode-go/qwen",
					"opencode:zai-coding-plan/glm-5.2",
				},
			},
			wantScope: "opencode:opencode-go",
			wantBenched: []ResilienceKey{
				{Harness: "opencode", Model: "opencode-go/kimi"},
				{Harness: "opencode", Model: "opencode-go/qwen"},
			},
			wantNotBenched: []ResilienceKey{
				{Harness: "opencode", Model: "zai-coding-plan/glm-5.2"},
			},
		},
		{
			name:     "agent error does not bench provider scope",
			category: reliability.CategoryAgentError,
			routeSpecs: map[string][]string{
				"default": {
					"opencode:zai-coding-plan/glm-5.2",
					"opencode:zai-coding-plan/glm-5.1",
					"opencode:opencode-go/kimi",
				},
			},
			wantNotBenched: []ResilienceKey{
				{Harness: "opencode", Model: "zai-coding-plan/glm-5.2"},
				{Harness: "opencode", Model: "zai-coding-plan/glm-5.1"},
				{Harness: "opencode", Model: "opencode-go/kimi"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldHeadPull := headPullLap
			oldQueueSize := queueSize
			claimed := false
			headPullLap = func(context.Context, string) (laps.Lap, error) {
				if claimed {
					return laps.NoLap, nil
				}
				claimed = true
				return laps.Lap{ID: "lap-opencode-limit", Title: "opencode limit", Assignee: "senior"}, nil
			}
			queueSize = func(context.Context, string) (int, error) { return 1, nil }
			defer func() {
				headPullLap = oldHeadPull
				queueSize = oldQueueSize
			}()

			workspaceDir := t.TempDir()
			rallyDir := store.RallyDir(workspaceDir)
			os.MkdirAll(rallyDir, 0o755)
			initRepo(t, workspaceDir)
			runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

			s := newTestStore(t, rallyDir)
			execCalls := 0
			exec := &funcExecutor{
				fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
					execCalls++
					ev := &reliability.FailureEvidence{Category: tt.category}
					if tt.category == reliability.CategoryUsageLimit {
						ev.ResetAfter = time.Hour
					}
					return &harnessapi.TryResult{Completed: false, Summary: "failed", Evidence: ev}, nil
				},
			}

			r := NewRunner(s, Config{
				WorkspaceDir:     workspaceDir,
				DataDir:          t.TempDir(),
				RouteSpecs:       tt.routeSpecs,
				TargetIterations: 1,
				RetryBudget:      1,
				LapsEnabled:      true,
				Resolver:         testResolver,
			}, map[string]harnessapi.Executor{"opencode": exec})

			if err := r.Run(context.Background()); err != nil {
				t.Fatalf("Run error = %v", err)
			}
			if execCalls != 1 {
				t.Fatalf("executor calls = %d, want 1", execCalls)
			}

			resilience := NewResilience(s)
			for _, key := range tt.wantBenched {
				if st, _ := resilience.GetState(key); st != StateBenched {
					t.Errorf("state(%s) = %s, want benched", key, st)
				}
				events, err := s.GetAgentStatus(key.Harness, key.Model)
				if err != nil {
					t.Fatalf("GetAgentStatus(%s): %v", key, err)
				}
				foundBench := false
				for _, event := range events {
					if event.EventType != "benched" {
						continue
					}
					foundBench = true
					if event.QuotaScope != tt.wantScope {
						t.Errorf("quota_scope(%s) = %q, want %q", key, event.QuotaScope, tt.wantScope)
					}
					if event.ResetAt == "" {
						t.Errorf("reset_at(%s) is empty on benched event", key)
					}
				}
				if !foundBench {
					t.Errorf("no benched event recorded for %s", key)
				}
			}
			for _, key := range tt.wantNotBenched {
				if st, _ := resilience.GetState(key); st == StateBenched {
					t.Errorf("state(%s) = benched, want not benched", key)
				}
			}
			if tt.category == reliability.CategoryAgentError {
				if got := countAgentStatusEvents(s, "benched"); got != 0 {
					t.Fatalf("benched events = %d, want 0 for agent_error", got)
				}
			}
		})
	}
}
