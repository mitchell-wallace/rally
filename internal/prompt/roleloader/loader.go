package roleloader

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Loader resolves per-role prompt fragments from .rally/agents/.
type Loader struct {
	WorkspaceDir string
}

// Load returns the matching role file contents for assignee, or an empty
// string when no matching file exists.
func (l Loader) Load(assignee string) (string, error) {
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return "", nil
	}

	agentsDir := filepath.Join(l.WorkspaceDir, ".rally", "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	want := strings.ToLower(assignee)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if strings.ToLower(base) != want {
			continue
		}

		data, err := os.ReadFile(filepath.Join(agentsDir, name))
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	return "", nil
}
