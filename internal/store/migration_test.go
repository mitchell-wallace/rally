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
			name: "NoOverwrite",
			setup: func(t *testing.T, workspaceDir string) {
				rallyDir := store.RallyDir(workspaceDir)
				stateDir := store.StateDir(workspaceDir)
				if err := os.MkdirAll(rallyDir, 0o755); err != nil {
					t.Fatalf("mkdir rally dir: %v", err)
				}
				if err := os.MkdirAll(stateDir, 0o755); err != nil {
					t.Fatalf("mkdir state dir: %v", err)
				}

				// Create legacy flat file
				if err := os.WriteFile(filepath.Join(rallyDir, "tries.jsonl"), []byte("legacy tries"), 0o644); err != nil {
					t.Fatalf("write legacy tries: %v", err)
				}

				// Create existing state target
				if err := os.WriteFile(filepath.Join(stateDir, "tries.jsonl"), []byte("existing tries"), 0o644); err != nil {
					t.Fatalf("write state tries: %v", err)
				}
			},
			verify: func(t *testing.T, workspaceDir string) {
				rallyDir := store.RallyDir(workspaceDir)
				stateDir := store.StateDir(workspaceDir)

				// Ensure existing state target not overwritten
				content, err := os.ReadFile(filepath.Join(stateDir, "tries.jsonl"))
				if err != nil {
					t.Errorf("state tries.jsonl missing")
				} else if string(content) != "existing tries" {
					t.Errorf("state tries.jsonl was overwritten, expected 'existing tries'")
				}
				if _, err := os.Stat(filepath.Join(rallyDir, "tries.jsonl")); !os.IsNotExist(err) {
					t.Errorf("legacy tries.jsonl should be removed once state/tries.jsonl already exists")
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
