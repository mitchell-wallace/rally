package laps

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Detect returns true if both conditions are met:
//   1. .laps/laps.json is discoverable from workspaceDir
//   2. the "laps" binary is available on PATH
func Detect(workspaceDir string) bool {
	lapsJSON := filepath.Join(workspaceDir, ".laps", "laps.json")
	if _, err := os.Stat(lapsJSON); os.IsNotExist(err) {
		return false
	}
	_, err := exec.LookPath("laps")
	return err == nil
}
