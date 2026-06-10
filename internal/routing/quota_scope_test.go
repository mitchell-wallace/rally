package routing

import "testing"

func TestQuotaScope_Antigravity_Flash(t *testing.T) {
	got := QuotaScope("antigravity", "Gemini 3.5 Flash (High)")
	want := "antigravity:flash"
	if got != want {
		t.Errorf("QuotaScope(%q, %q) = %q, want %q", "antigravity", "Gemini 3.5 Flash (High)", got, want)
	}
}

func TestQuotaScope_Antigravity_Pro(t *testing.T) {
	got := QuotaScope("antigravity", "Gemini 3.5 Pro")
	want := "antigravity:pro"
	if got != want {
		t.Errorf("QuotaScope(%q, %q) = %q, want %q", "antigravity", "Gemini 3.5 Pro", got, want)
	}
}

func TestQuotaScope_Antigravity_Claude(t *testing.T) {
	got := QuotaScope("antigravity", "Claude 4 Sonnet (High)")
	want := "antigravity:claude"
	if got != want {
		t.Errorf("QuotaScope(%q, %q) = %q, want %q", "antigravity", "Claude 4 Sonnet (High)", got, want)
	}
}

func TestQuotaScope_Antigravity_CaseInsensitive(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"GEMINI 3.5 FLASH (HIGH)", "antigravity:flash"},
		{"gemini 3.5 pro", "antigravity:pro"},
		{"CLAUDE 4 SONNET", "antigravity:claude"},
		{"Flash", "antigravity:flash"},
		{"PRO", "antigravity:pro"},
	}
	for _, tc := range cases {
		got := QuotaScope("antigravity", tc.model)
		if got != tc.want {
			t.Errorf("QuotaScope(%q, %q) = %q, want %q", "antigravity", tc.model, got, tc.want)
		}
	}
}

func TestQuotaScope_Antigravity_DistinctFamilies(t *testing.T) {
	flash := QuotaScope("antigravity", "Gemini 3.5 Flash (High)")
	pro := QuotaScope("antigravity", "Gemini 3.5 Pro")
	claude := QuotaScope("antigravity", "Claude 4 Sonnet (High)")
	if flash == pro || flash == claude || pro == claude {
		t.Errorf("flash=%q pro=%q claude=%q should all be distinct", flash, pro, claude)
	}
}

func TestQuotaScope_Antigravity_UnknownFamily(t *testing.T) {
	got := QuotaScope("antigravity", "Some Unknown Model")
	if got != "antigravity:some unknown model" {
		t.Errorf("QuotaScope for unknown family = %q, want %q", got, "antigravity:some unknown model")
	}
}

func TestQuotaScope_OpenCode_SplitsOnFirstSlash(t *testing.T) {
	got := QuotaScope("opencode", "zai-coding-plan/glm-5.1")
	want := "opencode:zai-coding-plan"
	if got != want {
		t.Errorf("QuotaScope(%q, %q) = %q, want %q", "opencode", "zai-coding-plan/glm-5.1", got, want)
	}
}

func TestQuotaScope_OpenCode_MultipleSlashes(t *testing.T) {
	got := QuotaScope("opencode", "provider-org/model-family/variant")
	want := "opencode:provider-org"
	if got != want {
		t.Errorf("QuotaScope(%q, %q) = %q, want %q", "opencode", "provider-org/model-family/variant", got, want)
	}
}

func TestQuotaScope_OpenCode_NoSlash(t *testing.T) {
	got := QuotaScope("opencode", "plain-model")
	want := "opencode:plain-model"
	if got != want {
		t.Errorf("QuotaScope(%q, %q) = %q, want %q", "opencode", "plain-model", got, want)
	}
}

func TestQuotaScope_DirectHarness_Claude(t *testing.T) {
	got := QuotaScope("claude", "claude-4-sonnet-20250514")
	want := "claude"
	if got != want {
		t.Errorf("QuotaScope(%q, %q) = %q, want %q", "claude", "claude-4-sonnet-20250514", got, want)
	}
}

func TestQuotaScope_DirectHarness_Codex(t *testing.T) {
	got := QuotaScope("codex", "gpt-5.5")
	want := "codex"
	if got != want {
		t.Errorf("QuotaScope(%q, %q) = %q, want %q", "codex", "gpt-5.5", got, want)
	}
}

func TestQuotaScope_DirectHarness_Gemini(t *testing.T) {
	got := QuotaScope("gemini", "gemini-3.5-pro")
	want := "gemini"
	if got != want {
		t.Errorf("QuotaScope(%q, %q) = %q, want %q", "gemini", "gemini-3.5-pro", got, want)
	}
}

func TestQuotaScope_DirectHarness_StraySlashNotMisSplit(t *testing.T) {
	got := QuotaScope("claude", "org/model")
	want := "claude"
	if got != want {
		t.Errorf("QuotaScope(%q, %q) = %q, want %q (stray slash should not affect direct harness)", "claude", "org/model", got, want)
	}
}

func TestQuotaScope_DirectHarness_CaseInsensitiveHarness(t *testing.T) {
	got := QuotaScope("Claude", "some/model")
	want := "Claude"
	if got != want {
		t.Errorf("QuotaScope(%q, %q) = %q, want %q", "Claude", "some/model", got, want)
	}
}

func TestQuotaScope_UnknownHarness_DirectFallback(t *testing.T) {
	got := QuotaScope("custom-harness", "some-model")
	want := "custom-harness"
	if got != want {
		t.Errorf("QuotaScope(%q, %q) = %q, want %q", "custom-harness", "some-model", got, want)
	}
}

func TestQuotaScope_Antigravity_FlashVariants_SameScope(t *testing.T) {
	a := QuotaScope("antigravity", "Gemini 3.5 Flash (High)")
	b := QuotaScope("antigravity", "Gemini 4 Flash")
	c := QuotaScope("antigravity", "flash-2.0-preview")
	if a != b || b != c {
		t.Errorf("all flash variants should share scope: a=%q b=%q c=%q", a, b, c)
	}
}
