package config

import (
	"strings"
	"testing"
)

func TestResolveAgent_RemovedGeminiAliasErrors(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{
		"gemini": {Models: map[string]string{"pro": "gemini-3.1-pro-preview"}},
		"ge":     {Models: map[string]string{"pro": "gemini-3.1-pro-preview"}},
	}}
	for _, spec := range []string{"ge", "ge:pro", "gemini", "gemini:pro"} {
		t.Run(spec, func(t *testing.T) {
			_, err := cfg.ResolveAgent(spec)
			alias, ok := RemovedGeminiAlias(err)
			if !ok {
				t.Fatalf("ResolveAgent(%q) error = %v, want removed gemini alias error", spec, err)
			}
			if alias != strings.Split(spec, ":")[0] {
				t.Fatalf("removed alias = %q, want %q", alias, strings.Split(spec, ":")[0])
			}
			if !strings.Contains(err.Error(), "antigravity") {
				t.Fatalf("error = %q, want antigravity guidance", err.Error())
			}
		})
	}
}

func TestResolveAgent_BareAlias_NoDefault(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{}}
	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "claude" {
		t.Errorf("Harness = %q, want 'claude'", resolved.Harness)
	}
	if resolved.Model != "" {
		t.Errorf("Model = %q, want empty when no default configured", resolved.Model)
	}
}

func TestResolveAgent_BareAlias_WithDefault(t *testing.T) {
	cfg := V2Config{ClaudeModel: "sonnet-4", Harnesses: map[string]*HarnessConfig{}}
	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "claude" {
		t.Errorf("Harness = %q, want 'claude'", resolved.Harness)
	}
	if resolved.Model != "sonnet-4" {
		t.Errorf("Model = %q, want 'sonnet-4' from config default", resolved.Model)
	}
}

func TestResolveAgent_WeightPassthrough(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{}}
	resolved, err := cfg.ResolveAgent("cc:2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "claude" {
		t.Errorf("Harness = %q, want 'claude'", resolved.Harness)
	}
	if resolved.Model != "" {
		t.Errorf("Model = %q, want empty for weight passthrough with no default", resolved.Model)
	}
}

func TestResolveAgent_WeightPassthrough_WithDefault(t *testing.T) {
	cfg := V2Config{ClaudeModel: "sonnet-4", Harnesses: map[string]*HarnessConfig{}}
	resolved, err := cfg.ResolveAgent("cc:2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "claude" {
		t.Errorf("Harness = %q, want 'claude'", resolved.Harness)
	}
	if resolved.Model != "sonnet-4" {
		t.Errorf("Model = %q, want 'sonnet-4' from config default", resolved.Model)
	}
}

func TestResolveAgent_NamedModel(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"op": {Models: map[string]string{
				"z":  "zai-coding-plan/glm-5.1",
				"gk": "opencode-go/kimi-k2.6",
			}},
		},
	}
	resolved, err := cfg.ResolveAgent("op:z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "opencode" {
		t.Errorf("Harness = %q, want 'opencode'", resolved.Harness)
	}
	if resolved.Model != "zai-coding-plan/glm-5.1" {
		t.Errorf("Model = %q, want 'zai-coding-plan/glm-5.1'", resolved.Model)
	}
}

func TestResolveAgent_AntigravityAliases(t *testing.T) {
	cfg := V2Config{AntigravityModel: "Gemini 3.5 Flash (High)", Harnesses: map[string]*HarnessConfig{}}
	for _, alias := range []string{"ag", "agy", "antigravity"} {
		t.Run(alias, func(t *testing.T) {
			resolved, err := cfg.ResolveAgent(alias)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resolved.Harness != "antigravity" {
				t.Errorf("Harness = %q, want 'antigravity'", resolved.Harness)
			}
			if resolved.Model != "Gemini 3.5 Flash (High)" {
				t.Errorf("Model = %q, want configured Antigravity model", resolved.Model)
			}
		})
	}
}

func TestResolveAgent_AntigravityNamedModel(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"ag": {Models: map[string]string{
				"flash": "Gemini 3.5 Flash (High)",
			}},
		},
	}
	resolved, err := cfg.ResolveAgent("ag:flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "antigravity" {
		t.Errorf("Harness = %q, want 'antigravity'", resolved.Harness)
	}
	if resolved.Model != "Gemini 3.5 Flash (High)" {
		t.Errorf("Model = %q, want 'Gemini 3.5 Flash (High)'", resolved.Model)
	}
}

func TestResolveAgent_RawModelString(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{}}
	resolved, err := cfg.ResolveAgent("opencode:zai-coding-plan/glm-5.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "opencode" {
		t.Errorf("Harness = %q, want 'opencode'", resolved.Harness)
	}
	if resolved.Model != "zai-coding-plan/glm-5.1" {
		t.Errorf("Model = %q, want 'zai-coding-plan/glm-5.1'", resolved.Model)
	}
}

func TestResolveRoleReasoning_BareAliasResolvesInSelectedHarness(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"cx": {Models: map[string]string{
				"g55-xh": "gpt-5.5-extra-high",
			}},
		},
	}
	model, effort, err := cfg.ResolveRoleReasoning("verify", "codex", "g55-xh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != "gpt-5.5-extra-high" {
		t.Errorf("model = %q, want resolved harness model alias", model)
	}
	if effort != "" {
		t.Errorf("effort = %q, want empty when alias resolves to a model", effort)
	}
}

func TestResolveRoleReasoning_HarnessScopedAlias(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"op": {Models: map[string]string{
				"g55-xh": "zai/glm-5.1-xh",
			}},
		},
	}

	// Scoped alias targeting the selected harness resolves to the model.
	model, effort, err := cfg.ResolveRoleReasoning("verify", "opencode", "op:g55-xh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != "zai/glm-5.1-xh" {
		t.Errorf("model = %q, want scoped harness model alias", model)
	}
	if effort != "" {
		t.Errorf("effort = %q, want empty", effort)
	}

	// Scoped alias for a different harness than the one selected is a no-op.
	model, effort, err = cfg.ResolveRoleReasoning("verify", "codex", "op:g55-xh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != "" || effort != "" {
		t.Errorf("scoped alias for other harness applied: model=%q effort=%q", model, effort)
	}
}

func TestResolveRoleReasoning_EffortTokenWhenNoAlias(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{}}
	model, effort, err := cfg.ResolveRoleReasoning("verify", "codex", "high")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model != "" {
		t.Errorf("model = %q, want empty for a bare effort token", model)
	}
	if effort != "high" {
		t.Errorf("effort = %q, want 'high'", effort)
	}
}

func TestResolveRoleReasoning_UnknownScopedAliasErrors(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"cx": {Models: map[string]string{"g55-xh": "gpt-5.5-extra-high"}},
		},
	}
	_, _, err := cfg.ResolveRoleReasoning("verify", "codex", "cx:nope")
	if err == nil {
		t.Fatal("expected error for unknown scoped model alias")
	}
}

func TestResolveAgent_UnknownHarness(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{}}
	_, err := cfg.ResolveAgent("unknown")
	if err == nil {
		t.Fatal("expected error for unknown harness")
	}
	if !strings.Contains(err.Error(), "unknown agent alias") {
		t.Errorf("error = %q, want 'unknown agent alias'", err.Error())
	}
}

func TestResolveAgent_UnresolvedModelDidYouMean(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"op": {Models: map[string]string{
				"z":  "zai-coding-plan/glm-5.1",
				"gk": "opencode-go/kimi-k2.6",
			}},
		},
	}
	_, err := cfg.ResolveAgent("op:gp")
	if err == nil {
		t.Fatal("expected error for unresolved model name")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("error = %q, want 'did you mean'", err.Error())
	}
	if !strings.Contains(err.Error(), "gk") {
		t.Errorf("error = %q, should suggest 'gk'", err.Error())
	}
}

func TestResolveAgent_NumericOnlyModelRejected(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{}}
	_, err := cfg.ResolveAgent("cc:2")
	if err != nil {
		t.Fatalf("numeric right side should be treated as weight: %v", err)
	}
}

func TestResolveAgent_ThirdSegmentRejected(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{}}
	_, err := cfg.ResolveAgent("cc:opus:2")
	if err == nil {
		t.Fatal("expected error for third colon segment")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %q, want 'not supported'", err.Error())
	}
}

func TestResolveAgent_UserDefinedHarness(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"droid": {
				Command: []string{"droid", "run"},
				Models:  map[string]string{"v1": "droid-v1"},
			},
		},
	}
	resolved, err := cfg.ResolveAgent("droid:v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "droid" {
		t.Errorf("Harness = %q, want 'droid'", resolved.Harness)
	}
	if resolved.Model != "droid-v1" {
		t.Errorf("Model = %q, want 'droid-v1'", resolved.Model)
	}
}

func TestResolveAgent_BuiltInNamedModelUsesCanonicalKey(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"cc": {Models: map[string]string{
				"opus": "claude-opus-4-7",
			}},
		},
	}
	resolved, err := cfg.ResolveAgent("cc:opus")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "claude" {
		t.Errorf("Harness = %q, want 'claude'", resolved.Harness)
	}
	if resolved.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want 'claude-opus-4-7'", resolved.Model)
	}
}

func TestResolveAgent_DidYouMeanTop3(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"cc": {Models: map[string]string{
				"opus":   "claude-opus-4-7",
				"sonnet": "claude-sonnet-4-6",
				"haiku":  "claude-haiku-3-5",
				"mini":   "claude-mini",
			}},
		},
	}
	_, err := cfg.ResolveAgent("cc:opux")
	if err == nil {
		t.Fatal("expected error for typo")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "opus") {
		t.Errorf("error = %q, should suggest 'opus' as closest", errStr)
	}
}

func TestResolveAgent_NoModelsDefined(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"droid": {Command: []string{"droid"}},
		},
	}
	_, err := cfg.ResolveAgent("droid:fast")
	if err == nil {
		t.Fatal("expected error for unresolved model with no models defined")
	}
	if !strings.Contains(err.Error(), "no models defined") {
		t.Errorf("error = %q, want 'no models defined'", err.Error())
	}
}

func TestResolveAgent_BareAlias_PrefersDefaultsOverRoot(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `claude_model = "root-sonnet"

[defaults]
claude_model = "defaults-opus"
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Model != "defaults-opus" {
		t.Errorf("Model = %q, want 'defaults-opus' (defaults should win)", resolved.Model)
	}
}

func TestResolveAgent_BareAlias_FallsBackToRoot(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `claude_model = "root-sonnet"
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Model != "root-sonnet" {
		t.Errorf("Model = %q, want 'root-sonnet' (root fallback)", resolved.Model)
	}
}

func TestResolveAgent_BareAlias_FallsThroughWhenNothingSet(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Model != "" {
		t.Errorf("Model = %q, want empty when nothing configured", resolved.Model)
	}
}

func TestResolveAgent_UserDefinedHarness_BareAlias_NoModel(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"droid": {Command: []string{"droid"}},
		},
	}
	resolved, err := cfg.ResolveAgent("droid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Model != "" {
		t.Errorf("Model = %q, want empty for user-defined bare alias", resolved.Model)
	}
}
