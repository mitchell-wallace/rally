package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent_prompt"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/user_prompt/roleloader"
)

func TestSyncRoleFolders_MigratesFlatFilesAndRegeneratesBuiltin(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := store.AgentsDir(tmp)
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// A pristine (managed) flat file: the current embedded junior body.
	juniorBody, _ := agent_prompt.Role("junior")
	mustWriteFile(t, filepath.Join(agentsDir, "junior.md"), juniorBody+"\n")
	// Managed via the bootstrap text (the verify flat file predates the embed).
	mustWriteFile(t, filepath.Join(agentsDir, "verify.md"), bootstrapInstructionsFor("verify"))
	// A user-customized flat file must be preserved.
	mustWriteFile(t, filepath.Join(agentsDir, "senior.md"), "# Senior\n\nmy custom guidance\n")

	if err := syncRoleFolders(tmp); err != nil {
		t.Fatalf("syncRoleFolders: %v", err)
	}

	// Managed files migrated out of the flat layout.
	for _, name := range []string{"junior.md", "verify.md"} {
		if _, err := os.Stat(filepath.Join(agentsDir, name)); !os.IsNotExist(err) {
			t.Errorf("flat %s should have been migrated away (stat err=%v)", name, err)
		}
	}

	// builtin/ regenerated from the embedded defaults for every embedded role.
	for _, role := range agent_prompt.Roles() {
		body, _ := agent_prompt.Role(role)
		got, err := os.ReadFile(filepath.Join(store.AgentsBuiltinDir(tmp), role+".md"))
		if err != nil {
			t.Fatalf("read builtin %s: %v", role, err)
		}
		if string(got) != body+"\n" {
			t.Errorf("builtin %s was not regenerated from embedded defaults", role)
		}
	}

	// Customized file preserved in user/ and wins in the loader.
	userSenior, err := os.ReadFile(filepath.Join(store.AgentsUserDir(tmp), "senior.md"))
	if err != nil {
		t.Fatalf("read user senior: %v", err)
	}
	if !strings.Contains(string(userSenior), "my custom guidance") {
		t.Error("customized senior should be preserved in user/")
	}

	loadedSenior, err := roleloader.Loader{WorkspaceDir: tmp}.Load("senior")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loadedSenior, "my custom guidance") {
		t.Error("loader should resolve the user/ override over builtin/")
	}
	loadedJunior, _ := roleloader.Loader{WorkspaceDir: tmp}.Load("junior")
	if strings.TrimSpace(loadedJunior) != strings.TrimSpace(juniorBody) {
		t.Error("loader should resolve builtin/ junior after migration")
	}
}

func TestSyncRoleFolders_IdempotentAndDoesNotClobberUser(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(store.AgentsUserDir(tmp), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing user override.
	mustWriteFile(t, filepath.Join(store.AgentsUserDir(tmp), "junior.md"), "# custom junior\n")

	if err := syncRoleFolders(tmp); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if err := syncRoleFolders(tmp); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(store.AgentsUserDir(tmp), "junior.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# custom junior\n" {
		t.Errorf("user override was modified by sync: %q", string(got))
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
