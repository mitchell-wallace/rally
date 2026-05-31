package store

import "path/filepath"

const rallyDirName = ".rally"

func RallyDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName)
}

func StateDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "state")
}

func AgentsDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "agents")
}

func CurrentTaskPath(workspaceDir string) string {
	return filepath.Join(StateDir(workspaceDir), "current_task.md")
}

func RunStatePath(workspaceDir string) string {
	return filepath.Join(StateDir(workspaceDir), "run-state.json")
}

func SummaryPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "summary.jsonl")
}

func ProgressPath(workspaceDir string) string {
	return SummaryPath(workspaceDir)
}

func ConfigPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "config.toml")
}

func InstructionsPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "instructions.md")
}

func HookAuditPath(workspaceDir string) string {
	return filepath.Join(StateDir(workspaceDir), "hook-audit.jsonl")
}
