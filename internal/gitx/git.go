// Package gitx provides shared git helper functions used by the relay runner.
package gitx

import (
	"errors"
	"fmt"
	"os/exec"
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

// IsWorkspaceDirty checks if there are user-agent workspace changes,
// excluding Rally's own operational files under .rally/.
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

	if _, err := GitOutput(dir, "add", ".rally/"); err != nil {
		return err
	}

	args := append(GitUserFallbackConfig(dir), "commit", "--no-verify", "-m", "rally: update state")
	if _, err := GitOutput(dir, args...); err != nil {
		if strings.Contains(string(out), "nothing to commit") {
			return nil
		}
		return err
	}
	return nil
}
