package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

type directFakeExecutor struct {
	results []*harnessapi.TryResult
	errs    []error
	opts    []harnessapi.RunOptions
}

func (f *directFakeExecutor) Execute(_ context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
	f.opts = append(f.opts, opts)
	i := len(f.opts) - 1
	var result *harnessapi.TryResult
	if i < len(f.results) {
		result = f.results[i]
	}
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return result, err
}

func (*directFakeExecutor) ResumeSupported() bool        { return true }
func (*directFakeExecutor) RotateSupported() bool        { return false }
func (*directFakeExecutor) LivenessProbeSupported() bool { return false }
func (*directFakeExecutor) RotateModel(string) error     { return errors.New("unsupported") }
func (*directFakeExecutor) ProbeLiveness(context.Context) (bool, error) {
	return false, errors.New("unsupported")
}

func directTestConfig(route []string, retryBudget int) config.V2Config {
	harnesses := make(map[string]*config.HarnessConfig, len(route))
	for _, name := range route {
		harnesses[name] = &config.HarnessConfig{}
	}
	return config.V2Config{
		Harnesses: harnesses,
		Routes: map[string][]string{
			"intern": route,
		},
		Reliability: config.ReliabilityConfig{RetryBudget: retryBudget},
	}
}

func TestRunDirectWritesOnlyFinalResponseToStdoutAndDoesNotCreateState(t *testing.T) {
	workspace := t.TempDir()
	input := filepath.Join(workspace, "prompt.md")
	if err := os.WriteFile(input, []byte("Summarize me."), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &directFakeExecutor{results: []*harnessapi.TryResult{{Completed: true, Summary: `{"answer":"ok"}`}}}
	var stdout, stderr bytes.Buffer

	err := runDirect(context.Background(), DirectRunOptions{
		WorkspaceDir: workspace,
		InputPath:    input,
		Params:       []string{"--format", "json", "two words"},
		Role:         "intern",
		Config:       directTestConfig([]string{"fake"}, 1),
		Out:          &stdout,
		Err:          &stderr,
	}, directRunDeps{
		executors: map[string]harnessapi.Executor{"fake": fake},
		sink:      telemetry.NoopSink{},
	})
	if err != nil {
		t.Fatalf("runDirect: %v", err)
	}
	if got, want := stdout.String(), "{\"answer\":\"ok\"}\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if len(fake.opts) != 1 {
		t.Fatalf("execute calls = %d, want 1", len(fake.opts))
	}
	opts := fake.opts[0]
	if !opts.DirectRun || opts.LapsEnabled {
		t.Fatalf("run options DirectRun=%v LapsEnabled=%v", opts.DirectRun, opts.LapsEnabled)
	}
	for _, want := range []string{input, `["--format","json","two words"]`, "primary workflow context"} {
		if !strings.Contains(opts.TaskPrompt, want) {
			t.Fatalf("task prompt missing %q:\n%s", want, opts.TaskPrompt)
		}
	}
	for _, statePath := range []string{filepath.Join(workspace, ".laps"), filepath.Join(workspace, ".rally")} {
		if _, err := os.Stat(statePath); !os.IsNotExist(err) {
			t.Fatalf("state path %s exists or stat failed: %v", statePath, err)
		}
	}
}

func TestRunDirectWritesOutputFileAndKeepsStdoutEmpty(t *testing.T) {
	workspace := t.TempDir()
	input := filepath.Join(workspace, "input")
	if err := os.Mkdir(input, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &directFakeExecutor{results: []*harnessapi.TryResult{{Completed: true, Summary: "file result"}}}
	var stdout bytes.Buffer
	output := filepath.Join(workspace, "nested", "result.txt")

	err := runDirect(context.Background(), DirectRunOptions{
		WorkspaceDir: workspace,
		InputPath:    input,
		OutputPath:   output,
		Config:       directTestConfig([]string{"fake"}, 1),
		Out:          &stdout,
		Err:          &bytes.Buffer{},
	}, directRunDeps{
		executors: map[string]harnessapi.Executor{"fake": fake},
		sink:      telemetry.NoopSink{},
	})
	if err != nil {
		t.Fatalf("runDirect: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "file result\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if !strings.Contains(fake.opts[0].TaskPrompt, "Rally does not concatenate or embed") {
		t.Fatalf("directory convention missing:\n%s", fake.opts[0].TaskPrompt)
	}
}

func TestRunDirectRotatesToRouteFallbackOnInvalidModel(t *testing.T) {
	workspace := t.TempDir()
	input := filepath.Join(workspace, "input.md")
	if err := os.WriteFile(input, []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := &directFakeExecutor{
		results: []*harnessapi.TryResult{{
			Completed: false,
			Evidence:  &reliability.FailureEvidence{Category: reliability.CategoryInvalidModel},
		}},
		errs: []error{errors.New("model unavailable")},
	}
	second := &directFakeExecutor{results: []*harnessapi.TryResult{{Completed: true, Summary: "fallback result"}}}
	var stdout, stderr bytes.Buffer

	err := runDirect(context.Background(), DirectRunOptions{
		WorkspaceDir: workspace,
		InputPath:    input,
		Config:       directTestConfig([]string{"first", "second"}, 2),
		Out:          &stdout,
		Err:          &stderr,
	}, directRunDeps{
		executors: map[string]harnessapi.Executor{"first": first, "second": second},
		sink:      telemetry.NoopSink{},
	})
	if err != nil {
		t.Fatalf("runDirect: %v", err)
	}
	if stdout.String() != "fallback result\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if len(first.opts) != 1 || len(second.opts) != 1 {
		t.Fatalf("calls first=%d second=%d, want 1 each", len(first.opts), len(second.opts))
	}
	if !strings.Contains(stderr.String(), "retrying") {
		t.Fatalf("stderr missing retry notice: %q", stderr.String())
	}
}

func TestResolveDirectCandidatesUsesBuiltInRoleDefaultWithoutConfiguredRoutes(t *testing.T) {
	candidates, route, err := resolveDirectCandidates(config.V2Config{
		Harnesses: map[string]*config.HarnessConfig{},
		Routes:    map[string][]string{},
	}, "intern")
	if err != nil {
		t.Fatalf("resolveDirectCandidates: %v", err)
	}
	if route != "intern" {
		t.Fatalf("route = %q, want intern", route)
	}
	if len(candidates) != 1 || candidates[0].Harness != "opencode" {
		t.Fatalf("candidates = %+v, want opencode", candidates)
	}
}
