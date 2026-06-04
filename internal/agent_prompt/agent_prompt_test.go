package agent_prompt

import (
	"strings"
	"testing"
)

func TestRoleEmbeddedDefaults(t *testing.T) {
	for _, role := range []string{"junior", "senior", "ui", "verify"} {
		got, ok := Role(role)
		if !ok {
			t.Fatalf("Role(%q): expected embedded default, got none", role)
		}
		if strings.TrimSpace(got) == "" {
			t.Fatalf("Role(%q): embedded default is empty", role)
		}
	}
}

func TestRoleCaseInsensitive(t *testing.T) {
	lower, ok := Role("senior")
	if !ok {
		t.Fatal("Role(senior): expected embedded default")
	}
	upper, ok := Role("SENIOR")
	if !ok {
		t.Fatal("Role(SENIOR): expected embedded default")
	}
	if lower != upper {
		t.Fatalf("Role is case-sensitive: %q != %q", upper, lower)
	}
	if padded, ok := Role("  Senior  "); !ok || padded != lower {
		t.Fatalf("Role does not trim/fold: ok=%v %q", ok, padded)
	}
}

func TestRoleMissing(t *testing.T) {
	if _, ok := Role("nonexistent"); ok {
		t.Fatal("Role(nonexistent): expected no embedded default")
	}
	if _, ok := Role(""); ok {
		t.Fatal("Role(empty): expected no embedded default")
	}
}

func TestRolesStripSharedFinalizeBlock(t *testing.T) {
	// junior/senior/ui carried an identical "When you are done, always
	// remember to:" finalize block in the on-disk role docs. The embedded
	// defaults must rely on general/finalize.md instead of repeating it.
	for _, role := range []string{"junior", "senior", "ui"} {
		got, _ := Role(role)
		if strings.Contains(got, "When you are done, always remember to") {
			t.Errorf("Role(%q) still contains the shared finalize block", role)
		}
	}
}

func TestRolesList(t *testing.T) {
	got := Roles()
	want := []string{"junior", "senior", "ui", "verify"}
	if len(got) != len(want) {
		t.Fatalf("Roles() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Roles() = %v, want %v (sorted)", got, want)
		}
	}
}

func TestGeneralSnippets(t *testing.T) {
	fin, ok := General(GeneralFinalize)
	if !ok || strings.TrimSpace(fin) == "" {
		t.Fatal("General(finalize): expected non-empty embedded snippet")
	}
	// Finalize guidance must cover commit, laps done/handoff, and laps wrapup.
	for _, want := range []string{"Commit", "laps done", "laps handoff", "laps wrapup"} {
		if !strings.Contains(fin, want) {
			t.Errorf("finalize.md missing %q guidance", want)
		}
	}
	if Finalize() != fin {
		t.Error("Finalize() and General(finalize) disagree")
	}

	hl, ok := General(GeneralHeadless)
	if !ok || strings.TrimSpace(hl) == "" {
		t.Fatal("General(headless): expected non-empty embedded snippet")
	}
	// Headless guidance must convey non-interactivity and reliance on planning docs.
	for _, want := range []string{"non-interactive", "planning document"} {
		if !strings.Contains(hl, want) {
			t.Errorf("headless.md missing %q guidance", want)
		}
	}
	if Headless() != hl {
		t.Error("Headless() and General(headless) disagree")
	}
}

func TestGeneralMissing(t *testing.T) {
	if _, ok := General("nope"); ok {
		t.Fatal("General(nope): expected no embedded snippet")
	}
}

func TestGeneralsList(t *testing.T) {
	got := Generals()
	want := []string{"finalize", "headless", "leftover_work"}
	if len(got) != len(want) {
		t.Fatalf("Generals() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Generals() = %v, want %v (sorted)", got, want)
		}
	}
}
