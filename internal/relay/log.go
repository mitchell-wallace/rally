package relay

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9]+`)

// repoKey returns a short, filesystem-safe identifier for a workspace directory.
// Format: sanitizedBasename-XXXXXXXX where XXXXXXXX is 8 hex chars of SHA-256(path).
// This scopes data-dir log files per repo so multiple checkouts don't collide.
func repoKey(workspaceDir string) string {
	base := strings.ToLower(filepath.Base(workspaceDir))
	base = nonAlphanumRe.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "repo"
	}
	h := sha256.Sum256([]byte(workspaceDir))
	return fmt.Sprintf("%s-%x", base, h[:4])
}

func openRelayLog(dataDir, workspaceDir string, relayID int) (io.WriteCloser, error) {
	paths := []string{
		relayLogPath(dataDir, workspaceDir, relayID),
		repoRelayLogPath(workspaceDir, relayID),
	}
	var files []*os.File
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			for _, opened := range files {
				_ = opened.Close()
			}
			return nil, err
		}
		files = append(files, f)
	}
	writers := make([]io.Writer, 0, len(files))
	for _, f := range files {
		writers = append(writers, f)
	}
	return &multiWriteCloser{files: files, writer: io.MultiWriter(writers...)}, nil
}

func relayLogPath(dataDir, workspaceDir string, relayID int) string {
	return filepath.Join(dataDir, "relays", repoKey(workspaceDir), fmt.Sprintf("relay-%d.log", relayID))
}

func repoRelayLogPath(workspaceDir string, relayID int) string {
	return filepath.Join(workspaceDir, ".rally", "relays", fmt.Sprintf("relay-%d.log", relayID))
}

func PruneRepoRelayLogs(workspaceDir string, keep int) error {
	dir := filepath.Join(workspaceDir, ".rally", "relays")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var logs []os.DirEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "relay-") || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		logs = append(logs, entry)
	}
	if len(logs) <= keep {
		return nil
	}
	sort.Slice(logs, func(i, j int) bool {
		return relayLogID(logs[i].Name()) < relayLogID(logs[j].Name())
	})
	for _, entry := range logs[:len(logs)-keep] {
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func relayLogID(name string) int {
	value := strings.TrimSuffix(strings.TrimPrefix(name, "relay-"), ".log")
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return n
}

type multiWriteCloser struct {
	files  []*os.File
	writer io.Writer
}

func (m *multiWriteCloser) Write(p []byte) (int, error) {
	return m.writer.Write(p)
}

func (m *multiWriteCloser) Close() error {
	var closeErr error
	for _, f := range m.files {
		if err := f.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}
