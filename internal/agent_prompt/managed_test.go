package agent_prompt

import "testing"

func TestIsManagedRoleContent_CurrentEmbeddedIsManaged(t *testing.T) {
	for _, role := range Roles() {
		body, ok := Role(role)
		if !ok {
			t.Fatalf("no embedded body for role %q", role)
		}
		// On-disk role files are written as the embedded body plus a trailing
		// newline; managed detection must be insensitive to that.
		if !IsManagedRoleContent(role, body+"\n") {
			t.Errorf("current embedded %q should be recognized as managed", role)
		}
		if !IsManagedRoleContent(role, "  "+body+"  \n\n") {
			t.Errorf("whitespace-padded embedded %q should be managed", role)
		}
	}
}

func TestIsManagedRoleContent_CaseInsensitiveRole(t *testing.T) {
	body, _ := Role("junior")
	if !IsManagedRoleContent("JUNIOR", body) {
		t.Error("role match should be case-insensitive")
	}
}

func TestIsManagedRoleContent_EditedContentIsNotManaged(t *testing.T) {
	body, _ := Role("junior")
	if IsManagedRoleContent("junior", body+"\n\n- my own extra rule\n") {
		t.Error("user-edited content must not be classified as managed")
	}
	if IsManagedRoleContent("junior", "completely different") {
		t.Error("unrelated content must not be managed")
	}
}

func TestIsManagedRoleContent_ExtraBodiesMatch(t *testing.T) {
	// The bootstrap text is passed by the migrator as an extra canonical body.
	bootstrap := "# Verify Role\n\nsome historical bootstrap body\n"
	if !IsManagedRoleContent("verify", bootstrap, bootstrap) {
		t.Error("content matching an extra canonical body should be managed")
	}
	if IsManagedRoleContent("verify", bootstrap) {
		t.Error("without the extra body, unknown content should not be managed")
	}
}

func TestManagedHashManifest_CoversAllEmbeddedRoles(t *testing.T) {
	for _, role := range Roles() {
		if len(managedRoleContentHashes[role]) == 0 {
			t.Errorf("manifest has no hashes for embedded role %q; regenerate scripts/gen_managed_role_hashes.sh", role)
		}
	}
}
