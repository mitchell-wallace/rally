package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize rally workspace",
	RunE:  runInit,
}

// commitSetupFiles stages exactly the listed paths and commits them with
// the given message. It returns true if a commit was made, false if nothing
// was staged. It tolerates non-git repos and git failures gracefully
// (returns an error rather than panicking) and uses --no-verify to avoid
// tripping repository hooks during tooling setup.
func commitSetupFiles(workspaceDir string, paths []string, message string) (bool, error) {
	_, inGit, err := gitx.GitRepoRoot(workspaceDir)
	if err != nil || !inGit {
		return false, nil // not a git repo — nothing to do
	}

	// Stage only the specific setup paths that exist on disk.
	for _, p := range paths {
		abs := filepath.Join(workspaceDir, p)
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			continue
		}
		if _, err := gitx.GitOutput(workspaceDir, "add", "--", p); err != nil {
			// The path might be gitignored; skip it silently.
			continue
		}
	}

	// Check whether anything is actually staged for the listed paths.
	diffArgs := append([]string{"diff", "--cached", "--name-only", "--"}, paths...)
	cached, err := gitx.GitOutput(workspaceDir, diffArgs...)
	if err != nil {
		return false, fmt.Errorf("check staged files: %w", err)
	}
	if strings.TrimSpace(string(cached)) == "" {
		return false, nil // nothing staged — no-op
	}

	// Commit only the staged setup paths.
	args := append(gitx.GitUserFallbackConfig(workspaceDir), "commit", "--no-verify", "-m", message, "--")
	args = append(args, paths...)
	if out, err := gitx.GitOutput(workspaceDir, args...); err != nil {
		// "nothing to commit" is benign — treat as no-op.
		if strings.Contains(string(out), "nothing to commit") || strings.Contains(string(out), "no changes added") {
			return false, nil
		}
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}

func runInit(cmd *cobra.Command, args []string) error {
	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}

	// 1. Run git init if not already a git repo
	if _, err := os.Stat(filepath.Join(workspaceDir, ".git")); os.IsNotExist(err) {
		if err := exec.Command("git", "init").Run(); err != nil {
			return fmt.Errorf("git init failed: %w", err)
		}
		fmt.Println("Initialized empty Git repository")
	}

	// 2. Create .rally/ directory
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		return err
	}
	if err := store.MigrateRallyStateLayout(workspaceDir); err != nil {
		return err
	}

	// 3. Create or update .rally/.gitignore
	gitignorePath := filepath.Join(rallyDir, ".gitignore")
	data, err := os.ReadFile(gitignorePath)
	if os.IsNotExist(err) {
		content := "state/\n"
		if err := os.WriteFile(gitignorePath, []byte(content), 0o644); err != nil {
			return err
		}
	} else if err == nil {
		lines := strings.Split(string(data), "\n")
		hasState := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "state" || trimmed == "state/" || trimmed == "/state" || trimmed == "/state/" {
				hasState = true
				break
			}
		}
		if !hasState {
			content := string(data)
			if len(content) > 0 && !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			content += "state/\n"
			if err := os.WriteFile(gitignorePath, []byte(content), 0o644); err != nil {
				return err
			}
		}
	} else {
		return err
	}

	// 4. Create .rally/config.toml
	configPath := filepath.Join(rallyDir, "config.toml")
	// Auto-migrate legacy rally.toml to .rally/config.toml (will be removed in a future version)
	legacyPath := filepath.Join(workspaceDir, "rally.toml")
	if _, err := os.Stat(legacyPath); err == nil {
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			data, err := os.ReadFile(legacyPath)
			if err == nil {
				if err := os.WriteFile(configPath, data, 0o644); err == nil {
					fmt.Println("Migrated legacy rally.toml to .rally/config.toml (auto-migration will be removed in a future version)")
				}
			}
		}
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := os.WriteFile(configPath, []byte(repoConfigTemplate), 0o644); err != nil {
			return err
		}
	}

	// Ensure the user-level config (the base / main source of truth) exists.
	// Created only when absent — never overwrite a user's existing config.
	if userPath := store.UserConfigPath(); userPath != "" {
		if _, err := os.Stat(userPath); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(userPath), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(userPath, []byte(userConfigSeed), 0o644); err != nil {
				return err
			}
			fmt.Printf("Created user-level config at %s\n", userPath)
		}
	}

	// 5. Create .rally/README.md
	readmePath := filepath.Join(rallyDir, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		content := `# Rally Data Directory

This directory contains rally's workspace configuration and local runtime data.

## Tracked Files
- ` + "`config.toml`" + ` — Repo-level config overrides (base config lives in ` + "`~/.config/rally/config.toml`" + `)
- ` + "`agents/builtin/`" + ` — Rally-managed role instructions (auto-updated by rally; do not edit)
- ` + "`agents/user/`" + ` — Your role instruction overrides (win over ` + "`builtin/`" + `)
- ` + "`README.md`" + ` — This guide
- ` + "`summary.jsonl`" + ` — Append-only run summary digest, when enabled by the current workflow

## Local State

Machine-managed runtime records live under ` + "`.rally/state/`" + `. That directory is gitignored and not shared through repository history.

- ` + "`state/tries.jsonl`" + ` — One line per agent execution attempt
- ` + "`state/messages.jsonl`" + ` — Inbox messages for agents
- ` + "`state/relays.jsonl`" + ` — Relay session records
- ` + "`state/agent_status.jsonl`" + ` — Agent pause/freeze state history
- ` + "`state/hook-audit.jsonl`" + ` — Laps hook audit trail
- ` + "`state/run-state.json`" + ` — Current run handoff and lap recording state
- ` + "`state/current_task.md`" + ` — Most recent assembled prompt

## Quick Reference for Agents
- View recent tries (last 10): ` + "`tail -10 .rally/state/tries.jsonl | jq .`" + `
- View pending messages: ` + "`cat .rally/state/messages.jsonl | jq 'select(.status==\\\"pending\\\")'`" + `
- View current relay status: ` + "`tail -1 .rally/state/relays.jsonl | jq .`" + `
- Counts: ` + "`wc -l .rally/state/*.jsonl`" + `
`
		if err := os.WriteFile(readmePath, []byte(content), 0o644); err != nil {
			return err
		}
	}

	// Auto-commit the setup files so they don't clutter git status.
	initPaths := []string{
		".rally/.gitignore",
		".rally/config.toml",
		".rally/README.md",
	}
	if committed, err := commitSetupFiles(workspaceDir, initPaths, "rally: initialize workspace"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: auto-commit setup files: %v\n", err)
	} else if committed {
		fmt.Println("Committed workspace setup.")
	}

	fmt.Println("Rally workspace initialized.")
	// Only nudge toward role setup if it hasn't already been bootstrapped, and
	// skip the tip when the caller is `rally init roles` (which calls runInit
	// directly and would otherwise show a redundant tip before the role
	// bootstrap runs).
	agentsDir := filepath.Join(rallyDir, "agents")
	if _, err := os.Stat(agentsDir); os.IsNotExist(err) && cmd.Name() != "roles" {
		fmt.Println("Tip: run `rally init roles` to set up role-based routing (recommended).")
	}
	return nil
}
