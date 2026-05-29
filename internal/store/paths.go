package store

import (
	"fmt"
	"path/filepath"
)

const rallyDirName = ".rally"

func RallyDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName)
}

func StateDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "state")
}

func RelaysDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "relays")
}

func AgentsDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "agents")
}

func CurrentTaskPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "state", "current_task.md")
}

func RunStatePath(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "state", "run-state.json")
}

func ProgressPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "progress.yaml")
}

func ConfigPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "config.toml")
}

func InstructionsPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "instructions.md")
}

func HookAuditPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "state", "hook-audit.jsonl")
}

func RelayLogPath(workspaceDir string, relayID int) string {
	return filepath.Join(workspaceDir, rallyDirName, "relays", fmt.Sprintf("relay-%d.log", relayID))
}
