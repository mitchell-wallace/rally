package runner

import (
	"context"
	"fmt"
	"github.com/mitchell-wallace/rally/internal/harness/generic"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestRunnerDoesNotCreateRepoRelayLogDir(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rallyDir, ".gitignore"), []byte("state/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	dataDir := t.TempDir()
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "change.txt"), []byte("ok\n"), 0o644); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		RouteSpecs:       map[string][]string{"default": {"op:model:1"}},
		TargetIterations: 1,
		Resolver:         testResolver,
		TaskPrompt:       "no repo relay logs",
	}, map[string]harnessapi.Executor{"opencode": exec})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if _, err := os.Stat(relayLogPath(dataDir, workspaceDir, 1)); err != nil {
		t.Fatalf("expected data-dir relay log: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rallyDir, "relays")); !os.IsNotExist(err) {
		t.Fatalf(".rally/relays should not be created when .rally/.gitignore only ignores state/, stat err=%v", err)
	}
}

func TestGracefulStop(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			time.Sleep(50 * time.Millisecond)
			changeCounter++
			// Append unique content so the try is not a no-op failure.
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "stop %d\n", changeCounter)
			f.Close()
			return &harnessapi.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 5,
	}, executors)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- r.Run(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
	r.RequestStop()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop in time")
	}

	relays := s.AllRelays()
	if len(relays) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(relays))
	}
	if relays[0].CompletedIterations >= 5 {
		t.Fatalf("expected < 5 iterations after stop, got %d", relays[0].CompletedIterations)
	}
}

func TestMessageConsumptionPerRun(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	s.AddMessage(store.MessageRecord{ID: 1, Body: "msg1", Status: "pending", Position: 1})
	s.AddMessage(store.MessageRecord{ID: 2, Body: "msg2", Status: "pending", Position: 2})

	addressed := false
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			addressed = true
			msgAddr := true
			changeCounter++
			// Append unique content so the try is not a no-op failure.
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "msg %d\n", changeCounter)
			f.Close()
			return &harnessapi.TryResult{Completed: true, MessageAddressed: &msgAddr}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 2,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if !addressed {
		t.Fatal("expected message to be addressed")
	}
	msgs := s.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	pending := 0
	for _, m := range msgs {
		if m.Status == "pending" {
			pending++
		}
	}
	if pending != 0 {
		t.Fatalf("expected 0 pending messages, got %d", pending)
	}
}

func TestRelayScopedMessageIncludedInAllRuns(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	s.AddMessage(store.MessageRecord{ID: 1, Body: "relay-msg", Status: "pending", Position: 1, Scope: "relay"})

	var relayMsgsSeen []string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			relayMsgsSeen = append(relayMsgsSeen, opts.RelayMessage)
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &harnessapi.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 3,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if len(relayMsgsSeen) != 3 {
		t.Fatalf("expected relay message in 3 runs, got %d", len(relayMsgsSeen))
	}
	for i, msg := range relayMsgsSeen {
		if msg != "relay-msg" {
			t.Fatalf("run %d relay message = %q, want 'relay-msg'", i, msg)
		}
	}
}

func TestRelayScopedMessageAddressed(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	s.AddMessage(store.MessageRecord{ID: 1, Body: "relay-msg", Status: "pending", Position: 1, Scope: "relay"})

	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			msgAddr := true
			return &harnessapi.TryResult{Completed: true, MessageAddressed: &msgAddr}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 2,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	relay := s.AllRelays()
	if len(relay) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(relay))
	}
	foundRelayMsg := false
	for _, id := range relay[0].ConsumedMessageIDs {
		if id == 1 {
			foundRelayMsg = true
		}
	}
	if !foundRelayMsg {
		t.Fatal("expected relay message ID 1 in ConsumedMessageIDs")
	}

	msgs := s.GetMessages()
	for _, m := range msgs {
		if m.ID == 1 && m.Status != "addressed" {
			t.Fatalf("expected relay message addressed, got %s", m.Status)
		}
	}
}

func TestCombinedRelayAndRunScopedMessages(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	s.AddMessage(store.MessageRecord{ID: 1, Body: "relay-msg", Status: "pending", Position: 1, Scope: "relay"})
	s.AddMessage(store.MessageRecord{ID: 2, Body: "run-msg-1", Status: "pending", Position: 2})
	s.AddMessage(store.MessageRecord{ID: 3, Body: "run-msg-2", Status: "pending", Position: 3})

	var relayMsgsSeen []string
	var inboxMsgsSeen []string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			relayMsgsSeen = append(relayMsgsSeen, opts.RelayMessage)
			inboxMsgsSeen = append(inboxMsgsSeen, opts.InboxMessage)
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			msgAddr := true
			return &harnessapi.TryResult{Completed: true, MessageAddressed: &msgAddr}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 2,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	// Relay message seen in both runs
	if len(relayMsgsSeen) != 2 {
		t.Fatalf("expected relay message in 2 runs, got %d", len(relayMsgsSeen))
	}
	for i, msg := range relayMsgsSeen {
		if msg != "relay-msg" {
			t.Fatalf("run %d relay message = %q, want 'relay-msg'", i, msg)
		}
	}

	// Inbox messages: each run gets a different run-scoped message
	if len(inboxMsgsSeen) != 2 {
		t.Fatalf("expected inbox message in 2 runs, got %d", len(inboxMsgsSeen))
	}
	if inboxMsgsSeen[0] != "run-msg-1" {
		t.Fatalf("run 1 inbox = %q, want 'run-msg-1'", inboxMsgsSeen[0])
	}
	if inboxMsgsSeen[1] != "run-msg-2" {
		t.Fatalf("run 2 inbox = %q, want 'run-msg-2'", inboxMsgsSeen[1])
	}

	// All messages should be addressed
	msgs := s.GetMessages()
	for _, m := range msgs {
		if m.Status != "addressed" {
			t.Fatalf("expected message %d to be addressed, got %s", m.ID, m.Status)
		}
	}

	// Relay ConsumedMessageIDs should have all 3 message IDs
	relay := s.AllRelays()
	if len(relay) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(relay))
	}
	if len(relay[0].ConsumedMessageIDs) != 3 {
		t.Fatalf("expected 3 consumed message IDs, got %v", relay[0].ConsumedMessageIDs)
	}
}

func writeRelayScript(t *testing.T, dir, name, scriptContent string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestE2E_FullConfig_NamedModelsAndFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	fallbackFile := filepath.Join(workspaceDir, "fallback.md")
	os.WriteFile(fallbackFile, []byte("Custom fallback instructions."), 0o644)

	configContent := fmt.Sprintf(`schema_version = 2

[defaults]
iterations = 2
mix = "cc:opus"

[harness.cc.models]
opus = "claude-opus-4-7"

[fallback]
instructions_file = %q
`, fallbackFile)
	os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(configContent), 0o644)

	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if cfg.Defaults.Iterations != 2 {
		t.Fatalf("iterations = %d, want 2", cfg.Defaults.Iterations)
	}
	if cfg.Defaults.Mix != "cc:opus" {
		t.Fatalf("mix = %q, want 'cc:opus'", cfg.Defaults.Mix)
	}

	resolver := func(spec string) (harnessapi.ResolvedAgent, error) {
		ra, err := cfg.ResolveAgent(spec)
		if err != nil {
			return harnessapi.ResolvedAgent{}, err
		}
		return harnessapi.ResolvedAgent{Harness: ra.Harness, Model: ra.Model}, nil
	}

	s := newTestStore(t, rallyDir)
	var capturedModel string
	var capturedTaskPrompt string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			capturedModel = opts.Model
			capturedTaskPrompt = opts.TaskPrompt
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &harnessapi.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	mixSpecs := strings.Fields(cfg.Defaults.Mix)
	r := NewRunner(s, Config{
		WorkspaceDir:      workspaceDir,
		DataDir:           t.TempDir(),
		AgentMixSpecs:     mixSpecs,
		TargetIterations:  cfg.Defaults.Iterations,
		Resolver:          resolver,
		FreeRunPromptFile: cfg.FreeRun.PromptFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if capturedModel != "claude-opus-4-7" {
		t.Errorf("expected model 'claude-opus-4-7' from named resolution, got %q", capturedModel)
	}
	if capturedTaskPrompt != "Custom fallback instructions." {
		t.Errorf("expected fallback instructions as task prompt, got %q", capturedTaskPrompt)
	}

	relays := s.AllRelays()
	if len(relays) != 1 || relays[0].CompletedIterations != 2 {
		t.Fatalf("expected 2 completed iterations, got %+v", relays)
	}
}

func TestE2E_UserDefinedHarness_ModelFlagSet(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	argsFile := filepath.Join(workspaceDir, "recorded_args.txt")
	changeFile := filepath.Join(workspaceDir, "changes.txt")
	script := writeRelayScript(t, workspaceDir, "droid.sh", fmt.Sprintf(
		`echo "ARGS:$@" > %q; touch %q; echo "ok"`, argsFile, changeFile))

	modelFlag := "--model"
	droidExec := generic.New([]string{script}, &modelFlag, "", 0, "", "")

	resolver := func(spec string) (harnessapi.ResolvedAgent, error) {
		if spec == "droid:v1" {
			return harnessapi.ResolvedAgent{Harness: "droid", Model: "droid-v1"}, nil
		}
		if spec == "droid" {
			return harnessapi.ResolvedAgent{Harness: "droid"}, nil
		}
		return testResolver(spec)
	}

	s := newTestStore(t, rallyDir)
	executors := map[string]harnessapi.Executor{"droid": droidExec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"droid:v1"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "--model") || !strings.Contains(content, "droid-v1") {
		t.Errorf("expected '--model droid-v1' in args, got %q", content)
	}
}

func TestE2E_UserDefinedHarness_BareAliasNoModel(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	argsFile := filepath.Join(workspaceDir, "recorded_args.txt")
	changeFile := filepath.Join(workspaceDir, "changes.txt")
	script := writeRelayScript(t, workspaceDir, "droid.sh", fmt.Sprintf(
		`echo "ARGS:$@" > %q; touch %q; echo "ok"`, argsFile, changeFile))

	modelFlag := "--model"
	droidExec := generic.New([]string{script}, &modelFlag, "", 0, "", "")

	resolver := func(spec string) (harnessapi.ResolvedAgent, error) {
		if spec == "droid" {
			return harnessapi.ResolvedAgent{Harness: "droid"}, nil
		}
		return testResolver(spec)
	}

	s := newTestStore(t, rallyDir)
	executors := map[string]harnessapi.Executor{"droid": droidExec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"droid"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "--model") || strings.Contains(content, "droid-v1") {
		t.Errorf("expected no model in args for bare alias, got %q", content)
	}
}

func TestE2E_UserDefinedHarness_ModelFlagEmpty(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	argsFile := filepath.Join(workspaceDir, "recorded_args.txt")
	changeFile := filepath.Join(workspaceDir, "changes.txt")
	script := writeRelayScript(t, workspaceDir, "droid.sh", fmt.Sprintf(
		`echo "ARGS:$@" > %q; touch %q; echo "ok"`, argsFile, changeFile))

	modelFlag := ""
	droidExec := generic.New([]string{script}, &modelFlag, "", 0, "", "")

	resolver := func(spec string) (harnessapi.ResolvedAgent, error) {
		if spec == "droid:v1" {
			return harnessapi.ResolvedAgent{Harness: "droid", Model: "droid-v1"}, nil
		}
		return testResolver(spec)
	}

	s := newTestStore(t, rallyDir)
	executors := map[string]harnessapi.Executor{"droid": droidExec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"droid:v1"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "droid-v1") {
		t.Errorf("expected positional model 'droid-v1' in args, got %q", content)
	}
	if strings.Contains(content, "--model") {
		t.Errorf("expected no '--model' flag, got %q", content)
	}
}

func TestE2E_UserDefinedHarness_ModelFlagUnset_InfoNote(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	argsFile := filepath.Join(workspaceDir, "recorded_args.txt")
	changeFile := filepath.Join(workspaceDir, "changes.txt")
	script := writeRelayScript(t, workspaceDir, "droid.sh", fmt.Sprintf(
		`echo "ARGS:$@" > %q; touch %q; echo "ok"`, argsFile, changeFile))

	droidExec := generic.New([]string{script}, nil, "", 0, "", "")

	resolver := func(spec string) (harnessapi.ResolvedAgent, error) {
		if spec == "droid:v1" {
			return harnessapi.ResolvedAgent{Harness: "droid", Model: "droid-v1"}, nil
		}
		return testResolver(spec)
	}

	s := newTestStore(t, rallyDir)
	executors := map[string]harnessapi.Executor{"droid": droidExec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"droid:v1"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "droid-v1") {
		t.Errorf("expected no model in args when model_flag unset, got %q", content)
	}
}

func TestE2E_BackwardsCompat_RootModelFields(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	configContent := `claude_model = "root-sonnet"
codex_model = "root-codex"
data_dir = "/tmp/data"
run_hooks_on_autocommit = true
`
	os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(configContent), 0o644)

	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if len(cfg.DeprecationNotes) != 2 {
		t.Fatalf("expected 2 deprecation notes, got %d: %v", len(cfg.DeprecationNotes), cfg.DeprecationNotes)
	}

	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("ResolveAgent cc: %v", err)
	}
	if resolved.Model != "root-sonnet" {
		t.Errorf("expected model 'root-sonnet' from root-level field, got %q", resolved.Model)
	}

	resolver := func(spec string) (harnessapi.ResolvedAgent, error) {
		ra, err := cfg.ResolveAgent(spec)
		if err != nil {
			return harnessapi.ResolvedAgent{}, err
		}
		return harnessapi.ResolvedAgent{Harness: ra.Harness, Model: ra.Model}, nil
	}

	s := newTestStore(t, rallyDir)
	var capturedModel string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			capturedModel = opts.Model
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &harnessapi.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if capturedModel != "root-sonnet" {
		t.Errorf("expected model 'root-sonnet' reaching executor, got %q", capturedModel)
	}
}

func TestE2E_DefaultsSection_NoDeprecation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	configContent := `schema_version = 2

[defaults]
claude_model = "defaults-opus"
iterations = 1
mix = "cc"
`
	os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(configContent), 0o644)

	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if len(cfg.DeprecationNotes) != 0 {
		t.Fatalf("expected 0 deprecation notes, got %d: %v", len(cfg.DeprecationNotes), cfg.DeprecationNotes)
	}

	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("ResolveAgent cc: %v", err)
	}
	if resolved.Model != "defaults-opus" {
		t.Errorf("expected model 'defaults-opus' from [defaults], got %q", resolved.Model)
	}

	resolver := func(spec string) (harnessapi.ResolvedAgent, error) {
		ra, err := cfg.ResolveAgent(spec)
		if err != nil {
			return harnessapi.ResolvedAgent{}, err
		}
		return harnessapi.ResolvedAgent{Harness: ra.Harness, Model: ra.Model}, nil
	}

	s := newTestStore(t, rallyDir)
	var capturedModel string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			capturedModel = opts.Model
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &harnessapi.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    strings.Fields(cfg.Defaults.Mix),
		TargetIterations: cfg.Defaults.Iterations,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if capturedModel != "defaults-opus" {
		t.Errorf("expected model 'defaults-opus' reaching executor, got %q", capturedModel)
	}
}

func TestE2E_RunStateClearedAtRelayStart(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	if err := progress.SaveRunState(workspaceDir, &progress.RunState{RunID: "old-run", HandoffState: 1, RecordedLaps: []string{"lap-old"}}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true}, nil
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

	rs, err := progress.LoadRunState(workspaceDir)
	if err != nil {
		t.Fatalf("LoadRunState error: %v", err)
	}
	if rs.RunID != "" {
		t.Fatalf("expected run-state cleared at relay start, got RunID=%q", rs.RunID)
	}
	if rs.HandoffState != 0 {
		t.Fatalf("expected HandoffState=0 after relay start, got %d", rs.HandoffState)
	}
	if len(rs.RecordedLaps) != 0 {
		t.Fatalf("expected RecordedLaps empty after relay start, got %v", rs.RecordedLaps)
	}
}
