package store

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// commitThenTruncate archives the current file via git, truncates to the last
// windowSize records, and commits the truncated file.
func commitThenTruncate(path string, windowSize int) error {
	filename := filepath.Base(path)
	inGit := isGitRepo(path)

	// 1. Commit current file (git only)
	if inGit {
		if err := gitAddCommit(path, fmt.Sprintf("rally: archive %s", filename)); err != nil {
			return fmt.Errorf("archive commit: %w", err)
		}
	}

	// 2. Read file and keep last windowSize records
	// We use a line-based approach since JSONL records are one per line.
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	lines := splitLines(data)
	if len(lines) > windowSize {
		lines = lines[len(lines)-windowSize:]
	}

	// 3. Rewrite file with kept records
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmpPath, err)
	}

	var writeErr error
	for i, line := range lines {
		if i > 0 {
			if _, err := f.WriteString("\n"); err != nil {
				writeErr = fmt.Errorf("write newline: %w", err)
				break
			}
		}
		if _, err := f.WriteString(line); err != nil {
			writeErr = fmt.Errorf("write line: %w", err)
			break
		}
	}
	if len(lines) > 0 && writeErr == nil {
		if _, err := f.WriteString("\n"); err != nil {
			writeErr = fmt.Errorf("write trailing newline: %w", err)
		}
	}

	if err := f.Close(); err != nil && writeErr == nil {
		writeErr = fmt.Errorf("close %s: %w", tmpPath, err)
	}

	if writeErr != nil {
		os.Remove(tmpPath)
		return writeErr
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename %s to %s: %w", tmpPath, path, err)
	}

	// 4. Commit truncated file (git only)
	if inGit {
		if err := gitAddCommit(path, fmt.Sprintf("rally: truncate %s", filename)); err != nil {
			return fmt.Errorf("truncate commit: %w", err)
		}
	}

	return nil
}

func isGitRepo(path string) bool {
	dir := filepath.Dir(path)
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return string(out) == "true\n"
}

func gitAddCommit(path, msg string) error {
	dir := filepath.Dir(path)
	name := filepath.Base(path)
	add := exec.Command("git", "-C", dir, "add", name)
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %s: %w", out, err)
	}
	commit := exec.Command("git", "-C", dir, "commit", "-m", msg)
	if out, err := commit.CombinedOutput(); err != nil {
		// If nothing to commit, that's okay
		if isNothingToCommit(string(out)) {
			return nil
		}
		return fmt.Errorf("git commit: %s: %w", out, err)
	}
	return nil
}

func isNothingToCommit(out string) bool {
	// Common git "nothing to commit" messages
	return contains(out, "nothing to commit") ||
		contains(out, "no changes added to commit") ||
		contains(out, "working tree clean")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			lines = append(lines, string(data[start:i]))
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, string(data[start:]))
	}
	return lines
}

// commitThenTruncateWithContent archives the current file via git, writes the
// exact provided records to the file, and commits the result.
func commitThenTruncateWithContent[T any](path string, records []T) error {
	filename := filepath.Base(path)
	inGit := isGitRepo(path)

	// 1. Commit current file (git only)
	if inGit {
		if err := gitAddCommit(path, fmt.Sprintf("rally: archive %s", filename)); err != nil {
			return fmt.Errorf("archive commit: %w", err)
		}
	}

	// 2. Rewrite file with exact kept records
	if err := rewriteJSONL(path, records); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	// 3. Commit truncated file (git only)
	if inGit {
		if err := gitAddCommit(path, fmt.Sprintf("rally: truncate %s", filename)); err != nil {
			return fmt.Errorf("truncate commit: %w", err)
		}
	}

	return nil
}
