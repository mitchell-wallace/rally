package config

import (
	"path/filepath"
	"strings"
	"testing"
)

const providerHarnessTables = `schema_version = 2

[harness.cx.models]
g55 = 'gpt-5.5'
g54 = 'gpt-5.4'

[harness.op.models]
ds = 'opencode-go/deepseek-v4'
zai = 'zai-coding-plan/glm-5.2'
`

func TestLoadV2_Providers_ArrayForm(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, providerHarnessTables+`
[providers]
codex = ['g55', 'g54', 'opencode:openai/gpt-5.5']
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	pc, ok := cfg.Providers["codex"]
	if !ok {
		t.Fatalf("expected provider 'codex'")
	}
	if pc.Disabled {
		t.Errorf("array-form provider should not be disabled")
	}
	if len(pc.Models) != 3 {
		t.Errorf("Models = %v, want 3 entries", pc.Models)
	}

	idx, err := cfg.BuildProviderIndex()
	if err != nil {
		t.Fatalf("BuildProviderIndex: %v", err)
	}
	// Bare alias, harness:model, and a cross-harness member all share the bucket.
	for _, tc := range []struct{ harness, model string }{
		{"codex", "gpt-5.5"},
		{"codex", "gpt-5.4"},
		{"opencode", "openai/gpt-5.5"},
	} {
		if got, want := idx.QuotaScope(tc.harness, tc.model), "provider:codex"; got != want {
			t.Errorf("QuotaScope(%s,%s) = %q, want %q", tc.harness, tc.model, got, want)
		}
	}
}

func TestLoadV2_Providers_TableFormDisabled(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, providerHarnessTables+`
[providers.opencode-go]
models = ['op:ds', 'opencode:opencode-go/glm-5.1']
disabled = true
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	pc, ok := cfg.Providers["opencode-go"]
	if !ok {
		t.Fatalf("expected provider 'opencode-go'")
	}
	if !pc.Disabled {
		t.Errorf("table-form provider with disabled=true should be disabled")
	}

	idx, err := cfg.BuildProviderIndex()
	if err != nil {
		t.Fatalf("BuildProviderIndex: %v", err)
	}
	if !idx.Disabled("opencode", "opencode-go/deepseek-v4") {
		t.Errorf("op:ds member should be disabled")
	}
	if !idx.Disabled("opencode", "opencode-go/glm-5.1") {
		t.Errorf("glm member should be disabled")
	}
}

func TestLoadV2_Providers_BareAliasAmbiguous(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[harness.cx.models]
foo = 'gpt-foo'

[harness.ge.models]
foo = 'gemini-foo'

[providers]
mix = ['foo']
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "ambiguous model alias") {
		t.Fatalf("expected ambiguous alias error, got %v", err)
	}
}

func TestLoadV2_Providers_BareAliasUnknown(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, providerHarnessTables+`
[providers]
codex = ['nope']
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "unknown model alias") {
		t.Fatalf("expected unknown alias error, got %v", err)
	}
}

func TestLoadV2_Providers_ConflictAcrossProviders(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, providerHarnessTables+`
[providers]
a = ['g55']
b = ['cx:g55']
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "only one provider") {
		t.Fatalf("expected cross-provider conflict error, got %v", err)
	}
}

func TestLoadV2_Providers_EmptyModels(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, providerHarnessTables+`
[providers.empty]
models = []
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "has no models") {
		t.Fatalf("expected empty-models error, got %v", err)
	}
}

func TestLoadV2_Providers_DuplicateKeyCase(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, providerHarnessTables+`
[providers]
Codex = ['g55']
codex = ['g54']
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "differ only by case") {
		t.Fatalf("expected duplicate-case error, got %v", err)
	}
}

func TestLoadV2_Providers_UnknownTableKey(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, providerHarnessTables+`
[providers.codex]
models = ['g55']
enbaled = true
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("expected unknown-key error, got %v", err)
	}
}

func TestLoadV2_Providers_DisabledNotBool(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, providerHarnessTables+`
[providers.codex]
models = ['g55']
disabled = "yes"
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "disabled must be a boolean") {
		t.Fatalf("expected disabled-bool error, got %v", err)
	}
}

func TestLoadV2_Providers_NoProvidersNilIndex(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, providerHarnessTables)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	idx, err := cfg.BuildProviderIndex()
	if err != nil {
		t.Fatalf("BuildProviderIndex: %v", err)
	}
	if idx != nil {
		t.Errorf("expected nil index when no providers configured")
	}
	// nil index falls back to harness-default scope.
	if got, want := idx.QuotaScope("opencode", "zai-coding-plan/glm-5.2"), "opencode:zai-coding-plan"; got != want {
		t.Errorf("nil QuotaScope = %q, want %q", got, want)
	}
}

func TestProviders_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, providerHarnessTables+`
[providers]
codex = ['g55', 'g54']

[providers.claude]
models = ['op:zai']
disabled = true
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	out := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveV2File(out, cfg); err != nil {
		t.Fatalf("SaveV2File: %v", err)
	}

	reloaded, err := LoadV2File(out)
	if err != nil {
		t.Fatalf("LoadV2File: %v", err)
	}
	if got, ok := reloaded.Providers["codex"]; !ok || got.Disabled || len(got.Models) != 2 {
		t.Errorf("codex round-trip = %+v", got)
	}
	if got, ok := reloaded.Providers["claude"]; !ok || !got.Disabled || len(got.Models) != 1 {
		t.Errorf("claude round-trip = %+v", got)
	}
}
