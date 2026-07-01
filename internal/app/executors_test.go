package app

import (
	"testing"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/harness/generic"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
)

// TestBuildExecutors_Parity confirms app.BuildExecutors still yields the same
// executor set and keys the pre-change inline map produced for a representative
// config: the four built-in canonical names plus one generic adapter per harness
// entry that declares a command, with generic-harness model defaults absent
// (tasks.md 5.4).
func TestBuildExecutors_Parity(t *testing.T) {
	emptyFlag := ""
	cfg := config.V2Config{
		ClaudeModel:      "claude-default",
		CodexModel:       "codex-default",
		OpenCodeModel:    "opencode-default",
		AntigravityModel: "antigravity-default",
		Harnesses: map[string]*config.HarnessConfig{
			"mycli": {
				Command:        []string{"mycli", "run"},
				ModelFlag:      &emptyFlag,
				OutputStrategy: "tail",
				OutputLines:    7,
				TailStream:     "stdout",
			},
			"modelsonly": {
				// No Command: defines model aliases only; no executor registered.
			},
		},
	}

	executors := BuildExecutors(cfg)

	for _, name := range []string{"antigravity", "claude", "codex", "opencode"} {
		if _, ok := executors[name]; !ok {
			t.Errorf("missing built-in executor %q", name)
		}
	}

	g, ok := executors["mycli"].(*generic.Executor)
	if !ok {
		t.Fatalf("mycli executor is %T, want *generic.Executor", executors["mycli"])
	}
	if g.ModelFlag != &emptyFlag {
		t.Errorf("mycli ModelFlag pointer not preserved through config->harness.Config translation: got %p, want %p", g.ModelFlag, &emptyFlag)
	}
	// The config shape has no generic-harness default-model field, so the mapper
	// must leave GenericConfig.Model empty (not "" from a programmatic default).
	if g.Model != "" {
		t.Errorf("mycli Model = %q, want empty (no generic default-model field in config)", g.Model)
	}

	if _, ok := executors["modelsonly"]; ok {
		t.Errorf("modelsonly (no command) should not register an executor")
	}

	if want := 5; len(executors) != want {
		t.Errorf("executor count = %d, want %d (4 built-ins + mycli): %v", len(executors), want, executorKeys(executors))
	}
}

// TestBuildExecutors_PreservesModelFlagAbsent confirms the config->harness.Config
// mapper preserves ModelFlag absent (nil) versus empty-string pointer semantics.
func TestBuildExecutors_PreservesModelFlagAbsent(t *testing.T) {
	cfg := config.V2Config{
		Harnesses: map[string]*config.HarnessConfig{
			"absent": {Command: []string{"absent"}}, // ModelFlag nil
			"empty":  {Command: []string{"empty"}, ModelFlag: strPtr("")},
		},
	}

	executors := BuildExecutors(cfg)

	absent, ok := executors["absent"].(*generic.Executor)
	if !ok {
		t.Fatalf("absent executor is %T, want *generic.Executor", executors["absent"])
	}
	if absent.ModelFlag != nil {
		t.Errorf("absent ModelFlag = %v, want nil (absent distinct from empty string)", absent.ModelFlag)
	}

	empty, ok := executors["empty"].(*generic.Executor)
	if !ok {
		t.Fatalf("empty executor is %T, want *generic.Executor", executors["empty"])
	}
	if empty.ModelFlag == nil {
		t.Fatalf("empty ModelFlag = nil, want non-nil pointer to empty string")
	}
	if *empty.ModelFlag != "" {
		t.Errorf("empty ModelFlag value = %q, want empty string", *empty.ModelFlag)
	}
}

func strPtr(s string) *string { return &s }

func executorKeys(m map[string]harnessapi.Executor) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
