package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/spf13/cobra"
)

var initRolesCmd = &cobra.Command{
	Use:   "init-roles",
	Short: "Install default role routing and role instructions",
	RunE:  runInitRoles,
}

type roleBootstrap struct {
	Name         string
	Route        []string
	Instructions string
}

var defaultRoleBootstraps = []roleBootstrap{
	{
		Name:  "junior",
		Route: []string{"opencode"},
		Instructions: `# Junior Role

Focus on small, well-scoped implementation work.

- Prefer simple changes that match the surrounding code.
- Ask for or create a follow-up lap when the work becomes ambiguous or broad.
- Keep tests close to the changed behavior.
- Leave design or architectural tradeoffs for a senior role when they matter.
`,
	},
	{
		Name:  "senior",
		Route: []string{"claude"},
		Instructions: `# Senior Role

Focus on architecture, tricky debugging, and high-judgment implementation.

- Identify the smallest durable design that fits the existing system.
- Preserve user changes and avoid unrelated refactors.
- Add regression coverage for behavior that could break again.
- Leave clear handoff notes when more work should follow.
`,
	},
	{
		Name:  "ui",
		Route: []string{"gemini"},
		Instructions: `# UI Role

Focus on user-facing flows, interface polish, and frontend behavior.

- Match the product's existing visual language and interaction patterns.
- Check responsive states and text fit on narrow and wide screens.
- Prefer complete, usable controls over placeholder screens.
- Verify the rendered experience when a dev server or browser check is available.
`,
	},
	{
		Name:  "verify",
		Route: []string{"codex"},
		Instructions: `# Verify Role

Focus on review, validation, and confidence-building checks.

- Prioritize bugs, regressions, missing tests, and risky assumptions.
- Run the most relevant tests or static checks available.
- Report concrete findings with file and line references where possible.
- Avoid broad rewrites unless verification exposes a necessary fix.
`,
	},
}

func runInitRoles(cmd *cobra.Command, args []string) error {
	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}

	if err := runInit(cmd, args); err != nil {
		return err
	}

	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	changedConfig := false
	if cfg.Defaults.OpenCodeModel == "" && cfg.OpenCodeModel == "" {
		cfg.Defaults.OpenCodeModel = "opencode-go/kimi-k2.6"
		changedConfig = true
	}
	if cfg.Defaults.ClaudeModel == "" && cfg.ClaudeModel == "" {
		cfg.Defaults.ClaudeModel = "claude-opus-4-7"
		changedConfig = true
	}
	if cfg.Defaults.GeminiModel == "" && cfg.GeminiModel == "" {
		cfg.Defaults.GeminiModel = "gemini-3.1-pro-preview"
		changedConfig = true
	}
	if cfg.Defaults.CodexModel == "" && cfg.CodexModel == "" {
		cfg.Defaults.CodexModel = "gpt-5.5"
		changedConfig = true
	}

	if cfg.Routes == nil {
		cfg.Routes = make(map[string][]string)
	}
	if _, ok := cfg.Routes["default"]; !ok {
		cfg.Routes["default"] = []string{"opencode"}
		changedConfig = true
	}
	for _, role := range defaultRoleBootstraps {
		if _, ok := cfg.Routes[role.Name]; ok {
			continue
		}
		cfg.Routes[role.Name] = append([]string(nil), role.Route...)
		changedConfig = true
	}

	if changedConfig {
		if err := config.SaveV2(workspaceDir, cfg); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
		fmt.Println("Updated .rally/config.toml with default role routing.")
	} else {
		fmt.Println(".rally/config.toml already has role routing defaults.")
	}

	agentsDir := filepath.Join(workspaceDir, ".rally", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return err
	}

	created := 0
	for _, role := range defaultRoleBootstraps {
		path := filepath.Join(agentsDir, role.Name+".md")
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.WriteFile(path, []byte(role.Instructions), 0o644); err != nil {
			return err
		}
		created++
	}

	if created > 0 {
		fmt.Printf("Created %d role instruction files in .rally/agents/.\n", created)
	} else {
		fmt.Println(".rally/agents role instruction files already exist.")
	}

	return nil
}

func init() {
	rootCmd.AddCommand(initRolesCmd)
}
