package harnessapi

import (
	"slices"
	"strings"
	"testing"
)

func TestApplyReasoningEffort_SupportedHarnesses(t *testing.T) {
	cases := []struct {
		harness  string
		effort   string
		wantArgs []string
	}{
		{"codex", "high", []string{"-c", "model_reasoning_effort=high"}},
		{"claude", "xhigh", []string{"--effort", "xhigh"}},
		{"opencode", "max", []string{"--variant", "max"}},
	}
	for _, tc := range cases {
		t.Run(tc.harness, func(t *testing.T) {
			args, warning := ApplyReasoningEffort([]string{"run"}, tc.harness, tc.effort)
			if warning != "" {
				t.Fatalf("warning = %q, want none for a documented value", warning)
			}
			want := append([]string{"run"}, tc.wantArgs...)
			if !slices.Equal(args, want) {
				t.Fatalf("args = %v, want %v", args, want)
			}
		})
	}
}

func TestApplyReasoningEffort_UnknownValueWarnsNotFails(t *testing.T) {
	args, warning := ApplyReasoningEffort([]string{"exec"}, "codex", "bogus")
	// The value is still injected — rally must not pre-empt the provider's own
	// validation (spike-1: codex defers rejection to the API).
	want := []string{"exec", "-c", "model_reasoning_effort=bogus"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	if warning == "" {
		t.Fatal("warning = empty, want a pass-through warning for an unknown effort")
	}
	if !strings.Contains(warning, "bogus") || !strings.Contains(warning, "high") {
		t.Fatalf("warning = %q, want offending value and documented set", warning)
	}
}

func TestApplyReasoningEffort_OpencodeVariantNotValidated(t *testing.T) {
	// opencode variants are provider-specific; an unrecognised value is injected
	// without a warning because there is no enumerable set to check against.
	args, warning := ApplyReasoningEffort([]string{"run"}, "opencode", "wibble")
	if warning != "" {
		t.Fatalf("warning = %q, want none for provider-specific variant", warning)
	}
	if !slices.Equal(args, []string{"run", "--variant", "wibble"}) {
		t.Fatalf("args = %v, want injected variant", args)
	}
}

func TestApplyReasoningEffort_UnsupportedHarnessesWarnAndSkip(t *testing.T) {
	for _, harness := range []string{"antigravity"} {
		t.Run(harness, func(t *testing.T) {
			base := []string{"--prompt", "hi"}
			args, warning := ApplyReasoningEffort(base, harness, "high")
			if !slices.Equal(args, base) {
				t.Fatalf("args = %v, want unchanged (injection skipped)", args)
			}
			if warning == "" {
				t.Fatalf("warning = empty, want an unsupported-harness warning")
			}
			if !strings.Contains(warning, "high") {
				t.Fatalf("warning = %q, want the ignored effort value", warning)
			}
		})
	}
}

func TestApplyReasoningEffort_UnknownHarnessWarnsAndSkips(t *testing.T) {
	args, warning := ApplyReasoningEffort([]string{"go"}, "droid", "high")
	if !slices.Equal(args, []string{"go"}) {
		t.Fatalf("args = %v, want unchanged for unknown harness", args)
	}
	if !strings.Contains(warning, "droid") {
		t.Fatalf("warning = %q, want the harness name", warning)
	}
}

func TestApplyReasoningEffort_EmptyEffortNoop(t *testing.T) {
	for _, harness := range []string{"codex", "antigravity"} {
		args, warning := ApplyReasoningEffort([]string{"x"}, harness, "  ")
		if warning != "" {
			t.Fatalf("[%s] warning = %q, want none for empty effort", harness, warning)
		}
		if !slices.Equal(args, []string{"x"}) {
			t.Fatalf("[%s] args = %v, want unchanged", harness, args)
		}
	}
}

func TestIsKnownReasoningEffort(t *testing.T) {
	for _, effort := range []string{"high", "MAX", " minimal ", "xhigh"} {
		if !IsKnownReasoningEffort(effort) {
			t.Errorf("IsKnownReasoningEffort(%q) = false, want true", effort)
		}
	}
	for _, effort := range []string{"", "ludicrous", "turbo"} {
		if IsKnownReasoningEffort(effort) {
			t.Errorf("IsKnownReasoningEffort(%q) = true, want false", effort)
		}
	}
}
