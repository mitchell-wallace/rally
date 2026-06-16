package agent_prompt

import (
	"strings"
	"testing"
)

func TestRoleEmbeddedDefaults(t *testing.T) {
	for _, role := range []string{"junior", "senior", "ui", "verify", "recovery"} {
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
	recoveryLower, ok := Role("recovery")
	if !ok {
		t.Fatal("Role(recovery): expected embedded default")
	}
	recoveryUpper, ok := Role("RECOVERY")
	if !ok {
		t.Fatal("Role(RECOVERY): expected embedded default")
	}
	if recoveryLower != recoveryUpper {
		t.Fatalf("Role(recovery) is case-sensitive: %q != %q", recoveryUpper, recoveryLower)
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
	want := []string{"junior", "recovery", "senior", "ui", "verify"}
	if len(got) != len(want) {
		t.Fatalf("Roles() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Roles() = %v, want %v (sorted)", got, want)
		}
	}
}

func TestRecoveryRoleClassificationContract(t *testing.T) {
	got, ok := Role("recovery")
	if !ok {
		t.Fatal("Role(recovery): expected embedded default")
	}
	for _, want := range []string{"continue", "discard", "course_correct", "repair_plan", "needs_user"} {
		if !strings.Contains(got, "`"+want+"`") {
			t.Errorf("recovery role missing classification %q", want)
		}
	}
	for _, want := range []string{"Classify first, then act", "laps wrapup --classification <value>"} {
		if !strings.Contains(got, want) {
			t.Errorf("recovery role missing %q guidance", want)
		}
	}
	if strings.Contains(strings.ToLower(got), "openspec") {
		t.Fatalf("recovery role must stay OpenSpec-agnostic:\n%s", got)
	}
}

func TestVoluntaryHandoffGuidanceOnlyInImplementationRoles(t *testing.T) {
	want := []string{"five serious debugging iterations", "A debugging iteration is one loop of", "Use your honest judgment"}
	for _, role := range []string{"junior", "senior", "ui"} {
		got, _ := Role(role)
		for _, phrase := range want {
			if !strings.Contains(got, phrase) {
				t.Errorf("Role(%q) missing voluntary handoff phrase %q", role, phrase)
			}
		}
	}
	for _, role := range []string{"verify", "recovery"} {
		got, _ := Role(role)
		for _, phrase := range want {
			if strings.Contains(got, phrase) {
				t.Errorf("Role(%q) unexpectedly contains voluntary handoff phrase %q", role, phrase)
			}
		}
	}
}

func TestGeneralSnippets(t *testing.T) {
	fin, ok := General(GeneralFinalize)
	if !ok || strings.TrimSpace(fin) == "" {
		t.Fatal("General(finalize): expected non-empty embedded snippet")
	}
	// Finalize guidance must cover commit, claimed-lap completion, handoff,
	// wrapup, and undo recovery.
	for _, want := range []string{"Commit", "claimed", "laps done", "laps handoff", "laps wrapup", "laps done undo"} {
		if !strings.Contains(fin, want) {
			t.Errorf("finalize.md missing %q guidance", want)
		}
	}
	if Finalize() != fin {
		t.Error("Finalize() and General(finalize) disagree")
	}

	ho, ok := General(GeneralHandoffOnly)
	if !ok || strings.TrimSpace(ho) == "" {
		t.Fatal("General(handoff_only): expected non-empty embedded snippet")
	}
	for _, want := range []string{"Do not continue implementation", "changed files", "laps handoff", "laps wrapup"} {
		if !strings.Contains(ho, want) {
			t.Errorf("handoff_only.md missing %q guidance", want)
		}
	}
	if HandoffOnly() != ho {
		t.Error("HandoffOnly() and General(handoff_only) disagree")
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
	want := []string{"finalize", "handoff_only", "headless", "leftover_work"}
	if len(got) != len(want) {
		t.Fatalf("Generals() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Generals() = %v, want %v (sorted)", got, want)
		}
	}
}
