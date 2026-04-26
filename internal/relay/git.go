package relay

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func gitRepoRoot(dir string) (string, bool, error) {
	output, err := gitOutput(dir, "rev-parse", "--show-toplevel")
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

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, gitCommandError(args, out, err)
	}
	return out, nil
}

func gitCommandError(args []string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, detail)
}

func gitUserFallbackConfig(dir string) []string {
	var cfg []string
	if value, err := gitOutput(dir, "config", "--get", "user.name"); err != nil || strings.TrimSpace(string(value)) == "" {
		cfg = append(cfg, "-c", "user.name=Rally")
	}
	if value, err := gitOutput(dir, "config", "--get", "user.email"); err != nil || strings.TrimSpace(string(value)) == "" {
		cfg = append(cfg, "-c", "user.email=rally@localhost")
	}
	return cfg
}

func isGitDirty(dir string) (bool, error) {
	out, err := gitOutput(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}
