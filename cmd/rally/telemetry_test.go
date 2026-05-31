package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
	"github.com/mitchell-wallace/rally/internal/testutil"
)

type mockSink struct {
	telemetry.NoopSink
	capturedIssues []string
	tryLogs        []map[string]interface{}
}

func (m *mockSink) EmitTryLog(ctx context.Context, fields map[string]interface{}) {
	m.tryLogs = append(m.tryLogs, fields)
}

func (m *mockSink) CaptureFailure(ctx context.Context, msg string, tags map[string]string) {
	m.capturedIssues = append(m.capturedIssues, msg)
}

type failingExecutor struct {}

func (f *failingExecutor) Execute(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
	return &agent.TryResult{Completed: false}, nil
}
func (f *failingExecutor) RotateModel(newModel string) error { return nil }
func (f *failingExecutor) ResumeSupported() bool { return false }
func (f *failingExecutor) RotateSupported() bool { return false }
func (f *failingExecutor) LivenessProbeSupported() bool { return false }
func (f *failingExecutor) ProbeLiveness(ctx context.Context) (bool, error) { return false, nil }

func TestTelemetryIssueCriteriaAndPromptSize(t *testing.T) {
	workspaceDir := testutil.SetupLapsFixtureProject(t)

	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}

	executors := map[string]agent.Executor{
		"antigravity": &failingExecutor{},
		"backupagent": &failingExecutor{},
	}

	cfg := relay.Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          store.RallyDir(workspaceDir),
		TargetIterations: 1,
		RetryBudget:      0,
		AgentMixSpecs:    []string{"junior"},
		RouteSpecs: map[string][]string{
			"junior": {"antigravity", "backupagent"},
		},
		UseOverrideRoute: true,
	}

	runner := relay.NewRunner(s, cfg, executors)
	mock := &mockSink{}
	runner.SetTelemetry(mock)

	err = runner.Run(context.Background())
	if err != nil {
		t.Log("Run returned error:", err)
	}

	if len(mock.capturedIssues) == 0 {
		t.Log("Captured issues: ", mock.capturedIssues)
		t.Fatal("expected an Issue to be captured")
	}

	foundFallbackLog := false
	for _, log := range mock.tryLogs {
		if log["event"] == "route_fallback" {
			foundFallbackLog = true
		}
	}
	if !foundFallbackLog {
		t.Errorf("expected route_fallback to be logged as a common event via EmitTryLog")
	}

	for _, issue := range mock.capturedIssues {
		if strings.Contains(issue, "route fallback") || strings.Contains(issue, "rotated") {
			t.Errorf("expected route fallback to NOT be logged as an Issue, but found: %s", issue)
		}
	}

	if len(mock.tryLogs) == 0 {
		t.Fatal("expected EmitTryLog to be called")
	}

	// Verify prompt size fields on a regular try log
	var tryLog map[string]interface{}
	for _, log := range mock.tryLogs {
		if log["event"] != "route_fallback" {
			tryLog = log
			break
		}
	}
	if tryLog == nil {
		t.Fatal("expected a standard try log")
	}

	if tryLog["prompt_bytes"] == nil || tryLog["prompt_bytes"].(int) == 0 {
		t.Errorf("missing or zero prompt_bytes in try log")
	}
	if tryLog["prompt_task_bytes"] == nil {
		t.Errorf("missing prompt_task_bytes in try log")
	}
}
