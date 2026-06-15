package relay

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mitchell-wallace/rally/internal/gitx"
)

var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9]+`)

// repoKey returns a short, filesystem-safe identifier for a workspace directory.
// Format: <basenamePrefix>-<hashPrefix> where basenamePrefix is the first 8
// chars of the sanitised lower-case folder name and hashPrefix is 4 hex chars
// of SHA-256(absoluteWorkspacePath). The path is canonicalised before hashing
// so the same checkout always lands in the same data-dir bucket, and two
// distinct checkouts under a shared data dir never collide.
func repoKey(workspaceDir string) string {
	base := strings.ToLower(filepath.Base(workspaceDir))
	base = nonAlphanumRe.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "repo"
	}
	if len(base) > 8 {
		base = base[:8]
	}

	canonical := workspaceDir
	if abs, err := filepath.Abs(workspaceDir); err == nil {
		canonical = abs
	}
	h := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("%s-%x", base, h[:2])
}

// repoDisplayName returns a stable human-readable repository name for
// telemetry. Prefer the Git remote slug so a local dev checkout like
// "rally-2" still reports "rally"; fall back to the checkout directory.
func repoDisplayName(workspaceDir string) string {
	if repoRoot, ok, err := gitx.GitRepoRoot(workspaceDir); err == nil && ok {
		if out, err := gitx.GitOutput(repoRoot, "remote", "get-url", "origin"); err == nil {
			if name := repoNameFromRemote(strings.TrimSpace(string(out))); name != "" {
				return name
			}
		}
	}
	return fallbackRepoDisplayName(workspaceDir)
}

func fallbackRepoDisplayName(workspaceDir string) string {
	name := strings.TrimSpace(filepath.Base(workspaceDir))
	name = strings.TrimSuffix(name, ".git")
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "repo"
	}
	return name
}

func repoNameFromRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimRight(remote, "/")
	remote = strings.TrimSuffix(remote, ".git")
	if remote == "" {
		return ""
	}
	remote = strings.ReplaceAll(remote, ":", "/")
	name := strings.TrimSpace(filepath.Base(remote))
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	return name
}

func openRelayLog(dataDir, workspaceDir string, relayID int) (io.WriteCloser, error) {
	path := relayLogPath(dataDir, workspaceDir, relayID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

func relayLogPath(dataDir, workspaceDir string, relayID int) string {
	return filepath.Join(dataDir, "relays", repoKey(workspaceDir), fmt.Sprintf("relay-%d.log", relayID))
}
