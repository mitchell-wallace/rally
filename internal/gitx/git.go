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
// Rally's own workspace metadata and gitignored local state under .rally/.
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
			if strings.HasPrefix(path, ".rally/") {
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
		existingTrackedPaths = append(existingTrackedPaths, path)
		if _, err := GitOutput(dir, "add", path); err != nil {
			return err
		}
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

var rallyTrackedStatePaths = []string{
	".rally/.gitignore",
	".rally/README.md",
	".rally/config.toml",
	".rally/instructions.md",
	".rally/agents",
	".rally/summary.jsonl",
}
