// Package agent_prompt holds rally's agent-facing prompt content as embedded
// .md files compiled into the binary via go:embed.
//
// The package name reflects who is being prompted: agent_prompt holds prompts
// fed to the *agent*, as opposed to user_prompt which holds prompts authored
// for the *user*.
//
// Content is organised into two embedded subfolders:
//
//   - general/ — shared snippets applicable to every role (e.g. finalize.md,
//     headless.md). These are always included when an agent prompt is composed.
//   - roles/   — per-role snippets (e.g. junior.md, senior.md). These hold only
//     role-specific guidance and rely on general/ for the shared finalize and
//     headless instructions, so they do not repeat them.
//
// Embedded role snippets are the default role instructions; an on-disk
// .rally/agents/<role>.md file may override a single role slot. Prompt
// composition (combining general snippets + role snippet + task context) is
// layered on top of these loaders elsewhere.
package agent_prompt

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

//go:embed general/*.md roles/*.md
var sources embed.FS

// General snippet names that are guaranteed to be embedded. They are exported
// so callers (prompt composition, `rally routes check`) can reference the
// shared snippets by a stable name rather than a literal string.
const (
	GeneralFinalize     = "finalize"
	GeneralHandoffOnly  = "handoff_only"
	GeneralHeadless     = "headless"
	GeneralLeftoverWork = "leftover_work"
)

// General returns the embedded general/<name>.md snippet content and whether
// it exists. The name is the file's base name without the .md extension
// (e.g. "finalize", "headless").
func General(name string) (string, bool) {
	return read("general/" + name + ".md")
}

// Finalize returns the shared finalize guidance (commit + laps done/handoff +
// laps wrapup). It is always embedded.
func Finalize() string {
	s, _ := General(GeneralFinalize)
	return s
}

// HandoffOnly returns the bounded handoff-only continuation guidance. It is
// always embedded.
func HandoffOnly() string {
	s, _ := General(GeneralHandoffOnly)
	return s
}

// Headless returns the shared headless / non-interactive guidance. It is
// always embedded.
func Headless() string {
	s, _ := General(GeneralHeadless)
	return s
}

// LeftoverWork returns the advisory guidance shown when the working tree has
// uncommitted non-rally changes at run start. It is always embedded.
func LeftoverWork() string {
	s, _ := General(GeneralLeftoverWork)
	return s
}

// Generals returns the names of all embedded general snippets, sorted.
func Generals() []string {
	return names("general")
}

// Role returns the embedded default prompt snippet for the given role and
// whether one exists. The role is matched case-insensitively against the
// roles/<role>.md base name, so "SENIOR" and "senior" resolve to the same
// embedded default.
func Role(role string) (string, bool) {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return "", false
	}
	return read("roles/" + role + ".md")
}

// Roles returns the names of all embedded role snippets, sorted.
func Roles() []string {
	return names("roles")
}

// read returns the embedded file content with surrounding whitespace trimmed,
// and whether the file exists.
func read(path string) (string, bool) {
	data, err := sources.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// names lists the .md base names under the given embedded subfolder, sorted.
func names(dir string) []string {
	entries, err := fs.ReadDir(sources, dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".md"))
	}
	sort.Strings(out)
	return out
}
