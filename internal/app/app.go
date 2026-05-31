package app

import (
	"path/filepath"

	"github.com/mitchell-wallace/rally/internal/store"
)

const (
	BinaryName          = "rally"
	ContainerDataRoot   = "/persist/agent/rally"
	DefaultRepoProgress = ".rally/summary.jsonl"
	EnvContainerName    = "RALLY_CONTAINER_NAME"
	EnvDataDir          = "RALLY_DATA_DIR"
	EnvRepoProgressPath = "RALLY_REPO_PROGRESS_PATH"
	EnvSessionID        = "RALLY_SESSION_ID"
	EnvBatchID          = "RALLY_BATCH_ID"
	EnvIterationIndex   = "RALLY_ITERATION_INDEX"
	EnvAgent            = "RALLY_AGENT"
	EnvSessionDir       = "RALLY_SESSION_DIR"
	EnvWorkspaceDir     = "RALLY_WORKSPACE_DIR"
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
	// TODO: Rename RepoProgressPath and RALLY_REPO_PROGRESS_PATH after the
	// progress.yaml compatibility terminology is no longer needed.
	return store.ProgressPath(workspaceDir)
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
