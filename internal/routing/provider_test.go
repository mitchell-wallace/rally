package routing

import "testing"

func TestProviderIndex_QuotaScope_MemberOverridesHarnessDefault(t *testing.T) {
	idx := NewProviderIndex()
	idx.Add("codex", "codex", "gpt-5.5", false)
	idx.Add("codex", "opencode", "openai/gpt-5.5", false)

	// Both members, despite different harnesses and harness-default scopes, share
	// the provider bucket so a usage limit benches them together.
	if got, want := idx.QuotaScope("codex", "gpt-5.5"), "provider:codex"; got != want {
		t.Errorf("QuotaScope(codex member) = %q, want %q", got, want)
	}
	if got, want := idx.QuotaScope("opencode", "openai/gpt-5.5"), "provider:codex"; got != want {
		t.Errorf("QuotaScope(opencode member) = %q, want %q", got, want)
	}
}

func TestProviderIndex_QuotaScope_NonMemberFallsBack(t *testing.T) {
	idx := NewProviderIndex()
	idx.Add("codex", "codex", "gpt-5.5", false)

	// A runner with no provider keeps the harness-default scope.
	if got, want := idx.QuotaScope("opencode", "zai-coding-plan/glm-5.2"), "opencode:zai-coding-plan"; got != want {
		t.Errorf("QuotaScope(non-member) = %q, want %q", got, want)
	}
}

func TestProviderIndex_Disabled(t *testing.T) {
	idx := NewProviderIndex()
	idx.Add("claude", "claude", "claude-opus-4-8", true)
	idx.Add("codex", "codex", "gpt-5.5", false)

	if !idx.Disabled("claude", "claude-opus-4-8") {
		t.Errorf("claude member should be disabled")
	}
	if idx.Disabled("codex", "gpt-5.5") {
		t.Errorf("codex member should not be disabled")
	}
	if idx.Disabled("opencode", "zai/glm") {
		t.Errorf("non-member should never be disabled")
	}
}

func TestProviderIndex_DisabledStickyAcrossAddOrder(t *testing.T) {
	idx := NewProviderIndex()
	// First member registers enabled, a later member marks the provider disabled;
	// the provider must end up disabled regardless of Add order.
	idx.Add("op", "opencode", "opencode-go/glm-5.1", false)
	idx.Add("op", "opencode", "opencode-go/kimi", true)

	if !idx.Disabled("opencode", "opencode-go/glm-5.1") {
		t.Errorf("provider should be disabled once any member marks it disabled")
	}
}

func TestProviderIndex_ProviderFor(t *testing.T) {
	idx := NewProviderIndex()
	idx.Add("codex", "codex", "gpt-5.5", false)

	if name, ok := idx.ProviderFor("codex", "gpt-5.5"); !ok || name != "codex" {
		t.Errorf("ProviderFor(member) = %q,%v, want codex,true", name, ok)
	}
	if _, ok := idx.ProviderFor("antigravity", "gemini-3-pro"); ok {
		t.Errorf("ProviderFor(non-member) should be false")
	}
}

func TestProviderIndex_NilSafe(t *testing.T) {
	var idx *ProviderIndex
	if got, want := idx.QuotaScope("opencode", "zai-coding-plan/glm-5.2"), "opencode:zai-coding-plan"; got != want {
		t.Errorf("nil QuotaScope = %q, want harness-default %q", got, want)
	}
	if idx.Disabled("codex", "gpt-5.5") {
		t.Errorf("nil Disabled should be false")
	}
	if _, ok := idx.ProviderFor("codex", "gpt-5.5"); ok {
		t.Errorf("nil ProviderFor should be false")
	}
	idx.Add("x", "codex", "gpt-5.5", true) // no panic on nil
}
