package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/spf13/cobra"
)

var initRolesCmd = &cobra.Command{
	Use:     "roles",
	Aliases: []string{"init-roles"},
	Short:   "Install default role routing and role instructions",
	RunE:    runInitRoles,
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

You are a reliable implementation runner. Your laps should already be scoped to work that can be completed without major product or architecture judgment calls, and your job is to deliver that work carefully.

- Follow the existing architecture, naming, style, and any task-specific instructions.
- Make high-quality, maintainable changes within the assigned scope.
- Prefer focused tests that exercise real behavior. Avoid over-mocking internals when a small integration or package-level test would give better confidence.
- If the task fundamentally needs an unforeseen abstraction or broader design choice, use the handoff flow instead of inventing it in place.
- If a bug fix is becoming messy, use the handoff flow with notes on what you tried, what failed, what you suspect, what you found about current state, and any test assertions you changed.
`,
	},
	{
		Name:  "senior",
		Route: []string{"claude"},
		Instructions: `# Senior Role

You are responsible for higher-judgment implementation, architecture-sensitive work, and tricky debugging.

- Preserve the task's core functional intent, even if the original plan is too rigid or mismatches constraints found in the code.
- Introduce or adjust abstractions cautiously when the task genuinely requires it, and fit them to the existing system.
- Consider downstream laps before changing contracts, data shape, or execution flow.
- You may cautiously update .laps/laps.json when plan adjustments would affect downstream work.
- Add or adjust tests at the right level for the risk, especially around regressions and integration boundaries.
`,
	},
	{
		Name:  "ui",
		Route: []string{"gemini"},
		Instructions: `# UI Role

Your role is to make the interface look and feel good, not merely to make it functional.

- Follow existing product patterns when they are present; for new choices, be moderately bold without becoming noisy.
- Use clear hierarchy, appropriate spacing, readable typography, tasteful color, and purposeful contrast.
- Keep UI focused. Do not add unnecessary buttons, explanatory text, decorative elements, or feature callouts baked into the product surface.
- Make interactions feel complete: useful states, sensible affordances, and polished responsive behavior.
- Verify the rendered experience when a dev server or browser check is available.
`,
	},
	{
		Name:  "verify",
		Route: []string{"codex"},
		Instructions: `# Verify Role

Your role is to build confidence in recent work and catch issues before they compound.

- Read any supplied planning documents and relevant task context.
- Inspect recent git commits and diffs to understand what changed and why.
- Look for code quality issues, behavioral regressions, missing edge cases, and test gaps, especially integration test gaps.
- Apply small fixes directly when they are clearly correct and only a few lines.
- Add new laps at the head for substantial fixes, unclear follow-up, or work that deserves its own implementation pass.
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
	if cfg.Defaults.AntigravityModel == "" && cfg.AntigravityModel == "" {
		cfg.Defaults.AntigravityModel = agent.DefaultAntigravityModel
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
	initCmd.AddCommand(initRolesCmd)
	// Keep `rally init-roles` working as a top-level alias so existing scripts
	// don't break.
	rootCmd.AddCommand(&cobra.Command{
		Use:    "init-roles",
		Short:  "Alias for `rally init roles`",
		Hidden: true,
		RunE:   runInitRoles,
	})
}
