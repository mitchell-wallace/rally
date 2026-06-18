package routing

import (
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent"
)

func TestApplyRoleReasoningFallback_ExplicitModelWins(t *testing.T) {
	called := false
	got, err := ApplyRoleReasoningFallback(
		agent.ResolvedAgent{Harness: "codex", Model: "gpt-5.5"},
		ParsedEntry{Raw: "cx:g55", Spec: "codex:gpt-5.5", ExplicitModel: true},
		"VERIFY",
		map[string]string{"verify": "g55-xh"},
		func(role, selectedHarness, preference string) (string, string, error) {
			called = true
			return "gpt-5.5-high", "", nil
		},
	)
	if err != nil {
		t.Fatalf("ApplyRoleReasoningFallback() error = %v", err)
	}
	if called {
		t.Fatal("reasoning resolver was called for an explicit route model")
	}
	if got.Model != "gpt-5.5" {
		t.Fatalf("Model = %q, want explicit route model", got.Model)
	}
	if got.ReasoningEffort != "" {
		t.Fatalf("ReasoningEffort = %q, want empty", got.ReasoningEffort)
	}
}

func TestApplyRoleReasoningFallback_RoleAliasResolvesForSelectedHarness(t *testing.T) {
	got, err := ApplyRoleReasoningFallback(
		agent.ResolvedAgent{Harness: "codex", Model: "gpt-5.5"},
		ParsedEntry{Raw: "cx", Spec: "codex:gpt-5.5"},
		"VERIFY",
		map[string]string{"verify": "g55-xh"},
		func(role, selectedHarness, preference string) (string, string, error) {
			if role != "VERIFY" {
				t.Fatalf("role = %q, want VERIFY", role)
			}
			if selectedHarness != "codex" {
				t.Fatalf("selectedHarness = %q, want codex", selectedHarness)
			}
			if preference != "g55-xh" {
				t.Fatalf("preference = %q, want g55-xh", preference)
			}
			return "gpt-5.5-high", "", nil
		},
	)
	if err != nil {
		t.Fatalf("ApplyRoleReasoningFallback() error = %v", err)
	}
	if got.Model != "gpt-5.5-high" {
		t.Fatalf("Model = %q, want role alias model", got.Model)
	}
}

func TestApplyRoleReasoningFallback_EffortPropagates(t *testing.T) {
	got, err := ApplyRoleReasoningFallback(
		agent.ResolvedAgent{Harness: "codex", Model: "gpt-5.5"},
		ParsedEntry{Raw: "cx", Spec: "codex:gpt-5.5"},
		"verify",
		map[string]string{"VERIFY": "high"},
		func(role, selectedHarness, preference string) (string, string, error) {
			return "", preference, nil
		},
	)
	if err != nil {
		t.Fatalf("ApplyRoleReasoningFallback() error = %v", err)
	}
	if got.Model != "gpt-5.5" {
		t.Fatalf("Model = %q, want original model", got.Model)
	}
	if got.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want high", got.ReasoningEffort)
	}
}
