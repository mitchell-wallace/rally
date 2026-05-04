package laps

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed laps-done-hook.sh
var doneHookScript string

//go:embed laps-handoff-hook.sh
var handoffHookScript string

//go:embed laps-wrapup-hook.sh
var wrapupHookScript string

// Hook matches the laps hooks.json entry format.
type Hook struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Command     string `json:"command"`
	When        string `json:"when"`
	Run         string `json:"run"`
	Passback    bool   `json:"passback"`
}

// HooksFile is the top-level hooks envelope.
type HooksFile struct {
	Version int    `json:"version"`
	Hooks   []Hook `json:"hooks"`
}

const rallyHookPrefix = "rally:"

var rallyHooks = []struct {
	filename    string
	script      string
	title       string
	description string
	command     string
	when        string
}{
	{
		filename:    "laps-done-hook.sh",
		script:      doneHookScript,
		title:       rallyHookPrefix + "laps-done",
		description: "Records lap completion and prompts for wrapup",
		command:     "done",
		when:        "after",
	},
	{
		filename:    "laps-handoff-hook.sh",
		script:      handoffHookScript,
		title:       rallyHookPrefix + "laps-handoff",
		description: "Signals handoff state and prompts for wrapup",
		command:     "handoff",
		when:        "before",
	},
	{
		filename:    "laps-wrapup-hook.sh",
		script:      wrapupHookScript,
		title:       rallyHookPrefix + "laps-wrapup",
		description: "Completes or hands off the current run",
		command:     "wrapup",
		when:        "before",
	},
}

// InstallHooks writes rally hook scripts into .laps/hooks/rally/ and
// ensures .laps/hooks.json contains the three rally-keyed entries.
// It preserves any existing non-rally entries.
// Returns true if any changes were made.
func InstallHooks(lapsDir string) (bool, error) {
	hooksPath := filepath.Join(lapsDir, "hooks.json")

	hf := &HooksFile{Version: 1}
	if data, err := os.ReadFile(hooksPath); err == nil {
		if err := json.Unmarshal(data, hf); err != nil {
			return false, fmt.Errorf("parse hooks.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("read hooks.json: %w", err)
	}

	// Separate existing rally hooks from user hooks.
	var userHooks []Hook
	for _, h := range hf.Hooks {
		if !strings.HasPrefix(h.Title, rallyHookPrefix) {
			userHooks = append(userHooks, h)
		}
	}

	// Ensure scripts directory exists.
	scriptsDir := filepath.Join(lapsDir, "hooks", "rally")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		return false, fmt.Errorf("create scripts dir: %w", err)
	}

	// Write scripts.
	changed := false
	for _, rh := range rallyHooks {
		path := filepath.Join(scriptsDir, rh.filename)
		existing, _ := os.ReadFile(path)
		if string(existing) != rh.script {
			if err := os.WriteFile(path, []byte(rh.script), 0o755); err != nil {
				return false, fmt.Errorf("write %s: %w", rh.filename, err)
			}
			changed = true
		}
	}

	// Build desired rally hook entries.
	var desiredRallyHooks []Hook
	for _, rh := range rallyHooks {
		run := filepath.Join(".laps", "hooks", "rally", rh.filename)
		// Forward laps variables so the scripts receive them naturally.
		switch rh.command {
		case "done":
			// after-hook: $id is the closed lap ID
			run += ` "$id"`
		case "handoff", "wrapup":
			// hook-only commands: forward all agent args
			run += ` $args`
		}
		desiredRallyHooks = append(desiredRallyHooks, Hook{
			Title:       rh.title,
			Description: rh.description,
			Command:     rh.command,
			When:        rh.when,
			Run:         run,
			Passback:    true,
		})
	}

	// Determine if hooks.json needs updating.
	if !hooksMatch(hf.Hooks, append(userHooks, desiredRallyHooks...)) {
		changed = true
	}

	if !changed {
		return false, nil
	}

	hf.Hooks = append(userHooks, desiredRallyHooks...)
	data, err := json.MarshalIndent(hf, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal hooks.json: %w", err)
	}
	if err := os.WriteFile(hooksPath, data, 0o644); err != nil {
		return false, fmt.Errorf("write hooks.json: %w", err)
	}

	return true, nil
}

// hooksMatch reports whether two hook slices are semantically identical
// for the purposes of determining whether an update is needed.
func hooksMatch(a, b []Hook) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !hookEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func hookEqual(a, b Hook) bool {
	return a.Title == b.Title &&
		a.Description == b.Description &&
		a.Command == b.Command &&
		a.When == b.When &&
		a.Run == b.Run &&
		a.Passback == b.Passback
}
