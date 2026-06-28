package routing

import (
	"testing"
)

func TestParseEntry_OneSegment_NoQuota(t *testing.T) {
	p, err := ParseEntry("cc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "cc" {
		t.Errorf("Spec = %q, want %q", p.Spec, "cc")
	}
	if p.HasQuota {
		t.Error("HasQuota should be false")
	}
	if p.QuotaMin != 0 || p.QuotaMax != 0 {
		t.Errorf("QuotaMin=%d, QuotaMax=%d; want 0,0", p.QuotaMin, p.QuotaMax)
	}
}

func TestParseEntry_OneSegment_WithSingleQuota(t *testing.T) {
	p, err := ParseEntry("cc:3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "cc" {
		t.Errorf("Spec = %q, want %q", p.Spec, "cc")
	}
	if !p.HasQuota {
		t.Fatal("HasQuota should be true")
	}
	if p.QuotaMin != 3 || p.QuotaMax != 3 {
		t.Errorf("QuotaMin=%d, QuotaMax=%d; want 3,3", p.QuotaMin, p.QuotaMax)
	}
	if !p.QuotaSingle() {
		t.Error("QuotaSingle should be true")
	}
}

func TestParseEntry_OneSegment_WithRangeQuota(t *testing.T) {
	p, err := ParseEntry("model-pro:2-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "model-pro" {
		t.Errorf("Spec = %q, want %q", p.Spec, "model-pro")
	}
	if !p.HasQuota {
		t.Fatal("HasQuota should be true")
	}
	if p.QuotaMin != 2 || p.QuotaMax != 5 {
		t.Errorf("QuotaMin=%d, QuotaMax=%d; want 2,5", p.QuotaMin, p.QuotaMax)
	}
	if !p.QuotaRange() {
		t.Error("QuotaRange should be true")
	}
}

func TestParseEntry_TwoSegments_NoQuota(t *testing.T) {
	p, err := ParseEntry("claude:opus-4.7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "claude:opus-4.7" {
		t.Errorf("Spec = %q, want %q", p.Spec, "claude:opus-4.7")
	}
	if p.HasQuota {
		t.Error("HasQuota should be false")
	}
}

func TestParseEntry_TwoSegments_WithSingleQuota(t *testing.T) {
	p, err := ParseEntry("claude:opus-4.7:1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "claude:opus-4.7" {
		t.Errorf("Spec = %q, want %q", p.Spec, "claude:opus-4.7")
	}
	if !p.HasQuota {
		t.Fatal("HasQuota should be true")
	}
	if p.QuotaMin != 1 || p.QuotaMax != 1 {
		t.Errorf("QuotaMin=%d, QuotaMax=%d; want 1,1", p.QuotaMin, p.QuotaMax)
	}
}

func TestParseEntry_TwoSegments_WithRangeQuota(t *testing.T) {
	p, err := ParseEntry("opencode:opencode-go/kimi-k2.6:1-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "opencode:opencode-go/kimi-k2.6" {
		t.Errorf("Spec = %q, want %q", p.Spec, "opencode:opencode-go/kimi-k2.6")
	}
	if !p.HasQuota {
		t.Fatal("HasQuota should be true")
	}
	if p.QuotaMin != 1 || p.QuotaMax != 5 {
		t.Errorf("QuotaMin=%d, QuotaMax=%d; want 1,5", p.QuotaMin, p.QuotaMax)
	}
}

func TestParseEntry_ShortcutWithQuota(t *testing.T) {
	p, err := ParseEntry("op:z:4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "op:z" {
		t.Errorf("Spec = %q, want %q", p.Spec, "op:z")
	}
	if !p.HasQuota {
		t.Fatal("HasQuota should be true")
	}
	if p.QuotaMin != 4 || p.QuotaMax != 4 {
		t.Errorf("QuotaMin=%d, QuotaMax=%d; want 4,4", p.QuotaMin, p.QuotaMax)
	}
}

func TestParseEntry_ShortcutWithRangeQuota(t *testing.T) {
	p, err := ParseEntry("op:gk:2-6")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "op:gk" {
		t.Errorf("Spec = %q, want %q", p.Spec, "op:gk")
	}
	if !p.HasQuota {
		t.Fatal("HasQuota should be true")
	}
	if p.QuotaMin != 2 || p.QuotaMax != 6 {
		t.Errorf("QuotaMin=%d, QuotaMax=%d; want 2,6", p.QuotaMin, p.QuotaMax)
	}
}

func TestParseEntry_ThreeSegments_NoQuota_Error(t *testing.T) {
	_, err := ParseEntry("claude:opus:4.7")
	if err == nil {
		t.Fatal("expected error for 3-segment entry without quota")
	}
	if err.Error() != "routing: entry \"claude:opus:4.7\" has 3 identifier segments (expected 1 for shortcut or 2 for harness:model)" {
		t.Errorf("error = %q", err.Error())
	}
}

func TestParseEntry_ModelWithEmbeddedDigits_NoQuota(t *testing.T) {
	p, err := ParseEntry("codex:gpt-4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "codex:gpt-4" {
		t.Errorf("Spec = %q, want %q", p.Spec, "codex:gpt-4")
	}
	if p.HasQuota {
		t.Error("gpt-4 should not be treated as quota")
	}
}

func TestParseEntry_ModelWithEmbeddedDigitsAndDot_NoQuota(t *testing.T) {
	p, err := ParseEntry("claude:claude-4.5-sonnet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "claude:claude-4.5-sonnet" {
		t.Errorf("Spec = %q, want %q", p.Spec, "claude:claude-4.5-sonnet")
	}
	if p.HasQuota {
		t.Error("claude-4.5-sonnet should not be treated as quota")
	}
}

func TestParseEntry_QuotaZero_Error(t *testing.T) {
	_, err := ParseEntry("cc:0")
	if err == nil {
		t.Fatal("expected error for zero quota")
	}
}

func TestParseEntry_QuotaRangeMinZero_Error(t *testing.T) {
	_, err := ParseEntry("cc:0-5")
	if err == nil {
		t.Fatal("expected error for quota range with min=0")
	}
}

func TestParseEntry_QuotaRangeMinExceedsMax_Error(t *testing.T) {
	_, err := ParseEntry("cc:5-3")
	if err == nil {
		t.Fatal("expected error for quota range where min > max")
	}
}

func TestParseEntry_EmptyString_Error(t *testing.T) {
	_, err := ParseEntry("")
	if err == nil {
		t.Fatal("expected error for empty entry")
	}
}

func TestParseEntry_SingleNumericSegment_TreatedAsShortcut(t *testing.T) {
	p, err := ParseEntry("4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "4" {
		t.Errorf("Spec = %q, want %q", p.Spec, "4")
	}
	if p.HasQuota {
		t.Error("single numeric segment should be treated as identifier, not quota")
	}
}

func TestParseEntry_RawPreserved(t *testing.T) {
	raw := "claude:opus-4.7:1"
	p, err := ParseEntry(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Raw != raw {
		t.Errorf("Raw = %q, want %q", p.Raw, raw)
	}
}

func TestParseEntries_MultipleValid(t *testing.T) {
	entries := []string{"claude:opus-4.7:1", "codex:gpt-5.5:3", "opencode:opencode-go/kimi-k2.6"}
	result, err := ParseEntries(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("got %d entries, want 3", len(result))
	}
	if result[0].Spec != "claude:opus-4.7" || !result[0].HasQuota || result[0].QuotaMin != 1 {
		t.Errorf("entry[0] = %+v", result[0])
	}
	if result[1].Spec != "codex:gpt-5.5" || !result[1].HasQuota || result[1].QuotaMin != 3 {
		t.Errorf("entry[1] = %+v", result[1])
	}
	if result[2].Spec != "opencode:opencode-go/kimi-k2.6" || result[2].HasQuota {
		t.Errorf("entry[2] = %+v", result[2])
	}
}

func TestParseEntries_InvalidEntry_ReturnsError(t *testing.T) {
	entries := []string{"cc:1", "bad:too:many:segments"}
	_, err := ParseEntries(entries)
	if err == nil {
		t.Fatal("expected error for invalid entry in list")
	}
}

func TestParseEntries_EmptyList(t *testing.T) {
	result, err := ParseEntries([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("got %d entries, want 0", len(result))
	}
}

func TestParseEntry_Scenario2_DefaultRoute(t *testing.T) {
	entries := []string{"claude:opus-4.7:1", "codex:gpt-5.5:3", "opencode:opencode-go/kimi-k2.6"}
	result, err := ParseEntries(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result[0].QuotaMin != 1 || result[0].QuotaMax != 1 {
		t.Errorf("opus entry: QuotaMin=%d QuotaMax=%d, want 1,1", result[0].QuotaMin, result[0].QuotaMax)
	}
	if result[1].QuotaMin != 3 || result[1].QuotaMax != 3 {
		t.Errorf("gpt entry: QuotaMin=%d QuotaMax=%d, want 3,3", result[1].QuotaMin, result[1].QuotaMax)
	}
	if result[2].HasQuota {
		t.Error("kimi entry should have no quota")
	}
}

func TestParseEntry_Scenario7_RangeQuota(t *testing.T) {
	p, err := ParseEntry("opencode:opencode-go/kimi-k2.6:1-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.QuotaMin != 1 || p.QuotaMax != 5 {
		t.Errorf("QuotaMin=%d QuotaMax=%d, want 1,5", p.QuotaMin, p.QuotaMax)
	}
	if !p.QuotaRange() {
		t.Error("expected QuotaRange")
	}
	if p.QuotaSingle() {
		t.Error("should not be QuotaSingle when range")
	}
}

func TestParseEntry_QuotaSingle_WhenMinEqualsMax(t *testing.T) {
	p := ParsedEntry{HasQuota: true, QuotaMin: 3, QuotaMax: 3}
	if !p.QuotaSingle() {
		t.Error("QuotaSingle should be true when min == max")
	}
	if p.QuotaRange() {
		t.Error("QuotaRange should be false when min == max")
	}
}

func TestParseEntry_SlashInModel_NoQuota(t *testing.T) {
	p, err := ParseEntry("opencode:opencode-go/kimi-k2.6")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "opencode:opencode-go/kimi-k2.6" {
		t.Errorf("Spec = %q, want %q", p.Spec, "opencode:opencode-go/kimi-k2.6")
	}
	if p.HasQuota {
		t.Error("should not have quota")
	}
}

func TestParseEntry_SlashInModel_WithQuota(t *testing.T) {
	p, err := ParseEntry("opencode:opencode-go/kimi-k2.6:3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "opencode:opencode-go/kimi-k2.6" {
		t.Errorf("Spec = %q, want %q", p.Spec, "opencode:opencode-go/kimi-k2.6")
	}
	if !p.HasQuota || p.QuotaMin != 3 || p.QuotaMax != 3 {
		t.Errorf("expected quota 3,3; got HasQuota=%v Min=%d Max=%d", p.HasQuota, p.QuotaMin, p.QuotaMax)
	}
}

func TestParseEntry_BareNumeric_NotQuotaWhenAlone(t *testing.T) {
	p, err := ParseEntry("42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Spec != "42" {
		t.Errorf("Spec = %q, want %q", p.Spec, "42")
	}
	if p.HasQuota {
		t.Error("single segment '42' should be treated as identifier, not quota")
	}
}

func TestParseEntry_FourSegments_WithQuotaOnEnd(t *testing.T) {
	_, err := ParseEntry("a:b:c:3")
	if err == nil {
		t.Fatal("expected error for 4-segment entry")
	}
}
