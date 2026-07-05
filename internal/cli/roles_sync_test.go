package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent_prompt"
	"github.com/mitchell-wallace/rally/internal/roles"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/user_prompt/roleloader"
)

func TestSyncRoleFolders_FreshGeneratesEightBuiltins(t *testing.T) {
	tmp := t.TempDir()

	if err := syncRoleFolders(tmp); err != nil {
		t.Fatalf("syncRoleFolders: %v", err)
	}

	entries, err := os.ReadDir(store.AgentsBuiltinDir(tmp))
	if err != nil {
		t.Fatalf("read builtin dir: %v", err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			got = append(got, entry.Name())
		}
	}
	want := []string{"architect.md", "intern.md", "junior.md", "qa.md", "recovery.md", "review.md", "senior.md", "verify.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("builtin files = %v, want %v", got, want)
	}
}

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

func TestSyncRoleFolders_RemovesManagedBuiltinUIAndPreservesUserUI(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(store.AgentsBuiltinDir(tmp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.AgentsUserDir(tmp), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(store.AgentsBuiltinDir(tmp), "ui.md"), bootstrapInstructionsFor("ui"))
	mustWriteFile(t, filepath.Join(store.AgentsUserDir(tmp), "ui.md"), "# custom ui\n")

	if err := syncRoleFolders(tmp); err != nil {
		t.Fatalf("syncRoleFolders: %v", err)
	}

	if _, err := os.Stat(filepath.Join(store.AgentsBuiltinDir(tmp), "ui.md")); !os.IsNotExist(err) {
		t.Fatalf("managed builtin ui.md should be removed (stat err=%v)", err)
	}
	got, err := os.ReadFile(filepath.Join(store.AgentsUserDir(tmp), "ui.md"))
	if err != nil {
		t.Fatalf("read user ui.md: %v", err)
	}
	if string(got) != "# custom ui\n" {
		t.Fatalf("user ui.md = %q, want preserved override", string(got))
	}
}

func TestSyncRoleFolders_MigratesLegacyFlatManagedUI(t *testing.T) {
	tmp := t.TempDir()
	agentsDir := store.AgentsDir(tmp)
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(agentsDir, "ui.md"), bootstrapInstructionsFor("ui"))

	if err := syncRoleFolders(tmp); err != nil {
		t.Fatalf("syncRoleFolders: %v", err)
	}

	if _, err := os.Stat(filepath.Join(agentsDir, "ui.md")); !os.IsNotExist(err) {
		t.Fatalf("legacy flat ui.md should be migrated away (stat err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(store.AgentsBuiltinDir(tmp), "ui.md")); !os.IsNotExist(err) {
		t.Fatalf("managed builtin ui.md should not be regenerated (stat err=%v)", err)
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

func TestDefaultRoleBootstrapsMatchCatalogRoutes(t *testing.T) {
	got := make(map[string][]string)
	for _, rb := range defaultRoleBootstraps {
		got[rb.Name] = rb.Route
	}

	want := make(map[string][]string)
	for _, spec := range roles.Builtins() {
		want[spec.Name] = spec.DefaultRoute
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultRoleBootstraps = %#v, want %#v", got, want)
	}
	if _, ok := got["ui"]; ok {
		t.Fatal("defaultRoleBootstraps should not include ui")
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
