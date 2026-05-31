package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mitchell-wallace/rally/internal/store"
)

func TestMigrateRallyStateLayout(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, workspaceDir string)
		verify func(t *testing.T, workspaceDir string)
	}{
		{
			name: "HappyPath",
			setup: func(t *testing.T, workspaceDir string) {
				rallyDir := store.RallyDir(workspaceDir)
				if err := os.MkdirAll(rallyDir, 0o755); err != nil {
					t.Fatalf("mkdir rally dir: %v", err)
				}

				// Create legacy flat files
				legacyFiles := []string{
					"tries.jsonl",
					"messages.jsonl",
					"relays.jsonl",
					"agent_status.jsonl",
					"hook-audit.jsonl",
					"run-state.json",
					"current_task.md",
				}
				for _, lf := range legacyFiles {
					if err := os.WriteFile(filepath.Join(rallyDir, lf), []byte("legacy "+lf), 0o644); err != nil {
						t.Fatalf("write %s: %v", lf, err)
					}
				}

				// Create legacy dirs
				if err := os.MkdirAll(filepath.Join(rallyDir, "batches"), 0o755); err != nil {
					t.Fatalf("mkdir batches: %v", err)
				}
				if err := os.MkdirAll(filepath.Join(rallyDir, "relays"), 0o755); err != nil {
					t.Fatalf("mkdir relays: %v", err)
				}

				// Create untouched user-managed files.
				if err := os.WriteFile(filepath.Join(rallyDir, "progress.yaml"), []byte("verbatim"), 0o644); err != nil {
					t.Fatalf("write progress.yaml: %v", err)
				}
				if err := os.WriteFile(filepath.Join(rallyDir, "config.toml.bak"), []byte("backup"), 0o644); err != nil {
					t.Fatalf("write config backup: %v", err)
				}
			},
			verify: func(t *testing.T, workspaceDir string) {
				rallyDir := store.RallyDir(workspaceDir)
				stateDir := store.StateDir(workspaceDir)

				// Check files were moved
				legacyFiles := []string{
					"tries.jsonl",
					"messages.jsonl",
					"relays.jsonl",
					"agent_status.jsonl",
					"hook-audit.jsonl",
					"run-state.json",
					"current_task.md",
				}
				for _, lf := range legacyFiles {
					if _, err := os.Stat(filepath.Join(rallyDir, lf)); !os.IsNotExist(err) {
						t.Errorf("legacy file %s still exists in root", lf)
					}
					content, err := os.ReadFile(filepath.Join(stateDir, lf))
					if err != nil {
						t.Errorf("failed to read migrated file %s: %v", lf, err)
					} else if string(content) != "legacy "+lf {
						t.Errorf("content mismatch for %s", lf)
					}
				}

				// Check dirs were removed
				if _, err := os.Stat(filepath.Join(rallyDir, "batches")); !os.IsNotExist(err) {
					t.Errorf("batches dir still exists")
				}
				if _, err := os.Stat(filepath.Join(rallyDir, "relays")); !os.IsNotExist(err) {
					t.Errorf("relays dir still exists")
				}

				// Check progress.yaml untouched
				content, err := os.ReadFile(filepath.Join(rallyDir, "progress.yaml"))
				if err != nil {
					t.Errorf("progress.yaml missing")
				} else if string(content) != "verbatim" {
					t.Errorf("progress.yaml changed")
				}
				content, err = os.ReadFile(filepath.Join(rallyDir, "config.toml.bak"))
				if err != nil {
					t.Errorf("config.toml.bak missing")
				} else if string(content) != "backup" {
					t.Errorf("config.toml.bak changed")
				}
			},
		},
		{
			name: "IdenticalTargetDropsRedundantSource",
			setup: func(t *testing.T, workspaceDir string) {
				rallyDir := store.RallyDir(workspaceDir)
				stateDir := store.StateDir(workspaceDir)
				if err := os.MkdirAll(rallyDir, 0o755); err != nil {
					t.Fatalf("mkdir rally dir: %v", err)
				}
				if err := os.MkdirAll(stateDir, 0o755); err != nil {
					t.Fatalf("mkdir state dir: %v", err)
				}

				// Legacy source and migrated target hold the same bytes, as
				// would happen if a prior migration copied (not moved) the file.
				if err := os.WriteFile(filepath.Join(rallyDir, "tries.jsonl"), []byte("same tries"), 0o644); err != nil {
					t.Fatalf("write legacy tries: %v", err)
				}
				if err := os.WriteFile(filepath.Join(stateDir, "tries.jsonl"), []byte("same tries"), 0o644); err != nil {
					t.Fatalf("write state tries: %v", err)
				}
			},
			verify: func(t *testing.T, workspaceDir string) {
				rallyDir := store.RallyDir(workspaceDir)
				stateDir := store.StateDir(workspaceDir)

				content, err := os.ReadFile(filepath.Join(stateDir, "tries.jsonl"))
				if err != nil {
					t.Errorf("state tries.jsonl missing")
				} else if string(content) != "same tries" {
					t.Errorf("state tries.jsonl content changed, got %q", string(content))
				}
				// Redundant legacy copy should be dropped to converge the layout.
				if _, err := os.Stat(filepath.Join(rallyDir, "tries.jsonl")); !os.IsNotExist(err) {
					t.Errorf("redundant legacy tries.jsonl should be removed when target is identical")
				}
				// No conflict copy should be left behind.
				if _, err := os.Stat(filepath.Join(stateDir, "tries.jsonl.legacy-conflict")); !os.IsNotExist(err) {
					t.Errorf("unexpected conflict file for identical contents")
				}
			},
		},
		{
			name: "ConflictingTargetPreservesLegacyData",
			setup: func(t *testing.T, workspaceDir string) {
				rallyDir := store.RallyDir(workspaceDir)
				stateDir := store.StateDir(workspaceDir)
				if err := os.MkdirAll(rallyDir, 0o755); err != nil {
					t.Fatalf("mkdir rally dir: %v", err)
				}
				if err := os.MkdirAll(stateDir, 0o755); err != nil {
					t.Fatalf("mkdir state dir: %v", err)
				}

				// Partially migrated workspace where source and destination
				// both exist with DIFFERENT contents.
				if err := os.WriteFile(filepath.Join(rallyDir, "tries.jsonl"), []byte("legacy tries"), 0o644); err != nil {
					t.Fatalf("write legacy tries: %v", err)
				}
				if err := os.WriteFile(filepath.Join(stateDir, "tries.jsonl"), []byte("existing tries"), 0o644); err != nil {
					t.Fatalf("write state tries: %v", err)
				}
			},
			verify: func(t *testing.T, workspaceDir string) {
				rallyDir := store.RallyDir(workspaceDir)
				stateDir := store.StateDir(workspaceDir)

				// Existing migrated target must not be overwritten.
				content, err := os.ReadFile(filepath.Join(stateDir, "tries.jsonl"))
				if err != nil {
					t.Errorf("state tries.jsonl missing")
				} else if string(content) != "existing tries" {
					t.Errorf("state tries.jsonl was overwritten, expected 'existing tries', got %q", string(content))
				}

				// The legacy file must NOT be deleted blindly; its data must be
				// preserved beside the target as a conflict copy.
				conflict := filepath.Join(stateDir, "tries.jsonl.legacy-conflict")
				preserved, err := os.ReadFile(conflict)
				if err != nil {
					t.Fatalf("legacy data lost: conflict copy missing: %v", err)
				}
				if string(preserved) != "legacy tries" {
					t.Errorf("preserved conflict copy content mismatch, got %q", string(preserved))
				}

				// The top-level legacy file should no longer occupy the old path.
				if _, err := os.Stat(filepath.Join(rallyDir, "tries.jsonl")); !os.IsNotExist(err) {
					t.Errorf("legacy tries.jsonl should be relocated out of the top-level layout")
				}
			},
		},
		{
			name: "RepeatedConflictKeepsExistingPreservedCopy",
			setup: func(t *testing.T, workspaceDir string) {
				rallyDir := store.RallyDir(workspaceDir)
				stateDir := store.StateDir(workspaceDir)
				if err := os.MkdirAll(rallyDir, 0o755); err != nil {
					t.Fatalf("mkdir rally dir: %v", err)
				}
				if err := os.MkdirAll(stateDir, 0o755); err != nil {
					t.Fatalf("mkdir state dir: %v", err)
				}

				// A conflict copy from an earlier migration already exists, and a
				// brand new (third) legacy variant has reappeared at the root.
				if err := os.WriteFile(filepath.Join(rallyDir, "tries.jsonl"), []byte("third variant"), 0o644); err != nil {
					t.Fatalf("write legacy tries: %v", err)
				}
				if err := os.WriteFile(filepath.Join(stateDir, "tries.jsonl"), []byte("existing tries"), 0o644); err != nil {
					t.Fatalf("write state tries: %v", err)
				}
				if err := os.WriteFile(filepath.Join(stateDir, "tries.jsonl.legacy-conflict"), []byte("first preserved"), 0o644); err != nil {
					t.Fatalf("write conflict tries: %v", err)
				}
			},
			verify: func(t *testing.T, workspaceDir string) {
				rallyDir := store.RallyDir(workspaceDir)
				stateDir := store.StateDir(workspaceDir)

				// Existing preserved conflict copy must never be clobbered.
				preserved, err := os.ReadFile(filepath.Join(stateDir, "tries.jsonl.legacy-conflict"))
				if err != nil {
					t.Fatalf("preserved conflict copy missing: %v", err)
				}
				if string(preserved) != "first preserved" {
					t.Errorf("preserved conflict copy was clobbered, got %q", string(preserved))
				}
				// The new variant must not be lost: with nowhere safe to move it,
				// it stays in place for manual reconciliation.
				root, err := os.ReadFile(filepath.Join(rallyDir, "tries.jsonl"))
				if err != nil {
					t.Fatalf("new legacy variant lost: %v", err)
				}
				if string(root) != "third variant" {
					t.Errorf("new legacy variant changed, got %q", string(root))
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workspaceDir := t.TempDir()
			tc.setup(t, workspaceDir)

			if err := store.MigrateRallyStateLayout(workspaceDir); err != nil {
				t.Fatalf("MigrateRallyStateLayout failed: %v", err)
			}

			// Idempotency check: run it again
			if err := store.MigrateRallyStateLayout(workspaceDir); err != nil {
				t.Fatalf("MigrateRallyStateLayout second run failed: %v", err)
			}

			tc.verify(t, workspaceDir)
		})
	}
}
