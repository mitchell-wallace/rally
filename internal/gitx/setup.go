package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CommitSetupFiles stages exactly the listed paths and commits them with
// the given message. It returns true if a commit was made, false if nothing
// was staged. It tolerates non-git repos and git failures gracefully
// (returns an error rather than panicking) and uses --no-verify to avoid
// tripping repository hooks during tooling setup.
func CommitSetupFiles(workspaceDir string, paths []string, message string) (bool, error) {
	_, inGit, err := GitRepoRoot(workspaceDir)
	if err != nil || !inGit {
		return false, nil // not a git repo — nothing to do
	}

	// Stage only the specific setup paths that exist on disk.
	for _, p := range paths {
		abs := filepath.Join(workspaceDir, p)
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			continue
		}
		if _, err := GitOutput(workspaceDir, "add", "--", p); err != nil {
			// The path might be gitignored; skip it silently.
			continue
		}
	}

	// Check whether anything is actually staged for the listed paths.
	diffArgs := append([]string{"diff", "--cached", "--name-only", "--"}, paths...)
	cached, err := GitOutput(workspaceDir, diffArgs...)
	if err != nil {
		return false, fmt.Errorf("check staged files: %w", err)
	}
	if strings.TrimSpace(string(cached)) == "" {
		return false, nil // nothing staged — no-op
	}

	// Commit only the staged setup paths.
	args := append(GitUserFallbackConfig(workspaceDir), "commit", "--no-verify", "-m", message, "--")
	args = append(args, paths...)
	if out, err := GitOutput(workspaceDir, args...); err != nil {
		// "nothing to commit" is benign — treat as no-op.
		if strings.Contains(string(out), "nothing to commit") || strings.Contains(string(out), "no changes added") {
			return false, nil
		}
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}
