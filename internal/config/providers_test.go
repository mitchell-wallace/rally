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

func TestLoadV2_Providers_HarnessWildcard(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[defaults]
codex_model = 'gpt-5.3'

[harness.cx.models]
g55 = 'gpt-5.5'
g54 = 'gpt-5.4'

[providers]
codex = ['codex:*']
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	counts, err := cfg.ProviderMemberCounts()
	if err != nil {
		t.Fatalf("ProviderMemberCounts: %v", err)
	}
	if got, want := counts["codex"], 3; got != want {
		t.Fatalf("codex member count = %d, want %d", got, want)
	}

	idx, err := cfg.BuildProviderIndex()
	if err != nil {
		t.Fatalf("BuildProviderIndex: %v", err)
	}
	for _, model := range []string{"gpt-5.3", "gpt-5.4", "gpt-5.5"} {
		if got, want := idx.QuotaScope("codex", model), "provider:codex"; got != want {
			t.Errorf("QuotaScope(codex,%s) = %q, want %q", model, got, want)
		}
	}
}

func TestLoadV2_Providers_ModelPrefixWildcard(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[defaults]
opencode_model = 'opencode-go/default'

[harness.op.models]
ds = 'opencode-go/deepseek-v4'
glm = 'opencode-go/glm-5.1'
zai = 'zai-coding-plan/glm-5.2'

[providers]
opencode_go = ['opencode-go/*']
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	counts, err := cfg.ProviderMemberCounts()
	if err != nil {
		t.Fatalf("ProviderMemberCounts: %v", err)
	}
	if got, want := counts["opencode_go"], 3; got != want {
		t.Fatalf("opencode_go member count = %d, want %d", got, want)
	}

	idx, err := cfg.BuildProviderIndex()
	if err != nil {
		t.Fatalf("BuildProviderIndex: %v", err)
	}
	for _, model := range []string{"opencode-go/default", "opencode-go/deepseek-v4", "opencode-go/glm-5.1"} {
		if got, want := idx.QuotaScope("opencode", model), "provider:opencode_go"; got != want {
			t.Errorf("QuotaScope(opencode,%s) = %q, want %q", model, got, want)
		}
	}
	if got, want := idx.QuotaScope("opencode", "zai-coding-plan/glm-5.2"), "opencode:zai-coding-plan"; got != want {
		t.Errorf("QuotaScope(non-matching prefix) = %q, want %q", got, want)
	}
}

func TestLoadV2_Providers_ScopedModelPrefixWildcard(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, providerHarnessTables+`
[providers]
opencode_go = ['op:opencode-go/*']
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	idx, err := cfg.BuildProviderIndex()
	if err != nil {
		t.Fatalf("BuildProviderIndex: %v", err)
	}
	if got, want := idx.QuotaScope("opencode", "opencode-go/deepseek-v4"), "provider:opencode_go"; got != want {
		t.Errorf("QuotaScope(scoped wildcard match) = %q, want %q", got, want)
	}
	if got, want := idx.QuotaScope("opencode", "zai-coding-plan/glm-5.2"), "opencode:zai-coding-plan"; got != want {
		t.Errorf("QuotaScope(scoped wildcard non-match) = %q, want %q", got, want)
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

func TestLoadV2_Providers_BareHarnessAliasNoModelRejected(t *testing.T) {
	dir := t.TempDir()
	// 'cc' resolves to claude with no default model -> empty model, which can
	// never match a model-specific route runner. Must be rejected.
	writeConfig(t, dir, providerHarnessTables+`
[providers]
anthropic = ['cc']
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "no concrete model") {
		t.Fatalf("expected empty-model rejection, got %v", err)
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

func TestLoadV2_Providers_WildcardConflictAcrossProviders(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, providerHarnessTables+`
[providers]
a = ['opencode-go/*']
b = ['op:ds']
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "only one provider") {
		t.Fatalf("expected wildcard conflict error, got %v", err)
	}
}

func TestLoadV2_Providers_WildcardNoMatches(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[providers]
opencode_go = ['opencode-go/*']
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "matched no configured models") {
		t.Fatalf("expected no-match wildcard error, got %v", err)
	}
}

func TestLoadV2_Providers_UnsupportedWildcard(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, providerHarnessTables+`
[providers]
bad = ['opencode-go*']
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "unsupported wildcard") {
		t.Fatalf("expected unsupported wildcard error, got %v", err)
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

// sparkHarnessTables is the codex-spark fixture: a normal codex lineup plus a
// separately metered codex model whose slug ends in "-spark".
const sparkHarnessTables = `schema_version = 2

[defaults]
codex_model = 'gpt-5.3'

[harness.cx.models]
g55   = 'gpt-5.5'
g54   = 'gpt-5.4'
spark = 'gpt-5.3-codex-spark'
`

func TestLoadV2_Providers_ScopedSuffixWildcard(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, sparkHarnessTables+`
[providers.codex-spark]
models = ['codex:*spark']
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	counts, err := cfg.ProviderMemberCounts()
	if err != nil {
		t.Fatalf("ProviderMemberCounts: %v", err)
	}
	if got, want := counts["codex-spark"], 1; got != want {
		t.Fatalf("codex-spark member count = %d, want %d", got, want)
	}
	idx, err := cfg.BuildProviderIndex()
	if err != nil {
		t.Fatalf("BuildProviderIndex: %v", err)
	}
	if got, want := idx.QuotaScope("codex", "gpt-5.3-codex-spark"), "provider:codex-spark"; got != want {
		t.Errorf("QuotaScope(codex spark) = %q, want %q", got, want)
	}
	// Suffix wildcard must NOT sweep up the unrelated codex models.
	if got, want := idx.QuotaScope("codex", "gpt-5.5"), "codex"; got != want {
		t.Errorf("QuotaScope(codex gpt-5.5) = %q, want %q (must stay out of codex-spark)", got, want)
	}
}

func TestLoadV2_Providers_BareSuffixWildcard(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, sparkHarnessTables+`
[providers.spark]
models = ['*spark']
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	counts, err := cfg.ProviderMemberCounts()
	if err != nil {
		t.Fatalf("ProviderMemberCounts: %v", err)
	}
	if got, want := counts["spark"], 1; got != want {
		t.Fatalf("spark member count = %d, want %d (only the codex spark model ends in 'spark')", got, want)
	}
}

// TestLoadV2_Providers_ExcludeCarvesOutMember mirrors the codex-spark use case:
// a `codex:*` group pulls in every codex model, then `exclude = ['codex:*spark']`
// peels the separately metered spark model out so it can live in its own
// provider without tripping the one-runner-one-provider rule.
func TestLoadV2_Providers_ExcludeCarvesOutMember(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, sparkHarnessTables+`
[providers.codex]
models  = ['codex:*']
exclude = ['codex:*spark']

[providers.codex-spark]
models = ['codex:*spark']
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	counts, err := cfg.ProviderMemberCounts()
	if err != nil {
		t.Fatalf("ProviderMemberCounts: %v", err)
	}
	if got, want := counts["codex"], 3; got != want {
		t.Errorf("codex member count = %d, want %d (spark excluded, default+g55+g54 remain)", got, want)
	}
	if got, want := counts["codex-spark"], 1; got != want {
		t.Errorf("codex-spark member count = %d, want %d", got, want)
	}

	idx, err := cfg.BuildProviderIndex()
	if err != nil {
		t.Fatalf("BuildProviderIndex: %v", err)
	}
	for _, model := range []string{"gpt-5.3", "gpt-5.4", "gpt-5.5"} {
		if got, want := idx.QuotaScope("codex", model), "provider:codex"; got != want {
			t.Errorf("QuotaScope(codex,%s) = %q, want %q", model, got, want)
		}
	}
	if got, want := idx.QuotaScope("codex", "gpt-5.3-codex-spark"), "provider:codex-spark"; got != want {
		t.Errorf("QuotaScope(codex spark) = %q, want %q (must be carved out)", got, want)
	}
}

func TestLoadV2_Providers_ExcludeConcreteSpec(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, sparkHarnessTables+`
[providers.codex]
models  = ['codex:*']
exclude = ['cx:spark']
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	counts, err := cfg.ProviderMemberCounts()
	if err != nil {
		t.Fatalf("ProviderMemberCounts: %v", err)
	}
	if got, want := counts["codex"], 3; got != want {
		t.Errorf("codex member count = %d, want %d", got, want)
	}
}

// TestLoadV2_Providers_ExcludePreventsConflict verifies an exclude is applied
// before the ownership check, so a wildcard that would otherwise overlap
// another provider's claim does not error.
func TestLoadV2_Providers_ExcludePreventsConflict(t *testing.T) {
	dir := t.TempDir()
	// codex-spark sorts after codex, so it claims spark second; codex must have
	// already removed spark via exclude or this would be a conflict.
	writeConfig(t, dir, sparkHarnessTables+`
[providers.codex]
models  = ['codex:*']
exclude = ['codex:*spark']

[providers.codex-spark]
models = ['codex:*spark']
`)
	if _, err := LoadV2(dir); err != nil {
		t.Fatalf("exclude should prevent cross-provider conflict, got: %v", err)
	}
}

func TestLoadV2_Providers_ExcludeWildcardNoMatchIsLenient(t *testing.T) {
	dir := t.TempDir()
	// No codex model ends in "-missing", but that is a no-op exclude, not an
	// error: excludes are filters, robust to models being added later.
	writeConfig(t, dir, sparkHarnessTables+`
[providers.codex]
models  = ['codex:*']
exclude = ['codex:*missing']
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	counts, err := cfg.ProviderMemberCounts()
	if err != nil {
		t.Fatalf("ProviderMemberCounts: %v", err)
	}
	if got, want := counts["codex"], 4; got != want {
		t.Errorf("codex member count = %d, want %d (no-op exclude, all 4 codex models)", got, want)
	}
}

func TestLoadV2_Providers_ExcludeEverythingRejected(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, sparkHarnessTables+`
[providers.codex]
models  = ['cx:g55', 'cx:g54']
exclude = ['codex:*']
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "no models remaining after exclusions") {
		t.Fatalf("expected empty-after-exclude error, got %v", err)
	}
}

func TestLoadV2_Providers_ExcludeNotArray(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, sparkHarnessTables+`
[providers.codex]
models  = ['codex:*']
exclude = "cx:spark"
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "exclude must be an array of strings") {
		t.Fatalf("expected exclude-array error, got %v", err)
	}
}

func TestLoadV2_Providers_SuffixWildcardNoMatch(t *testing.T) {
	dir := t.TempDir()
	// A models wildcard (unlike an exclude) must hard-fail when it matches
	// nothing so a typo never silently empties a quota group.
	writeConfig(t, dir, sparkHarnessTables+`
[providers.missing]
models = ['codex:*nope']
`)
	_, err := LoadV2(dir)
	if err == nil || !strings.Contains(err.Error(), "matched no configured models") {
		t.Fatalf("expected no-match wildcard error, got %v", err)
	}
}

func TestProviders_ExcludeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, sparkHarnessTables+`
[providers.codex]
models  = ['codex:*']
exclude = ['codex:*spark']
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
	got, ok := reloaded.Providers["codex"]
	if !ok {
		t.Fatalf("codex provider lost on round-trip")
	}
	if len(got.Exclude) != 1 || got.Exclude[0] != "codex:*spark" {
		t.Errorf("codex exclude round-trip = %+v", got.Exclude)
	}
}
