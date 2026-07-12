// Package roleinstructions resolves repository role-instruction overrides.
// It is audience-neutral so both application workflows and prompt-facing
// compatibility packages can share the same lookup order.
package roleinstructions

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mitchell-wallace/rally/internal/store"
)

// Loader resolves per-role prompt fragments from .rally/agents/.
type Loader struct {
	WorkspaceDir string
}

// Load returns the matching role file contents for assignee, or an empty
// string when no matching file exists. Resolution order, highest priority
// first:
//
//	.rally/agents/user/<role>.md     — user overrides
//	.rally/agents/builtin/<role>.md  — rally-managed defaults
//	.rally/agents/<role>.md          — legacy flat layout
func (l Loader) Load(assignee string) (string, error) {
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return "", nil
	}

	want := strings.ToLower(assignee)
	for _, dir := range []string{
		store.AgentsUserDir(l.WorkspaceDir),
		store.AgentsBuiltinDir(l.WorkspaceDir),
		store.AgentsDir(l.WorkspaceDir),
	} {
		content, found, err := readRoleFromDir(dir, want)
		if err != nil {
			return "", err
		}
		if found {
			return content, nil
		}
	}
	return "", nil
}

func readRoleFromDir(dir, want string) (string, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if strings.ToLower(base) != want {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", false, err
		}
		return string(data), true, nil
	}
	return "", false, nil
}
