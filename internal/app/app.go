package app

import "path/filepath"

const (
	BinaryName          = "rally"
	ContainerDataRoot   = "/persist/agent/rally"
	DefaultRepoProgress = "docs/orchestration/rally-progress.yaml"
	EnvContainerName    = "RALLY_CONTAINER_NAME"
	EnvDataDir          = "RALLY_DATA_DIR"
	EnvRepoProgressPath = "RALLY_REPO_PROGRESS_PATH"
	EnvSessionID        = "RALLY_SESSION_ID"
	EnvBatchID          = "RALLY_BATCH_ID"
	EnvIterationIndex   = "RALLY_ITERATION_INDEX"
	EnvAgent            = "RALLY_AGENT"
	EnvSessionDir       = "RALLY_SESSION_DIR"
	EnvWorkspaceDir     = "RALLY_WORKSPACE_DIR"
	EnvBeads            = "RALLY_BEADS"
	EnvNoUpdateCheck    = "RALLY_NO_UPDATE_CHECK"
	SchemaVersion       = 1
	RepoHistoryWindow   = 50
	ReleaseOwner        = "mitchell-wallace"
	ReleaseRepo         = "rally"
)

func ContainerDataDir(containerName string) string {
	return filepath.Join(ContainerDataRoot, containerName)
}

func RepoProgressPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, DefaultRepoProgress)
}

func ContainerEnv(containerName string) map[string]string {
	dataDir := ContainerDataDir(containerName)
	return map[string]string{
		EnvContainerName:    containerName,
		EnvDataDir:          dataDir,
		EnvRepoProgressPath: RepoProgressPath("/workspace"),
		EnvWorkspaceDir:     "/workspace",
	}
}

func SessionDir(dataDir string, sessionID int) string {
	return filepath.Join(dataDir, "sessions", "session-"+itoa(sessionID))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + (n % 10))
		n /= 10
	}
	return sign + string(digits[i:])
}
