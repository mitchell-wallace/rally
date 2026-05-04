package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteTryLog writes captured output to the try's log file.
// It creates parent directories if needed.
func WriteTryLog(path string, data []byte) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir for log: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
