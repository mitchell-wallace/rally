package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	capturedTags   []map[string]string
	tryLogs        []map[string]interface{}
}

func (m *mockSink) EmitTryLog(ctx context.Context, fields map[string]interface{}) {
	m.tryLogs = append(m.tryLogs, fields)
}

func (m *mockSink) CaptureFailure(ctx context.Context, msg string, tags map[string]string) {
	m.capturedIssues = append(m.capturedIssues, msg)
	m.capturedTags = append(m.capturedTags, tags)
}

type customExecutor struct {
	attempts   int
	succeedOn  int
	logContent string
	execError  error
}

func (c *customExecutor) Execute(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
	c.attempts++
	if c.logContent != "" && opts.LogPath != "" {
		_ = os.WriteFile(opts.LogPath, []byte(c.logContent), 0o644)
	}
	// Make a file change so that a successful attempt does not fail with "no changes made"
	if opts.WorkspaceDir != "" {
		_ = os.WriteFile(filepath.Join(opts.WorkspaceDir, "dummy.txt"), []byte(fmt.Sprintf("attempt %d", c.attempts)), 0o644)
	}
	if c.execError != nil {
		return nil, c.execError
	}
	completed := c.attempts >= c.succeedOn
	summary := fmt.Sprintf("try %d summary", c.attempts)
	return &agent.TryResult{
		Completed: completed,
		Summary:   summary,
	}, nil
}

func (c *customExecutor) RotateModel(newModel string) error { return nil }
func (c *customExecutor) ResumeSupported() bool             { return false }
func (c *customExecutor) RotateSupported() bool             { return false }
func (c *customExecutor) LivenessProbeSupported() bool      { return false }
func (c *customExecutor) ProbeLiveness(ctx context.Context) (bool, error) {
	return false, nil
}

// Test original telemetry/prompt check
func TestTelemetryIssueCriteriaAndPromptSize(t *testing.T) {
	workspaceDir := testutil.SetupLapsFixtureProject(t)

	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}

	executors := map[string]agent.Executor{
		"antigravity": &customExecutor{succeedOn: 99}, // always fail
		"backupagent": &customExecutor{succeedOn: 99}, // always fail
	}

	cfg := relay.Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          store.RallyDir(workspaceDir),
		TargetIterations: 1,
		RetryBudget:      1,
		AgentMixSpecs:    []string{"junior"},
		RouteSpecs: map[string][]string{
			"junior": {"antigravity", "backupagent"},
		},
		UseOverrideRoute: true,
		Resolver: func(spec string) (agent.ResolvedAgent, error) {
			if spec == "backupagent" {
				return agent.ResolvedAgent{Harness: "backupagent", Model: "default-model"}, nil
			}
			return agent.ResolvedAgent{Harness: "antigravity", Model: "default-model"}, nil
		},
	}

	runner := relay.NewRunner(s, cfg, executors)
	mock := &mockSink{}
	runner.SetTelemetry(mock)

	_ = runner.Run(context.Background())

	if len(mock.capturedIssues) == 0 {
		t.Fatal("expected an Issue to be captured")
	}

	foundFallbackLog := false
	for _, log := range mock.tryLogs {
		if log["event"] == "route_fallback" {
			foundFallbackLog = true
		}
	}
	if !foundFallbackLog {
		t.Errorf("expected route_fallback to be logged as a common event")
	}

	for _, issue := range mock.capturedIssues {
		if strings.Contains(issue, "route fallback") || strings.Contains(issue, "rotated") {
			t.Errorf("expected route fallback to NOT be logged as an Issue, got: %s", issue)
		}
	}
}

func TestTelemetry_PromptBreakdown(t *testing.T) {
	workspaceDir := testutil.SetupLapsFixtureProject(t)

	// Add a lap to the queue with title/description/assignee
	ctx := context.Background()
	addCmd := exec.CommandContext(ctx, "laps", "add", "head", "--title", "junior-task", "--description", "task prompt text", "--assignee", "junior")
	addCmd.Dir = workspaceDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("laps add head failed: %v\noutput: %s", err, out)
	}

	// Create role instructions file
	agentsDir := store.AgentsDir(workspaceDir)
	_ = os.MkdirAll(agentsDir, 0o755)
	roleFile := filepath.Join(agentsDir, "junior.md")
	if err := os.WriteFile(roleFile, []byte("role instructions content"), 0o644); err != nil {
		t.Fatalf("failed to write role instructions: %v", err)
	}

	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}

	// Add a dummy try record to the store to populate recent context
	dummyTry := store.TryRecord{
		ID:        999,
		RunID:     1,
		RelayID:   1, // active relay ID will be 1
		AgentType: "antigravity",
		Completed: false,
		Summary:   "previous run dummy summary",
	}
	if err := s.AppendTry(dummyTry); err != nil {
		t.Fatalf("failed to append dummy try: %v", err)
	}

	// Add an inbox message and relay message
	inboxMsg := store.MessageRecord{
		ID:        1,
		Body:      "inbox message body",
		Status:    "pending",
		Scope:     "run",
		CreatedAt: "2026-05-31T09:00:00Z",
	}
	if err := s.AddMessage(inboxMsg); err != nil {
		t.Fatalf("failed to add inbox message: %v", err)
	}

	relayMsg := store.MessageRecord{
		ID:        2,
		Body:      "relay message body",
		Status:    "pending",
		Scope:     "relay",
		CreatedAt: "2026-05-31T09:00:00Z",
	}
	if err := s.AddMessage(relayMsg); err != nil {
		t.Fatalf("failed to add relay message: %v", err)
	}

	executors := map[string]agent.Executor{
		"antigravity": &customExecutor{succeedOn: 2}, // fails first try, succeeds second
	}

	cfg := relay.Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          store.RallyDir(workspaceDir),
		LapsEnabled:      true,
		TargetIterations: 1,
		RetryBudget:      2,
		AgentMixSpecs:    []string{"junior"},
		RouteSpecs: map[string][]string{
			"junior": {"antigravity"},
		},
		UseOverrideRoute: true,
		Instructions:     "global instructions content",
		Resolver: func(spec string) (agent.ResolvedAgent, error) {
			return agent.ResolvedAgent{Harness: "antigravity", Model: "default-model"}, nil
		},
	}

	runner := relay.NewRunner(s, cfg, executors)
	mock := &mockSink{}
	runner.SetTelemetry(mock)

	err = runner.Run(context.Background())
	if err != nil {
		t.Fatalf("runner failed: %v", err)
	}

	// Find the try log for attempt 2 (which succeeded)
	var try2Log map[string]interface{}
	for _, log := range mock.tryLogs {
		if log["event"] == "try" && log["attempt"] == 2 {
			try2Log = log
		}
	}
	if try2Log == nil {
		t.Fatal("expected a try log for attempt 2")
	}

	// Assert prompt breakdowns match exact lengths
	expected := map[string]int{
		"prompt_instructions_bytes":      len("global instructions content"),
		"prompt_role_instructions_bytes": len("role instructions content"),
		"prompt_inbox_bytes":             len("inbox message body"),
		"prompt_relay_message_bytes":     len("relay message body"),
		"prompt_previous_summary_bytes":  len("try 1 summary"),
	}

	for key, expectedLen := range expected {
		val, ok := try2Log[key]
		if !ok {
			t.Errorf("missing key %s in try log", key)
			continue
		}
		gotLen, ok := val.(int)
		if !ok {
			t.Errorf("key %s is not an int: %v", key, val)
			continue
		}
		if gotLen != expectedLen {
			t.Errorf("expected %s to be %d, got %d", key, expectedLen, gotLen)
		}
	}

	// prompt_task_bytes should be at least the original task prompt length
	taskVal, ok := try2Log["prompt_task_bytes"]
	if !ok {
		t.Errorf("missing prompt_task_bytes in try log")
	} else {
		taskLen := taskVal.(int)
		if taskLen < len("task prompt text") {
			t.Errorf("expected prompt_task_bytes to be at least %d, got %d", len("task prompt text"), taskLen)
		}
	}

	// prompt_recent_context_bytes should be non-zero because of the dummy try
	recentVal, ok := try2Log["prompt_recent_context_bytes"]
	if !ok || recentVal.(int) == 0 {
		t.Errorf("expected prompt_recent_context_bytes to be non-zero")
	}

	// prompt_bytes should be the sum of all elements (which is > 0)
	totalVal, ok := try2Log["prompt_bytes"]
	if !ok || totalVal.(int) == 0 {
		t.Errorf("expected prompt_bytes to be non-zero")
	}
}

func TestTelemetry_AgentClassRetry_NoIssue(t *testing.T) {
	workspaceDir := testutil.SetupLapsFixtureProject(t)

	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}

	// Fail on attempt 1, succeed on attempt 2. Classifies as FailureAgent.
	executors := map[string]agent.Executor{
		"antigravity": &customExecutor{succeedOn: 2},
	}

	cfg := relay.Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          store.RallyDir(workspaceDir),
		TargetIterations: 1,
		RetryBudget:      2,
		AgentMixSpecs:    []string{"junior"},
		RouteSpecs: map[string][]string{
			"junior": {"antigravity"},
		},
		UseOverrideRoute: true,
		Resolver: func(spec string) (agent.ResolvedAgent, error) {
			return agent.ResolvedAgent{Harness: "antigravity", Model: "default-model"}, nil
		},
	}

	runner := relay.NewRunner(s, cfg, executors)
	mock := &mockSink{}
	runner.SetTelemetry(mock)

	err = runner.Run(context.Background())
	if err != nil {
		t.Fatalf("runner failed: %v", err)
	}

	// Verify no issues were captured (since it failed agent-class and then succeeded)
	if len(mock.capturedIssues) > 0 {
		t.Errorf("expected no captured issues for agent-class retry, but got: %v", mock.capturedIssues)
	}
}

func TestTelemetry_InfraFailure_Issue(t *testing.T) {
	workspaceDir := testutil.SetupLapsFixtureProject(t)

	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}

	// Write an API-timeout signal to the log file to classify as FailureInfra.
	// (The rate-limit pattern previously used here is now scoped to the claude
	// harness; "request timed out" stays harness-agnostic transient infra.)
	executors := map[string]agent.Executor{
		"antigravity": &customExecutor{succeedOn: 99, logContent: "request timed out\n"},
	}

	cfg := relay.Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          store.RallyDir(workspaceDir),
		TargetIterations: 1,
		RetryBudget:      1,
		AgentMixSpecs:    []string{"junior"},
		RouteSpecs: map[string][]string{
			"junior": {"antigravity"},
		},
		UseOverrideRoute: true,
		Resolver: func(spec string) (agent.ResolvedAgent, error) {
			return agent.ResolvedAgent{Harness: "antigravity", Model: "default-model"}, nil
		},
	}

	runner := relay.NewRunner(s, cfg, executors)
	mock := &mockSink{}
	runner.SetTelemetry(mock)

	_ = runner.Run(context.Background())

	// Verify that an Issue was captured containing the rate-limit failure
	foundInfraIssue := false
	for _, issue := range mock.capturedIssues {
		if strings.Contains(issue, "try 1 failed") {
			foundInfraIssue = true
		}
	}
	if !foundInfraIssue {
		t.Errorf("expected infra failure Issue (try failed), captured issues: %v", mock.capturedIssues)
	}
}

func TestTelemetry_RelayStall_Issue(t *testing.T) {
	workspaceDir := testutil.SetupLapsFixtureProject(t)

	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}

	// Freeze the agents in store
	resilience := relay.NewResilience(s)
	if err := resilience.FreezeAgent(relay.ResilienceKey{Harness: "antigravity", Model: "default-model"}, 1, "test freeze"); err != nil {
		t.Fatalf("failed to freeze agent: %v", err)
	}

	executors := map[string]agent.Executor{
		"antigravity": &customExecutor{succeedOn: 1},
	}

	cfg := relay.Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          store.RallyDir(workspaceDir),
		TargetIterations: 1,
		RetryBudget:      1,
		AgentMixSpecs:    []string{"junior"},
		RouteSpecs: map[string][]string{
			"junior": {"antigravity"},
		},
		UseOverrideRoute: true,
		Resolver: func(spec string) (agent.ResolvedAgent, error) {
			return agent.ResolvedAgent{Harness: "antigravity", Model: "default-model"}, nil
		},
	}

	runner := relay.NewRunner(s, cfg, executors)
	mock := &mockSink{}
	runner.SetTelemetry(mock)

	err = runner.Run(context.Background())
	if err == nil {
		t.Fatal("expected runner to return error due to all agents frozen")
	}

	// Verify that the relay stall Issue was captured
	foundStallIssue := false
	for _, issue := range mock.capturedIssues {
		if strings.Contains(issue, "stalled: all agents frozen") {
			foundStallIssue = true
		}
	}
	if !foundStallIssue {
		t.Errorf("expected stall Issue (all agents frozen), captured issues: %v", mock.capturedIssues)
	}
}
