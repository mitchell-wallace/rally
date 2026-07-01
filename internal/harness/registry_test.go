package harness

import (
	"sort"
	"testing"

	"github.com/mitchell-wallace/rally/internal/harness/antigravity"
	"github.com/mitchell-wallace/rally/internal/harness/claude"
	"github.com/mitchell-wallace/rally/internal/harness/codex"
	"github.com/mitchell-wallace/rally/internal/harness/generic"
	"github.com/mitchell-wallace/rally/internal/harness/opencode"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
)

// TestBuildExecutors_BuiltInCanonicalNames confirms the registry produces the
// four built-in adapters keyed by their canonical names, each constructed via
// its package constructor over the concrete Executor type and wired with its
// configured model default (tasks.md 5.4).
func TestBuildExecutors_BuiltInCanonicalNames(t *testing.T) {
	executors := BuildExecutors(Config{
		ClaudeModel:      "claude-default",
		CodexModel:       "codex-default",
		OpenCodeModel:    "opencode-default",
		AntigravityModel: "antigravity-default",
	})

	want := []string{"antigravity", "claude", "codex", "opencode"}
	for _, name := range want {
		ex, ok := executors[name]
		if !ok {
			t.Errorf("missing built-in executor %q", name)
			continue
		}
		switch name {
		case "claude":
			c, ok := ex.(*claude.Executor)
			if !ok {
				t.Errorf("claude executor is %T, want *claude.Executor", ex)
			} else if c.Model != "claude-default" {
				t.Errorf("claude.Model = %q, want %q", c.Model, "claude-default")
			}
		case "codex":
			c, ok := ex.(*codex.Executor)
			if !ok {
				t.Errorf("codex executor is %T, want *codex.Executor", ex)
			} else if c.Model != "codex-default" {
				t.Errorf("codex.Model = %q, want %q", c.Model, "codex-default")
			}
		case "opencode":
			c, ok := ex.(*opencode.Executor)
			if !ok {
				t.Errorf("opencode executor is %T, want *opencode.Executor", ex)
			} else if c.Model != "opencode-default" {
				t.Errorf("opencode.Model = %q, want %q", c.Model, "opencode-default")
			}
		case "antigravity":
			c, ok := ex.(*antigravity.Executor)
			if !ok {
				t.Errorf("antigravity executor is %T, want *antigravity.Executor", ex)
			} else if c.Model != "antigravity-default" {
				t.Errorf("antigravity.Model = %q, want %q", c.Model, "antigravity-default")
			}
		}
	}

	if len(executors) != len(want) {
		t.Errorf("executor count = %d, want %d (built-ins only): %v", len(executors), len(want), sortedKeys(executors))
	}
}

// TestBuildExecutors_GenericWithCommand confirms a Custom entry that declares a
// command registers a generic adapter carrying its command and command-spec
// fields, and that one without a command is skipped (tasks.md 5.4).
func TestBuildExecutors_GenericWithCommand(t *testing.T) {
	emptyFlag := ""
	executors := BuildExecutors(Config{
		Custom: map[string]GenericConfig{
			"mycli": {
				Command:        []string{"mycli", "run"},
				ModelFlag:      &emptyFlag,
				OutputStrategy: "tail",
				OutputLines:    7,
				TailStream:     "stdout",
				Model:          "mycli-default",
			},
			"modelsonly": {
				// No Command: defines model aliases only, must not register an executor.
				Model: "ignored",
			},
		},
	})

	ex, ok := executors["mycli"]
	if !ok {
		t.Fatalf("missing generic executor %q", "mycli")
	}
	g, ok := ex.(*generic.Executor)
	if !ok {
		t.Fatalf("mycli executor is %T, want *generic.Executor", ex)
	}
	if !equalStrings(g.Command, []string{"mycli", "run"}) {
		t.Errorf("generic.Command = %v, want [mycli run]", g.Command)
	}
	if g.ModelFlag != &emptyFlag {
		t.Errorf("generic.ModelFlag = %p, want the passed *string %p (empty string preserved)", g.ModelFlag, &emptyFlag)
	}
	if g.OutputStrategy != "tail" {
		t.Errorf("generic.OutputStrategy = %q, want %q", g.OutputStrategy, "tail")
	}
	if g.OutputLines != 7 {
		t.Errorf("generic.OutputLines = %d, want 7", g.OutputLines)
	}
	if g.TailStream != "stdout" {
		t.Errorf("generic.TailStream = %q, want %q", g.TailStream, "stdout")
	}
	if g.Model != "mycli-default" {
		t.Errorf("generic.Model = %q, want %q", g.Model, "mycli-default")
	}

	if _, ok := executors["modelsonly"]; ok {
		t.Errorf("modelsonly (no command) should not register an executor")
	}

	if want := 5; len(executors) != want {
		t.Errorf("executor count = %d, want %d (4 built-ins + mycli): %v", len(executors), want, sortedKeys(executors))
	}
}

// TestBuildExecutors_NilModelFlagAbsent confirms ModelFlag nil (absent) threads
// through to the generic adapter distinct from an empty-string pointer, the
// semantic the config->harness.Config translation must preserve.
func TestBuildExecutors_NilModelFlagAbsent(t *testing.T) {
	executors := BuildExecutors(Config{
		Custom: map[string]GenericConfig{
			"absent": {Command: []string{"absent"}},
			"empty":  {Command: []string{"empty"}, ModelFlag: strPtr("")},
		},
	})

	absent, ok := executors["absent"].(*generic.Executor)
	if !ok {
		t.Fatalf("absent executor is %T, want *generic.Executor", executors["absent"])
	}
	if absent.ModelFlag != nil {
		t.Errorf("absent generic.ModelFlag = %v, want nil (absent distinct from empty)", absent.ModelFlag)
	}

	empty, ok := executors["empty"].(*generic.Executor)
	if !ok {
		t.Fatalf("empty executor is %T, want *generic.Executor", executors["empty"])
	}
	if empty.ModelFlag == nil {
		t.Errorf("empty generic.ModelFlag = nil, want non-nil pointer to empty string")
	} else if *empty.ModelFlag != "" {
		t.Errorf("empty generic.ModelFlag value = %q, want empty string", *empty.ModelFlag)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func strPtr(s string) *string { return &s }

func sortedKeys(m map[string]harnessapi.Executor) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
