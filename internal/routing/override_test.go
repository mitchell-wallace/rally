package routing

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent"
)

func overrideResolver(spec string) (agent.ResolvedAgent, error) {
	switch spec {
	case "op:z":
		return agent.ResolvedAgent{Harness: "opencode", Model: "zai-coding-plan/glm-5.1"}, nil
	case "op:gk":
		return agent.ResolvedAgent{Harness: "opencode", Model: "google/gemini-2.5-pro"}, nil
	case "gemini-pro":
		return agent.ResolvedAgent{Harness: "gemini", Model: "gemini-2.5-pro"}, nil
	}

	parts := strings.SplitN(spec, ":", 2)
	if len(parts) == 0 || parts[0] == "" {
		return agent.ResolvedAgent{}, fmt.Errorf("unknown agent alias %q", spec)
	}

	aliases := map[string]string{
		"cc":       "claude",
		"claude":   "claude",
		"cx":       "codex",
		"codex":    "codex",
		"ge":       "gemini",
		"gemini":   "gemini",
		"op":       "opencode",
		"opencode": "opencode",
	}
	harness, ok := aliases[parts[0]]
	if !ok {
		return agent.ResolvedAgent{}, fmt.Errorf("unknown agent alias %q", parts[0])
	}
	if len(parts) == 1 {
		return agent.ResolvedAgent{Harness: harness}, nil
	}
	return agent.ResolvedAgent{Harness: harness, Model: parts[1]}, nil
}

func TestBuildOverrideRoute_ResolvesDirectEntries(t *testing.T) {
	override, err := BuildOverrideRoute("override", []string{"op:z:4", "claude:opus-4.7"}, nil, overrideResolver)
	if err != nil {
		t.Fatalf("BuildOverrideRoute() error = %v", err)
	}

	if len(override.Entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(override.Entries))
	}
	if override.Entries[0].Spec != "opencode:zai-coding-plan/glm-5.1" {
		t.Fatalf("entry[0].Spec = %q", override.Entries[0].Spec)
	}
	if !override.Entries[0].HasQuota || override.Entries[0].QuotaMin != 4 || override.Entries[0].QuotaMax != 4 {
		t.Fatalf("entry[0] quota = %+v, want single quota 4", override.Entries[0])
	}
	if override.Entries[1].Spec != "claude:opus-4.7" {
		t.Fatalf("entry[1].Spec = %q, want claude:opus-4.7", override.Entries[1].Spec)
	}
}

func TestBuildOverrideRoute_InlinesRoleReferenceWithoutQuota(t *testing.T) {
	override, err := BuildOverrideRoute("override", []string{"claude:opus-4.7", "SENIOR"}, map[string][]string{
		"SENIOR": {"cx:gpt-5.5", "claude:opus-4.7"},
	}, overrideResolver)
	if err != nil {
		t.Fatalf("BuildOverrideRoute() error = %v", err)
	}

	if override.HasDynamicRoleRefs() {
		t.Fatal("override should not keep dynamic role refs when quota is absent")
	}
	if len(override.Entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(override.Entries))
	}
	got := []string{override.Entries[0].Spec, override.Entries[1].Spec, override.Entries[2].Spec}
	want := []string{"claude:opus-4.7", "codex:gpt-5.5", "claude:opus-4.7"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBuildOverrideRoute_RoleReferenceWithRangeQuotaRejected(t *testing.T) {
	_, err := BuildOverrideRoute("override", []string{"DEFAULT:1-2"}, map[string][]string{
		"default": {"claude:opus-4.7"},
	}, overrideResolver)
	if err == nil {
		t.Fatal("expected error for role ref range quota")
	}
	if !strings.Contains(err.Error(), "single numeric quota") {
		t.Fatalf("error = %v, want single numeric quota message", err)
	}
}

func TestBuildOverrideRoute_Scenario5_SingleDirectOverride(t *testing.T) {
	override, err := BuildOverrideRoute("override", []string{"op:opencode-go/fancy-new-model"}, map[string][]string{
		"default": {"claude:opus-4.7"},
		"ROLEA":   {"codex:gpt-5.5"},
		"ROLEB":   {"gemini:gemini-2.5-pro"},
	}, overrideResolver)
	if err != nil {
		t.Fatalf("BuildOverrideRoute() error = %v", err)
	}

	s := NewScheduler(override.Entries)
	st := mustNext(t, s)
	resolved, err := override.ResolveSelection(st.Entry)
	if err != nil {
		t.Fatalf("ResolveSelection() error = %v", err)
	}
	if resolved.Spec != "opencode:opencode-go/fancy-new-model" {
		t.Fatalf("first resolved spec = %q", resolved.Spec)
	}

	st = mustNext(t, s)
	resolved, err = override.ResolveSelection(st.Entry)
	if err != nil {
		t.Fatalf("ResolveSelection() error = %v", err)
	}
	if resolved.Spec != "opencode:opencode-go/fancy-new-model" {
		t.Fatalf("second resolved spec = %q", resolved.Spec)
	}
}

func TestBuildOverrideRoute_Scenario6_RoleReferenceAdvancesCursor(t *testing.T) {
	override, err := BuildOverrideRoute("override", []string{"op:opencode-go/fancy-new-model", "DEFAULT:1"}, map[string][]string{
		"default": {"claude:opus-4.7", "codex:gpt-5.5"},
		"ROLEA":   {"claude:sonnet-4.6"},
		"ROLEB":   {"gemini:gemini-2.5-pro"},
	}, overrideResolver)
	if err != nil {
		t.Fatalf("BuildOverrideRoute() error = %v", err)
	}

	if !override.HasDynamicRoleRefs() {
		t.Fatal("override should keep dynamic role ref for DEFAULT:1")
	}

	s := NewScheduler(override.Entries)

	fancy := mustNext(t, s)
	resolved, err := override.ResolveSelection(fancy.Entry)
	if err != nil {
		t.Fatalf("ResolveSelection() error = %v", err)
	}
	if resolved.Spec != "opencode:opencode-go/fancy-new-model" {
		t.Fatalf("first pick = %q, want fancy", resolved.Spec)
	}

	s.OnAgentFailed(fancy, "retry-budget-exhausted")

	defaultPick := mustNext(t, s)
	resolved, err = override.ResolveSelection(defaultPick.Entry)
	if err != nil {
		t.Fatalf("ResolveSelection() error = %v", err)
	}
	if resolved.Spec != "claude:opus-4.7" {
		t.Fatalf("default visit 1 = %q, want claude:opus-4.7", resolved.Spec)
	}

	s.OnAgentRecovered(fancy)

	fancy = mustNext(t, s)
	resolved, err = override.ResolveSelection(fancy.Entry)
	if err != nil {
		t.Fatalf("ResolveSelection() error = %v", err)
	}
	if resolved.Spec != "opencode:opencode-go/fancy-new-model" {
		t.Fatalf("second fancy pick = %q, want fancy", resolved.Spec)
	}

	s.OnAgentFailed(fancy, "retry-budget-exhausted")

	defaultPick = mustNext(t, s)
	resolved, err = override.ResolveSelection(defaultPick.Entry)
	if err != nil {
		t.Fatalf("ResolveSelection() error = %v", err)
	}
	if resolved.Spec != "codex:gpt-5.5" {
		t.Fatalf("default visit 2 = %q, want codex:gpt-5.5", resolved.Spec)
	}
}
