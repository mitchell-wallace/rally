package relay

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/store"
)

func mixTestResolver(spec string) (agent.ResolvedAgent, error) {
	aliases := map[string]string{
		"ag": "antigravity", "agy": "antigravity", "antigravity": "antigravity",
		"cc": "claude", "claude": "claude",
		"cx": "codex", "codex": "codex",
		"op": "opencode", "opencode": "opencode",
	}
	parts := strings.SplitN(spec, ":", 2)
	harness, ok := aliases[parts[0]]
	if !ok {
		return agent.ResolvedAgent{}, fmt.Errorf("unknown agent alias %q", parts[0])
	}
	if len(parts) == 2 {
		if _, err := strconv.Atoi(parts[1]); err == nil {
			return agent.ResolvedAgent{Harness: harness}, nil
		}
		return agent.ResolvedAgent{Harness: harness, Model: parts[1]}, nil
	}
	return agent.ResolvedAgent{Harness: harness}, nil
}

func newMixTestStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFormatMixLabel(t *testing.T) {
	tests := []struct {
		name   string
		stored string
		want   string
	}{
		{"empty", "", "(empty)"},
		{"routes marker", relaySelectionModeRoutes, "configured routes"},
		{"override with specs", relaySelectionModeOverridePrefix + "cc ag op", "cc ag op"},
		{"override with quotas", relaySelectionModeOverridePrefix + "cc:1 ag:1", "cc:1 ag:1"},
		{"override bare", relaySelectionModeOverridePrefix, "(override)"},
		{"override only whitespace", relaySelectionModeOverridePrefix + "  ", "(override)"},
		{"legacy mix", "cc:1 cx:2", "cc:1 cx:2"},
		{"trims whitespace", "  cc cx  ", "cc cx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatMixLabel(tt.stored); got != tt.want {
				t.Errorf("FormatMixLabel(%q) = %q, want %q", tt.stored, got, tt.want)
			}
		})
	}
}

func TestAgentMixNamedModels(t *testing.T) {
	mix, err := ParseAgentMix([]string{"op:z", "cc:opus"}, Resolver(mixTestResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}

	if len(mix.Cycle) != 2 {
		t.Fatalf("expected 2 cycle entries, got %d", len(mix.Cycle))
	}
	if mix.Cycle[0] != (agent.ResolvedAgent{Harness: "opencode", Model: "z"}) {
		t.Fatalf("cycle[0] = %+v, want {opencode z}", mix.Cycle[0])
	}
	if mix.Cycle[1] != (agent.ResolvedAgent{Harness: "claude", Model: "opus"}) {
		t.Fatalf("cycle[1] = %+v, want {claude opus}", mix.Cycle[1])
	}
	if mix.Weights["opencode"] != 1 {
		t.Fatalf("weights[opencode] = %d, want 1", mix.Weights["opencode"])
	}
	if mix.Weights["claude"] != 1 {
		t.Fatalf("weights[claude] = %d, want 1", mix.Weights["claude"])
	}
	if mix.Label != "op:z cc:opus" {
		t.Fatalf("label = %q, want %q", mix.Label, "op:z cc:opus")
	}
}

func TestAgentMixMixedForms(t *testing.T) {
	mix, err := ParseAgentMix([]string{"cc:2", "op:z"}, Resolver(mixTestResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}

	if len(mix.Cycle) != 3 {
		t.Fatalf("expected 3 cycle entries, got %d: %+v", len(mix.Cycle), mix.Cycle)
	}
	if mix.Cycle[0] != (agent.ResolvedAgent{Harness: "claude"}) {
		t.Fatalf("cycle[0] = %+v, want {claude}", mix.Cycle[0])
	}
	if mix.Cycle[1] != (agent.ResolvedAgent{Harness: "claude"}) {
		t.Fatalf("cycle[1] = %+v, want {claude}", mix.Cycle[1])
	}
	if mix.Cycle[2] != (agent.ResolvedAgent{Harness: "opencode", Model: "z"}) {
		t.Fatalf("cycle[2] = %+v, want {opencode z}", mix.Cycle[2])
	}
	if mix.Label != "claude claude op:z" {
		t.Fatalf("label = %q, want %q", mix.Label, "claude claude op:z")
	}
}

func TestAgentMixMixedNamedAndWeighted(t *testing.T) {
	mix, err := ParseAgentMix([]string{"cc:2", "op:z", "cc:sonnet"}, Resolver(mixTestResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}

	if len(mix.Cycle) != 2 {
		t.Fatalf("expected 2 cycle entries (1 named opencode + 1 named claude), got %d: %+v", len(mix.Cycle), mix.Cycle)
	}
	if mix.Cycle[0] != (agent.ResolvedAgent{Harness: "claude", Model: "sonnet"}) {
		t.Fatalf("cycle[0] = %+v, want {claude sonnet}", mix.Cycle[0])
	}
	if mix.Cycle[1] != (agent.ResolvedAgent{Harness: "opencode", Model: "z"}) {
		t.Fatalf("cycle[1] = %+v, want {opencode z}", mix.Cycle[1])
	}
}

func TestResumeFromStoredLabelWithNamedModels(t *testing.T) {
	resolver := Resolver(mixTestResolver)

	mix1, err := ParseAgentMix([]string{"op:z", "cc:opus"}, resolver)
	if err != nil {
		t.Fatalf("initial ParseAgentMix failed: %v", err)
	}

	mix2, err := ParseAgentMix(strings.Fields(mix1.Label), resolver)
	if err != nil {
		t.Fatalf("re-parse of label %q failed: %v", mix1.Label, err)
	}

	if len(mix1.Cycle) != len(mix2.Cycle) {
		t.Fatalf("cycle length mismatch: %d vs %d", len(mix1.Cycle), len(mix2.Cycle))
	}
	for i := range mix1.Cycle {
		if mix1.Cycle[i] != mix2.Cycle[i] {
			t.Fatalf("cycle[%d] mismatch: %+v vs %+v", i, mix1.Cycle[i], mix2.Cycle[i])
		}
	}
	if mix1.Label != mix2.Label {
		t.Fatalf("label mismatch: %q vs %q", mix1.Label, mix2.Label)
	}
}

func TestResumeFromStoredLabelWithMixedForms(t *testing.T) {
	resolver := Resolver(mixTestResolver)

	mix1, err := ParseAgentMix([]string{"cc:2", "op:z"}, resolver)
	if err != nil {
		t.Fatalf("initial ParseAgentMix failed: %v", err)
	}

	mix2, err := ParseAgentMix(strings.Fields(mix1.Label), resolver)
	if err != nil {
		t.Fatalf("re-parse of label %q failed: %v", mix1.Label, err)
	}

	if len(mix1.Cycle) != len(mix2.Cycle) {
		t.Fatalf("cycle length mismatch: %d vs %d", len(mix1.Cycle), len(mix2.Cycle))
	}
	for i := range mix1.Cycle {
		if mix1.Cycle[i] != mix2.Cycle[i] {
			t.Fatalf("cycle[%d] mismatch: %+v vs %+v", i, mix1.Cycle[i], mix2.Cycle[i])
		}
	}
}

// TestResumeRoundTripWithRealResolver uses the real config resolver to catch
// the case where resolved model strings look like identifier-like keys (e.g.
// "claude-opus-4-7") and must not be stored literally in the label.
func TestResumeRoundTripWithRealResolver(t *testing.T) {
	cfg := config.V2Config{
		Harnesses: map[string]*config.HarnessConfig{
			"cc": {Models: map[string]string{
				"opus": "claude-opus-4-7",
			}},
			"op": {Models: map[string]string{
				"z": "zai-coding-plan/glm-5.1",
			}},
		},
	}
	resolver := Resolver(cfg.ResolveAgent)

	mix1, err := ParseAgentMix([]string{"cc:opus", "op:z"}, resolver)
	if err != nil {
		t.Fatalf("initial ParseAgentMix failed: %v", err)
	}

	mix2, err := ParseAgentMix(strings.Fields(mix1.Label), resolver)
	if err != nil {
		t.Fatalf("re-parse of label %q failed: %v — label must store short alias, not resolved model string", mix1.Label, err)
	}

	if len(mix1.Cycle) != len(mix2.Cycle) {
		t.Fatalf("cycle length mismatch after resume: %d vs %d", len(mix1.Cycle), len(mix2.Cycle))
	}
	for i := range mix1.Cycle {
		if mix1.Cycle[i] != mix2.Cycle[i] {
			t.Fatalf("cycle[%d] mismatch after resume: %+v vs %+v", i, mix1.Cycle[i], mix2.Cycle[i])
		}
	}
	if mix1.Label != mix2.Label {
		t.Fatalf("label changed after resume: %q -> %q", mix1.Label, mix2.Label)
	}
}

func TestPerHarnessModelPauseIsolation(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)

	s := newMixTestStore(t, rallyDir)
	resilience := NewResilience(s)

	// Pause only claude:opus - claude:sonnet should remain active.
	if err := resilience.PauseAgent(ResilienceKey{Harness: "claude", Model: "opus"}, 1); err != nil {
		t.Fatalf("PauseAgent failed: %v", err)
	}

	mix, err := ParseAgentMix([]string{"cc:opus", "cc:sonnet"}, Resolver(mixTestResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}

	if len(mix.Cycle) != 2 {
		t.Fatalf("expected 2 cycle entries, got %d", len(mix.Cycle))
	}
	if mix.Cycle[0].Model != "opus" || mix.Cycle[1].Model != "sonnet" {
		t.Fatalf("expected opus+sonnet models, got %+v", mix.Cycle)
	}

	// claude:sonnet is still active, so SelectActiveAgent should succeed.
	picked, _, _, err := resilience.SelectActiveAgent(mix, 0)
	if err != nil {
		t.Fatalf("SelectActiveAgent should succeed with sonnet active: %v", err)
	}
	if picked.Model != "sonnet" {
		t.Fatalf("expected sonnet (active model), got %s:%s", picked.Harness, picked.Model)
	}

	// Now pause sonnet too - all models paused.
	if err := resilience.PauseAgent(ResilienceKey{Harness: "claude", Model: "sonnet"}, 1); err != nil {
		t.Fatalf("PauseAgent(sonnet) failed: %v", err)
	}

	_, _, _, err = resilience.SelectActiveAgent(mix, 0)
	if err == nil {
		t.Fatal("expected error when all agent models are paused")
	}
	if err.Error() != "all agents paused" {
		t.Fatalf("expected 'all agents paused' error, got %q", err.Error())
	}
}

func TestParseAgentMixThirdColonSegmentRejected(t *testing.T) {
	_, err := ParseAgentMix([]string{"cc:opus:2"}, Resolver(mixTestResolver))
	if err == nil {
		t.Fatal("expected error for third colon segment")
	}
	if !strings.Contains(err.Error(), "weight-on-named-model") {
		t.Fatalf("error = %q, want mention of weight-on-named-model", err.Error())
	}
}

func TestParseAgentMixUnresolvedModelError(t *testing.T) {
	strictResolver := func(spec string) (agent.ResolvedAgent, error) {
		ra, err := mixTestResolver(spec)
		if err != nil {
			return ra, err
		}
		if ra.Model != "" && ra.Model != "z" && ra.Model != "opus" && ra.Model != "sonnet" {
			return agent.ResolvedAgent{}, fmt.Errorf("unknown model %q for harness %q", ra.Model, ra.Harness)
		}
		return ra, nil
	}
	_, err := ParseAgentMix([]string{"cc:unknown_model"}, Resolver(strictResolver))
	if err == nil {
		t.Fatal("expected error for unresolved model name")
	}
	if !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("error = %q, want mention of unknown model", err.Error())
	}
}

func TestParseAgentMixUnknownHarnessError(t *testing.T) {
	_, err := ParseAgentMix([]string{"unknown:foo"}, Resolver(mixTestResolver))
	if err == nil {
		t.Fatal("expected error for unknown harness")
	}
	if !strings.Contains(err.Error(), "unknown agent alias") {
		t.Fatalf("error = %q, want mention of unknown agent alias", err.Error())
	}
}

func TestParseAgentMixBareAlias(t *testing.T) {
	mix, err := ParseAgentMix([]string{"cc"}, Resolver(mixTestResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}
	if len(mix.Cycle) != 1 {
		t.Fatalf("expected 1 cycle entry, got %d", len(mix.Cycle))
	}
	if mix.Cycle[0] != (agent.ResolvedAgent{Harness: "claude"}) {
		t.Fatalf("cycle[0] = %+v, want {claude}", mix.Cycle[0])
	}
	if mix.Label != "claude" {
		t.Fatalf("label = %q, want %q", mix.Label, "claude")
	}
}

func TestParseAgentMixEmptySpecsDefaultMix(t *testing.T) {
	mix, err := ParseAgentMix(nil, Resolver(mixTestResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix with nil specs failed: %v", err)
	}
	if len(mix.Cycle) != 3 {
		t.Fatalf("expected 3 cycle entries (1 claude + 2 codex), got %d: %+v", len(mix.Cycle), mix.Cycle)
	}
	if mix.Cycle[0] != (agent.ResolvedAgent{Harness: "claude"}) {
		t.Fatalf("cycle[0] = %+v, want {claude}", mix.Cycle[0])
	}
	if mix.Cycle[1] != (agent.ResolvedAgent{Harness: "codex"}) {
		t.Fatalf("cycle[1] = %+v, want {codex}", mix.Cycle[1])
	}
	if mix.Cycle[2] != (agent.ResolvedAgent{Harness: "codex"}) {
		t.Fatalf("cycle[2] = %+v, want {codex}", mix.Cycle[2])
	}
	if mix.Weights["claude"] != 1 {
		t.Fatalf("weights[claude] = %d, want 1", mix.Weights["claude"])
	}
	if mix.Weights["codex"] != 2 {
		t.Fatalf("weights[codex] = %d, want 2", mix.Weights["codex"])
	}
}

func TestParseAgentMixNilResolverBareAlias(t *testing.T) {
	mix, err := ParseAgentMix([]string{"cc"}, nil)
	if err != nil {
		t.Fatalf("ParseAgentMix with nil resolver failed: %v", err)
	}
	if len(mix.Cycle) != 1 {
		t.Fatalf("expected 1 cycle entry, got %d", len(mix.Cycle))
	}
	if mix.Cycle[0] != (agent.ResolvedAgent{Harness: "claude", Model: ""}) {
		t.Fatalf("cycle[0] = %+v, want {claude}", mix.Cycle[0])
	}
	if mix.Label != "claude" {
		t.Fatalf("label = %q, want %q", mix.Label, "claude")
	}
}

func TestParseAgentMixNilResolverWithWeight(t *testing.T) {
	mix, err := ParseAgentMix([]string{"cc:2", "cx"}, nil)
	if err != nil {
		t.Fatalf("ParseAgentMix with nil resolver failed: %v", err)
	}
	if len(mix.Cycle) != 3 {
		t.Fatalf("expected 3 cycle entries, got %d", len(mix.Cycle))
	}
	if mix.Weights["claude"] != 2 {
		t.Fatalf("weights[claude] = %d, want 2", mix.Weights["claude"])
	}
	if mix.Weights["codex"] != 1 {
		t.Fatalf("weights[codex] = %d, want 1", mix.Weights["codex"])
	}
}

func TestParseAgentMixNilResolverUnknownAlias(t *testing.T) {
	_, err := ParseAgentMix([]string{"unknown"}, nil)
	if err == nil {
		t.Fatal("expected error for unknown alias with nil resolver")
	}
	if !strings.Contains(err.Error(), "unknown agent alias") {
		t.Fatalf("error = %q, want mention of unknown agent alias", err.Error())
	}
}

func TestParseAgentMixNilResolverInvalidWeight(t *testing.T) {
	_, err := ParseAgentMix([]string{"cc:abc"}, nil)
	if err == nil {
		t.Fatal("expected error for invalid weight with nil resolver")
	}
	if !strings.Contains(err.Error(), "invalid agent weight") {
		t.Fatalf("error = %q, want mention of invalid agent weight", err.Error())
	}
}

func TestParseAgentMixAllFormsCombined(t *testing.T) {
	mix, err := ParseAgentMix([]string{"cc", "cx:2", "op:z"}, Resolver(mixTestResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}
	if len(mix.Cycle) != 4 {
		t.Fatalf("expected 4 cycle entries, got %d: %+v", len(mix.Cycle), mix.Cycle)
	}
	if mix.Cycle[0] != (agent.ResolvedAgent{Harness: "claude"}) {
		t.Fatalf("cycle[0] = %+v, want {claude}", mix.Cycle[0])
	}
	if mix.Cycle[1] != (agent.ResolvedAgent{Harness: "codex"}) {
		t.Fatalf("cycle[1] = %+v, want {codex}", mix.Cycle[1])
	}
	if mix.Cycle[2] != (agent.ResolvedAgent{Harness: "codex"}) {
		t.Fatalf("cycle[2] = %+v, want {codex}", mix.Cycle[2])
	}
	if mix.Cycle[3] != (agent.ResolvedAgent{Harness: "opencode", Model: "z"}) {
		t.Fatalf("cycle[3] = %+v, want {opencode z}", mix.Cycle[3])
	}
}
