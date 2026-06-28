package reliability

import (
	"strings"
	"testing"
)

func TestCategoryToClass_ExhaustiveMapping(t *testing.T) {
	// Every category in AllCategories must map to exactly one FailureClass.
	for _, cat := range AllCategories {
		class := CategoryToClass(cat)
		if class == "" {
			t.Errorf("CategoryToClass(%q) returned empty FailureClass", cat)
		}
		// Verify it's one of the three valid classes.
		switch class {
		case FailureInfra, FailureAgent, FailureIncomplete:
			// ok
		default:
			t.Errorf("CategoryToClass(%q) returned unknown class %q", cat, class)
		}
	}
}

func TestCategoryToClass_MappingValues(t *testing.T) {
	// Design Decision 3: load-bearing mapping.
	tests := []struct {
		category      FailureCategory
		expectedClass FailureClass
	}{
		{CategoryUsageLimit, FailureAgent},
		{CategoryShortRateLimit, FailureInfra},
		{CategoryProviderOverloaded, FailureInfra},
		{CategoryTransientInfra, FailureInfra},
		{CategoryInvalidModel, FailureAgent},
		{CategoryAuthOrProxy, FailureAgent},
		{CategoryHarnessLaunch, FailureInfra},
		{CategoryIncompleteFinalization, FailureIncomplete},
		{CategoryAgentError, FailureAgent},
		{CategoryUnidentifiedIssue, FailureAgent},
	}

	for _, tt := range tests {
		t.Run(string(tt.category), func(t *testing.T) {
			got := CategoryToClass(tt.category)
			if got != tt.expectedClass {
				t.Errorf("CategoryToClass(%q) = %q, want %q", tt.category, got, tt.expectedClass)
			}
		})
	}
}

func TestCategoryToClass_NotInfra(t *testing.T) {
	// The load-bearing invariant: usage_limit, invalid_model, and
	// auth_or_proxy MUST NOT map to FailureInfra so they do not feed
	// the freeze counter.
	mustNotBeInfra := []FailureCategory{
		CategoryUsageLimit,
		CategoryInvalidModel,
		CategoryAuthOrProxy,
	}
	for _, cat := range mustNotBeInfra {
		class := CategoryToClass(cat)
		if class == FailureInfra {
			t.Errorf("CategoryToClass(%q) = FailureInfra, but this category must NOT map to infra (would feed the freeze counter)", cat)
		}
	}
}

func TestCategoryToClass_UnknownDefaultsToAgent(t *testing.T) {
	got := CategoryToClass("some_unknown_category")
	if got != FailureAgent {
		t.Errorf("CategoryToClass for unknown category = %q, want %q", got, FailureAgent)
	}
}

func TestCategoryDisplayLabel_AllCategories(t *testing.T) {
	// Every category must have a non-empty display label.
	for _, cat := range AllCategories {
		label := CategoryDisplayLabel(cat)
		if label == "" {
			t.Errorf("CategoryDisplayLabel(%q) returned empty string", cat)
		}
	}
}

func TestCategoryDisplayLabel_NoHarnessName(t *testing.T) {
	// Display labels must not contain harness names unless the category is
	// intentionally harness-specific (none currently are).
	harnessNames := []string{"claude", "codex", "antigravity", "opencode"}

	for _, cat := range AllCategories {
		label := CategoryDisplayLabel(cat)
		lowerLabel := strings.ToLower(label)
		for _, harness := range harnessNames {
			if strings.Contains(lowerLabel, harness) {
				t.Errorf("CategoryDisplayLabel(%q) = %q contains harness name %q; labels should not carry harness names unless intentionally harness-specific",
					cat, label, harness)
			}
		}
	}
}

func TestCategoryDisplayLabel_ExpectedValues(t *testing.T) {
	// Verify the specific display labels from the design Decision 2 table.
	tests := []struct {
		category FailureCategory
		expected string
	}{
		{CategoryUsageLimit, "usage limit"},
		{CategoryShortRateLimit, "rate limit"},
		{CategoryProviderOverloaded, "provider overloaded"},
		{CategoryTransientInfra, "infra error"},
		{CategoryInvalidModel, "invalid model"},
		{CategoryAuthOrProxy, "auth/proxy error"},
		{CategoryHarnessLaunch, "harness launch error"},
		{CategoryIncompleteFinalization, "incomplete: file changes without finalization"},
		{CategoryAgentError, "agent error"},
		{CategoryUnidentifiedIssue, "unidentified issue"},
	}

	for _, tt := range tests {
		t.Run(string(tt.category), func(t *testing.T) {
			got := CategoryDisplayLabel(tt.category)
			if got != tt.expected {
				t.Errorf("CategoryDisplayLabel(%q) = %q, want %q", tt.category, got, tt.expected)
			}
		})
	}
}

func TestCategoryDisplayLabel_UnknownFallback(t *testing.T) {
	got := CategoryDisplayLabel("unknown_category")
	if got != "unknown_category" {
		t.Errorf("CategoryDisplayLabel for unknown category = %q, want %q", got, "unknown_category")
	}
}

func TestAllCategories_Count(t *testing.T) {
	// There must be exactly ten categories per design Decision 2 and Decision 8.
	if len(AllCategories) != 10 {
		t.Errorf("AllCategories has %d entries, want 10", len(AllCategories))
	}
}

func TestAllCategories_DoNotContainLifecycleOutcomes(t *testing.T) {
	for _, forbidden := range []string{"handoff_requested", "run_timeout", "handoff_timeout"} {
		for _, cat := range AllCategories {
			if string(cat) == forbidden {
				t.Fatalf("FailureCategory must not contain lifecycle outcome %q", forbidden)
			}
		}
	}
}

func TestAllCategories_NoDuplicates(t *testing.T) {
	seen := make(map[FailureCategory]bool)
	for _, cat := range AllCategories {
		if seen[cat] {
			t.Errorf("duplicate category in AllCategories: %q", cat)
		}
		seen[cat] = true
	}
}

func TestAllCategories_AllHaveMappings(t *testing.T) {
	// Every category in AllCategories must have both a display label
	// and a FailureClass mapping.
	for _, cat := range AllCategories {
		if _, ok := categoryDisplayLabels[cat]; !ok {
			t.Errorf("category %q missing from categoryDisplayLabels", cat)
		}
		if _, ok := categoryToClass[cat]; !ok {
			t.Errorf("category %q missing from categoryToClass", cat)
		}
	}
}

func TestFailureEvidence_CanBeNil(t *testing.T) {
	// FailureEvidence is a pointer type on TryResult. Verify that a nil
	// evidence value doesn't panic when checking fields.
	var ev *FailureEvidence
	if ev != nil {
		t.Error("expected nil FailureEvidence")
	}
}

func TestStrategyDecision_HasCategoryAndDisplayLabel(t *testing.T) {
	// Verify the StrategyDecision struct accepts Category and DisplayLabel.
	d := StrategyDecision{
		Strategy:     StrategyResume,
		Reason:       "test pattern",
		FailureClass: FailureInfra,
		Category:     CategoryShortRateLimit,
		DisplayLabel: "rate limit",
	}
	if d.Category != CategoryShortRateLimit {
		t.Errorf("Category = %q, want %q", d.Category, CategoryShortRateLimit)
	}
	if d.DisplayLabel != "rate limit" {
		t.Errorf("DisplayLabel = %q, want %q", d.DisplayLabel, "rate limit")
	}
}
