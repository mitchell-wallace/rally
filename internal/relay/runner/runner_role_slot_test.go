package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent_prompt"
	"github.com/mitchell-wallace/rally/internal/store"
)

// resolveRoleInstructions fills the role slot from an on-disk override when one
// exists and otherwise falls back to the embedded role default.

func TestResolveRoleInstructionsEmbeddedDefault(t *testing.T) {
	// No .rally/agents/ files at all — the embedded default must be used.
	r := &Runner{cfg: Config{WorkspaceDir: t.TempDir(), LapsEnabled: true}}

	for _, role := range []string{"senior", "RECOVERY"} {
		got, err := r.resolveRoleInstructions(role)
		if err != nil {
			t.Fatalf("resolveRoleInstructions(%q): %v", role, err)
		}
		want, ok := agent_prompt.Role(role)
		if !ok {
			t.Fatalf("expected an embedded %s default", role)
		}
		if got != want {
			t.Fatalf("role slot for %s = %q, want embedded default %q", role, got, want)
		}
	}
}

func TestResolveRoleInstructionsOnDiskOverrides(t *testing.T) {
	workspaceDir := t.TempDir()
	agentsDir := store.AgentsDir(workspaceDir)
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := "Operator-owned senior guidance."
	if err := os.WriteFile(filepath.Join(agentsDir, "senior.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &Runner{cfg: Config{WorkspaceDir: workspaceDir, LapsEnabled: true}}
	got, err := r.resolveRoleInstructions("SENIOR")
	if err != nil {
		t.Fatalf("resolveRoleInstructions: %v", err)
	}
	if got != custom {
		t.Fatalf("role slot = %q, want on-disk override %q", got, custom)
	}
	// The override must replace only the role slot, never the embedded default
	// for a different role.
	if embedded, _ := agent_prompt.Role("senior"); strings.Contains(got, embedded) {
		t.Fatal("on-disk override should not be blended with the embedded default")
	}
}

func TestResolveRecoveryRoleInstructionsOnDiskOverrides(t *testing.T) {
	workspaceDir := t.TempDir()
	agentsDir := store.AgentsDir(workspaceDir)
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := "Operator-owned recovery guidance."
	if err := os.WriteFile(filepath.Join(agentsDir, "recovery.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &Runner{cfg: Config{WorkspaceDir: workspaceDir, LapsEnabled: true}}
	got, err := r.resolveRoleInstructions("RECOVERY")
	if err != nil {
		t.Fatalf("resolveRoleInstructions: %v", err)
	}
	if got != custom {
		t.Fatalf("role slot = %q, want on-disk override %q", got, custom)
	}
	if embedded, _ := agent_prompt.Role("recovery"); strings.Contains(got, embedded) {
		t.Fatal("on-disk recovery override should not be blended with the embedded default")
	}
}

func TestResolveRoleInstructionsDisabledWithoutLaps(t *testing.T) {
	r := &Runner{cfg: Config{WorkspaceDir: t.TempDir(), LapsEnabled: false}}
	got, err := r.resolveRoleInstructions("senior")
	if err != nil {
		t.Fatalf("resolveRoleInstructions: %v", err)
	}
	if got != "" {
		t.Fatalf("role slot = %q, want empty when laps disabled", got)
	}
}

func TestResolveRoleInstructionsUnknownRole(t *testing.T) {
	r := &Runner{cfg: Config{WorkspaceDir: t.TempDir(), LapsEnabled: true}}
	got, err := r.resolveRoleInstructions("does-not-exist")
	if err != nil {
		t.Fatalf("resolveRoleInstructions: %v", err)
	}
	if got != "" {
		t.Fatalf("role slot = %q, want empty for an unknown role with no override", got)
	}
}
