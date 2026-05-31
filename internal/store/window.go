package store

import (
	"fmt"
	"os"
)

// truncateInPlace truncates the file at path to the last windowSize lines.
func truncateInPlace(path string, windowSize int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	lines := splitLines(data)
	if len(lines) <= windowSize {
		return nil
	}
	lines = lines[len(lines)-windowSize:]

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

	return nil
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
