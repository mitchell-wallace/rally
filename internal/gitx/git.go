// Package gitx provides shared git helper functions used by the relay runner.
package gitx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func GitRepoRoot(dir string) (string, bool, error) {
	output, err := GitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", false, nil
		}
		return "", false, err
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return "", false, nil
	}
	return root, true, nil
}

func GitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, GitCommandError(args, out, err)
	}
	return out, nil
}

func GitCommandError(args []string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, detail)
}

func GitUserFallbackConfig(dir string) []string {
	var cfg []string
	if value, err := GitOutput(dir, "config", "--get", "user.name"); err != nil || strings.TrimSpace(string(value)) == "" {
		cfg = append(cfg, "-c", "user.name=Rally")
	}
	if value, err := GitOutput(dir, "config", "--get", "user.email"); err != nil || strings.TrimSpace(string(value)) == "" {
		cfg = append(cfg, "-c", "user.email=rally@localhost")
	}
	return cfg
}

func IsGitDirty(dir string) (bool, error) {
	out, err := GitOutput(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// IsWorkspaceDirty checks if there are user-agent workspace changes, excluding
// Rally's own workspace metadata: gitignored local state under .rally/ and the
// .laps/ queue/hook machinery that Rally rewrites on its own. This mirrors the
// rally-state exclusion in filesChangedList so a run that only churns Rally's
// bookkeeping is not mistaken for real user work.
func IsWorkspaceDirty(dir string) (bool, error) {
	out, err := GitOutput(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			path := parts[1]
			if strings.HasPrefix(path, ".rally/") || strings.HasPrefix(path, ".laps/") {
				continue
			}
		}
		return true, nil
	}
	return false, nil
}

// CommitRallyState commits Rally's non-state operational files (config, instructions, etc).
// State files under .rally/state/ are gitignored and never committed.
func CommitRallyState(dir string) error {
	_, inGit, err := GitRepoRoot(dir)
	if err != nil || !inGit {
		return nil
	}

	out, err := GitOutput(dir, "status", "--porcelain", ".rally/")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil
	}

	var existingTrackedPaths []string
	for _, path := range rallyTrackedStatePaths {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if _, err := GitOutput(dir, "add", path); err != nil {
			// Tolerate operator-gitignored .rally operational paths: skip the
			// path silently (never `-f`, never abort) so the remaining tracked
			// state still commits. Confirm the add actually failed because the
			// path is gitignored before swallowing the error, so unrelated git
			// failures still surface. This tolerance is scoped to these .rally
			// operational paths and never applies to .laps/laps.json.
			if pathIsGitIgnored(dir, path) {
				continue
			}
			return err
		}
		existingTrackedPaths = append(existingTrackedPaths, path)
	}
	if len(existingTrackedPaths) == 0 {
		return nil
	}

	diffArgs := append([]string{"diff", "--cached", "--name-only", "--"}, existingTrackedPaths...)
	cached, err := GitOutput(dir, diffArgs...)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(cached)) == "" {
		return nil
	}

	args := append(GitUserFallbackConfig(dir), "commit", "--no-verify", "-m", "rally: update state", "--")
	args = append(args, existingTrackedPaths...)
	if commitOut, err := GitOutput(dir, args...); err != nil {
		if strings.Contains(string(commitOut), "nothing to commit") || strings.Contains(string(commitOut), "no changes added") {
			return nil
		}
		return err
	}
	return nil
}

// pathIsGitIgnored reports whether path is excluded by a .gitignore rule.
// It uses `git check-ignore`, which exits 0 when the path is ignored, 1 when it
// is not, and 2 on error. Only a clean exit 0 is treated as ignored, so an
// unrelated git failure (exit 2) does not get mistaken for the ignored
// condition and continues to surface to the caller.
func pathIsGitIgnored(dir, path string) bool {
	cmd := exec.Command("git", "-C", dir, "check-ignore", "-q", "--", path)
	return cmd.Run() == nil
}

var rallyTrackedStatePaths = []string{
	".rally/.gitignore",
	".rally/README.md",
	".rally/config.toml",
	".rally/instructions.md",
	".rally/agents",
	".rally/summary.jsonl",
}
