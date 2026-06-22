package store

import (
	"os"
	"path/filepath"
)

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

// AgentsBuiltinDir holds rally-managed role instruction files. Rally regenerates
// these from the binary's embedded role defaults on each run, so they always
// reflect the installed rally version.
func AgentsBuiltinDir(workspaceDir string) string {
	return filepath.Join(AgentsDir(workspaceDir), "builtin")
}

// AgentsUserDir holds user-authored role instruction overrides. A user/<role>.md
// file wins over the matching builtin/<role>.md and is never touched by rally.
func AgentsUserDir(workspaceDir string) string {
	return filepath.Join(AgentsDir(workspaceDir), "user")
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

// UserConfigPath returns the path to the user-level rally config, which is the
// base layer for every repo: ~/.config/rally/config.toml (honouring
// XDG_CONFIG_HOME). Repo-level config.toml values override it per key. Returns
// an empty string only when the home directory cannot be resolved.
func UserConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "rally", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "rally", "config.toml")
}

func InstructionsPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, rallyDirName, "instructions.md")
}

func HookAuditPath(workspaceDir string) string {
	return filepath.Join(StateDir(workspaceDir), "hook-audit.jsonl")
}
