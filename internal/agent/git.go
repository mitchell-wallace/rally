package agent

import (
	"fmt"
	"os/exec"
	"strings"
)

func gitRepoRoot(dir string) (string, bool, error) {
	out, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false, nil
	}
	return strings.TrimSpace(string(out)), true, nil
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.CombinedOutput()
}

func gitCommandError(args []string, output []byte, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("git %s failed: %w\noutput: %s", strings.Join(args, " "), err, string(output))
}

func gitUserFallbackConfig(dir string) []string {
	nameOut, _ := gitOutput(dir, "config", "user.name")
	emailOut, _ := gitOutput(dir, "config", "user.email")
	name := strings.TrimSpace(string(nameOut))
	email := strings.TrimSpace(string(emailOut))
	if name == "" || email == "" {
		return []string{"-c", "user.name=Rally", "-c", "user.email=rally@localhost"}
	}
	return nil
}
