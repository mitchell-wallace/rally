package cli

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

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize rally workspace",
		RunE:  runInit,
	}
	cmd.AddCommand(newInitRolesCmd())
	cmd.AddCommand(newInitAllCmd())
	return cmd
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
		if err := os.WriteFile(readmePath, []byte(rallyReadmeBody), 0o644); err != nil {
			return err
		}
	}

	// Auto-commit the setup files so they don't clutter git status.
	initPaths := []string{
		".rally/.gitignore",
		".rally/config.toml",
		".rally/README.md",
	}
	if committed, err := gitx.CommitSetupFiles(workspaceDir, initPaths, "rally: initialize workspace"); err != nil {
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
