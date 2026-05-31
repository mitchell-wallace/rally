package store

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// legacyConflictSuffix is appended to a preserved legacy state file when its
// contents differ from an already-migrated target. The data is kept under
// state/ (a gitignored location) so nothing is lost and the operator can
// reconcile the two copies manually.
const legacyConflictSuffix = ".legacy-conflict"

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
// partially completed migrations can be retried safely. When a legacy source
// and its already-migrated target both exist with differing contents, the
// legacy file is preserved (never deleted blindly) so no data is lost.
func MigrateRallyStateLayout(workspaceDir string) error {
	rallyDir := RallyDir(workspaceDir)
	stateDir := StateDir(workspaceDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create rally state dir: %w", err)
	}

	for _, name := range legacyStateFiles {
		src := filepath.Join(rallyDir, name)
		dst := filepath.Join(stateDir, name)
		if err := migrateStateFile(src, dst); err != nil {
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

// migrateStateFile relocates a single legacy state file into the new layout.
// It is idempotent and conflict-safe:
//   - no source: nothing to do.
//   - no target: move source into place.
//   - target exists, contents identical: drop the redundant legacy source.
//   - target exists, contents differ: preserve the legacy source rather than
//     delete it, so the operator can reconcile without data loss.
func migrateStateFile(src, dst string) error {
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

	dstInfo, err := os.Stat(dst)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.Rename(src, dst)
	}
	if err != nil {
		return err
	}
	if dstInfo.IsDir() {
		// A directory sits where a state file is expected; leave the legacy
		// source untouched rather than risk clobbering anything.
		return fmt.Errorf("target %s is a directory", dst)
	}

	same, err := filesIdentical(src, dst)
	if err != nil {
		return err
	}
	if same {
		// The target already holds the same bytes, so the legacy copy is
		// redundant and safe to remove. This keeps the root layout converging
		// to the new shape and the migration idempotent.
		return removeIfExists(src)
	}

	return preserveLegacyConflict(src, dst)
}

// preserveLegacyConflict relocates a conflicting legacy source out of the
// top-level layout without losing it. The bytes land beside the migrated
// target as <dst><legacyConflictSuffix>. An existing conflict file is never
// overwritten; if one already holds the same bytes the redundant source is
// dropped, otherwise the source is left in place for the operator to resolve.
func preserveLegacyConflict(src, dst string) error {
	conflict := dst + legacyConflictSuffix

	if _, err := os.Stat(conflict); err == nil {
		same, err := filesIdentical(src, conflict)
		if err != nil {
			return err
		}
		if same {
			return removeIfExists(src)
		}
		// A different conflict copy is already preserved. Don't clobber it and
		// don't delete the source; leave both for manual reconciliation.
		fmt.Fprintf(os.Stderr,
			"rally: %s conflicts with already-preserved %s; leaving legacy file in place\n",
			src, conflict)
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(conflict), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, conflict); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr,
		"rally: %s differed from migrated state; preserved legacy copy at %s\n",
		src, conflict)
	return nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// filesIdentical reports whether two files hold the same bytes. A size
// mismatch short-circuits the byte comparison.
func filesIdentical(a, b string) (bool, error) {
	aInfo, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bInfo, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if aInfo.Size() != bInfo.Size() {
		return false, nil
	}

	af, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer af.Close()
	bf, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer bf.Close()

	const chunk = 64 * 1024
	abuf := make([]byte, chunk)
	bbuf := make([]byte, chunk)
	for {
		an, aerr := io.ReadFull(af, abuf)
		bn, berr := io.ReadFull(bf, bbuf)
		if an != bn || !bytes.Equal(abuf[:an], bbuf[:bn]) {
			return false, nil
		}
		if aerr == io.EOF || aerr == io.ErrUnexpectedEOF {
			// Sizes matched up front, so both streams end together.
			return true, nil
		}
		if aerr != nil {
			return false, aerr
		}
		if berr != nil {
			return false, berr
		}
	}
}
