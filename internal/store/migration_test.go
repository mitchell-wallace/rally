package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mitchell-wallace/rally/internal/store"
)

func TestMigrateRallyStateLayout(t *testing.T) {
	tests := []struct {
		name string
		setup func(t *testing.T, workspaceDir string)
		verify func(t *testing.T, workspaceDir string)
	}{
		{
			name: "HappyPath",
			setup: func(t *testing.T, workspaceDir string) {
				rallyDir := store.RallyDir(workspaceDir)
				os.MkdirAll(rallyDir, 0755)

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
					os.WriteFile(filepath.Join(rallyDir, lf), []byte("legacy "+lf), 0644)
				}

				// Create legacy dirs
				os.MkdirAll(filepath.Join(rallyDir, "batches"), 0755)
				os.MkdirAll(filepath.Join(rallyDir, "relays"), 0755)

				// Create untouched files
				os.WriteFile(filepath.Join(rallyDir, "progress.yaml"), []byte("verbatim"), 0644)
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
			},
		},
		{
			name: "NoOverwrite",
			setup: func(t *testing.T, workspaceDir string) {
				rallyDir := store.RallyDir(workspaceDir)
				stateDir := store.StateDir(workspaceDir)
				os.MkdirAll(rallyDir, 0755)
				os.MkdirAll(stateDir, 0755)

				// Create legacy flat file
				os.WriteFile(filepath.Join(rallyDir, "tries.jsonl"), []byte("legacy tries"), 0644)

				// Create existing state target
				os.WriteFile(filepath.Join(stateDir, "tries.jsonl"), []byte("existing tries"), 0644)
			},
			verify: func(t *testing.T, workspaceDir string) {
				stateDir := store.StateDir(workspaceDir)

				// Ensure existing state target not overwritten
				content, err := os.ReadFile(filepath.Join(stateDir, "tries.jsonl"))
				if err != nil {
					t.Errorf("state tries.jsonl missing")
				} else if string(content) != "existing tries" {
					t.Errorf("state tries.jsonl was overwritten, expected 'existing tries'")
				}

				// The legacy file is untouched because move fails
				// Wait, the logic says "if err := moveIfTargetMissing(src, dst)".
				// Since target is not missing, it does nothing and returns nil. So legacy file is kept? Let's check implementation.
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
