package store

import (
	"fmt"
	"os"
	"path/filepath"
)

var legacyStateFiles = []string{
	"tries.jsonl",
	"messages.jsonl",
	"relays.jsonl",
	"agent_status.jsonl",
	"hook-audit.jsonl",
	"run-state.json",
	"current_task.md",
}

var legacyStateDirs = []string{
	"batches",
	"relays",
}

// MigrateRallyStateLayout moves legacy top-level runtime files into
// .rally/state. Existing targets are never overwritten, so interrupted or
// partially completed migrations can be retried safely.
func MigrateRallyStateLayout(workspaceDir string) error {
	rallyDir := RallyDir(workspaceDir)
	stateDir := StateDir(workspaceDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create rally state dir: %w", err)
	}

	for _, name := range legacyStateFiles {
		src := filepath.Join(rallyDir, name)
		dst := filepath.Join(stateDir, name)
		if err := moveIfTargetMissing(src, dst); err != nil {
			return fmt.Errorf("migrate %s: %w", name, err)
		}
	}

	for _, name := range legacyStateDirs {
		path := filepath.Join(rallyDir, name)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("stat legacy %s dir: %w", name, err)
		}
		if !info.IsDir() {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove legacy %s dir: %w", name, err)
		}
	}

	return nil
}

func moveIfTargetMissing(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if srcInfo.IsDir() {
		return nil
	}

	if _, err := os.Stat(dst); err == nil {
		// A partially migrated workspace may already have the state/ target.
		// Keep the existing destination and drop the stale legacy source so the
		// root .rally/ layout still converges to the new shape.
		if err := os.Remove(src); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}
