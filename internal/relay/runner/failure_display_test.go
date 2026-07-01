package runner

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestCategorizedTryRecordCarriesCategoryAndDisplayReason(t *testing.T) {
	tests := []struct {
		name                   string
		category               reliability.FailureCategory
		evidence               *reliability.FailureEvidence
		wantCategory           string
		wantFailReasonContains string
	}{
		{
			name:                   "usage_limit with reset",
			category:               reliability.CategoryUsageLimit,
			evidence:               &reliability.FailureEvidence{Category: reliability.CategoryUsageLimit, ResetAfter: 5 * time.Hour},
			wantCategory:           string(reliability.CategoryUsageLimit),
			wantFailReasonContains: "usage limit, resets in",
		},
		{
			name:                   "short_rate_limit",
			category:               reliability.CategoryShortRateLimit,
			evidence:               &reliability.FailureEvidence{Category: reliability.CategoryShortRateLimit, RetryAfter: 90 * time.Second},
			wantCategory:           string(reliability.CategoryShortRateLimit),
			wantFailReasonContains: "rate limit, waiting",
		},
		{
			name:                   "invalid_model",
			category:               reliability.CategoryInvalidModel,
			evidence:               &reliability.FailureEvidence{Category: reliability.CategoryInvalidModel},
			wantCategory:           string(reliability.CategoryInvalidModel),
			wantFailReasonContains: "invalid model",
		},
		{
			name:                   "auth_or_proxy",
			category:               reliability.CategoryAuthOrProxy,
			evidence:               &reliability.FailureEvidence{Category: reliability.CategoryAuthOrProxy},
			wantCategory:           string(reliability.CategoryAuthOrProxy),
			wantFailReasonContains: "auth/proxy error",
		},
		{
			name:                   "provider_overloaded",
			category:               reliability.CategoryProviderOverloaded,
			evidence:               &reliability.FailureEvidence{Category: reliability.CategoryProviderOverloaded},
			wantCategory:           string(reliability.CategoryProviderOverloaded),
			wantFailReasonContains: "provider overloaded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceDir := t.TempDir()
			rallyDir := store.RallyDir(workspaceDir)
			os.MkdirAll(rallyDir, 0o755)
			initRepo(t, workspaceDir)
			runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

			s := newTestStore(t, rallyDir)
			exec := &funcExecutor{
				fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
					if opts.LogPath != "" {
						_ = os.WriteFile(opts.LogPath, []byte("failed\n"), 0o644)
					}
					return &harnessapi.TryResult{Completed: false, Summary: "failed", Evidence: tt.evidence}, nil
				},
			}

			r := NewRunner(s, Config{
				WorkspaceDir:     workspaceDir,
				DataDir:          t.TempDir(),
				AgentMixSpecs:    []string{"op:dsf"},
				TargetIterations: 1,
				RetryBudget:      1,
				LapsEnabled:      true,
				Resolver:         cheapTestResolver,
			}, map[string]harnessapi.Executor{"opencode": exec})

			_, err := r.runOne(
				context.Background(),
				&store.RelayRecord{ID: 1, TargetIterations: 1},
				0,
				harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
				runTask{Name: "task", Prompt: "do work", Assignee: "senior"},
				nil, nil, false, false, nil, nil,
				io.Discard,
			)
			if err != nil {
				t.Fatalf("runOne error = %v", err)
			}

			tries := s.AllTries()
			if len(tries) != 1 {
				t.Fatalf("tries = %d, want 1", len(tries))
			}
			rec := tries[0]
			if rec.Category != tt.wantCategory {
				t.Fatalf("Category = %q, want %q", rec.Category, tt.wantCategory)
			}
			if !strings.Contains(rec.FailReason, tt.wantFailReasonContains) {
				t.Fatalf("FailReason = %q, want containing %q", rec.FailReason, tt.wantFailReasonContains)
			}
			if rec.Category == rec.FailReason {
				t.Fatalf("Category and FailReason should differ: both = %q", rec.Category)
			}
		})
	}
}

func TestFormatCategorizedDisplay(t *testing.T) {
	tests := []struct {
		name     string
		cat      reliability.FailureCategory
		cooldown time.Duration
		evidence *reliability.FailureEvidence
		want     string
	}{
		{
			name:     "usage_limit with reset after",
			cat:      reliability.CategoryUsageLimit,
			cooldown: 2 * time.Minute,
			evidence: &reliability.FailureEvidence{Category: reliability.CategoryUsageLimit, ResetAfter: 123*time.Hour + 50*time.Minute},
			want:     "usage limit, resets in 123h50m",
		},
		{
			name:     "usage_limit with reset at",
			cat:      reliability.CategoryUsageLimit,
			cooldown: 2 * time.Minute,
			evidence: &reliability.FailureEvidence{
				Category: reliability.CategoryUsageLimit,
				ResetAt:  func() *time.Time { t := time.Now().Add(5*time.Hour + 30*time.Minute); return &t }(),
			},
		},
		{
			// Without parsed reset evidence the label carries no timing: the
			// classifier cooldown is not the quota reset, and the real bench
			// window is BenchDefaultDuration.
			name:     "usage_limit without parsed reset omits timing",
			cat:      reliability.CategoryUsageLimit,
			cooldown: 2 * time.Minute,
			evidence: &reliability.FailureEvidence{Category: reliability.CategoryUsageLimit},
			want:     "usage limit",
		},
		{
			name:     "short_rate_limit with cooldown",
			cat:      reliability.CategoryShortRateLimit,
			cooldown: 2 * time.Minute,
			evidence: &reliability.FailureEvidence{Category: reliability.CategoryShortRateLimit},
			want:     "rate limit, waiting 2m",
		},
		{
			name:     "invalid_model no timing",
			cat:      reliability.CategoryInvalidModel,
			evidence: &reliability.FailureEvidence{Category: reliability.CategoryInvalidModel},
			want:     "invalid model",
		},
		{
			name: "agent_error no timing",
			cat:  reliability.CategoryAgentError,
			want: "agent error",
		},
		{
			name: "incomplete_finalization no timing",
			cat:  reliability.CategoryIncompleteFinalization,
			want: "incomplete: file changes without finalization",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCategorizedDisplay(tt.cat, tt.cooldown, tt.evidence)
			if tt.want != "" && got != tt.want {
				t.Fatalf("formatCategorizedDisplay() = %q, want %q", got, tt.want)
			}
			if strings.Contains(tt.name, "reset at") {
				if !strings.Contains(got, "usage limit, resets in") {
					t.Fatalf("expected 'usage limit, resets in' in output, got %q", got)
				}
				if strings.Contains(got, "0m") && !strings.Contains(got, "0h") {
					t.Fatalf("expected hours in output for reset_at test, got %q", got)
				}
			}
		})
	}
}
