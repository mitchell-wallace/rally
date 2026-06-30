package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/agent_prompt"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/spf13/cobra"
)

var initRolesCmd = &cobra.Command{
	Use:   "roles",
	Short: "Install default role routing and role instructions",
	RunE:  runInitRoles,
}

var initAllCmd = &cobra.Command{
	Use:   "all",
	Short: "Initialize workspace and install roles",
	RunE:  runInitAll,
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
		Route: []string{"ag"},
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
		Instructions: "# Verify Role\n" +
			"\n" +
			"Your role is to build confidence in recent work and catch issues before they compound.\n" +
			"\n" +
			"- Read any supplied planning documents and relevant task context.\n" +
			"- Inspect recent git commits and diffs to understand what changed and why.\n" +
			"- Identify the intended target/base branch before diffing. Do not assume `main`; use PR metadata, repo docs, branch config, the user's instructions, or git history to choose the comparison target.\n" +
			"- Treat work committed before the first lap/try in the current relay batch as pre-existing baseline unless the user explicitly asks to review or remove it.\n" +
			"- Look for code quality issues, behavioral regressions, missing edge cases, and test gaps, especially integration test gaps.\n" +
			"- Apply small fixes directly when they are clearly correct and only a few lines.\n" +
			"- Add new laps at the head for substantial fixes, unclear follow-up, or work that deserves its own implementation pass.\n" +
			"- Do not rewrite git history during verification or cleanup. Avoid reset/rebase/squash/amend-away/force-push strategies unless the user explicitly approves them. Prefer additive commits, revert commits, or a new recovery branch so removed work remains backtrackable.\n",
	},
	{
		Name:  "recovery",
		Route: []string{"claude"},
	},
}

func runInitRoles(cmd *cobra.Command, args []string) error {
	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}
	return runRolesSetup(workspaceDir)
}

func runInitAll(cmd *cobra.Command, args []string) error {
	if err := runInit(cmd, args); err != nil {
		return err
	}
	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}
	return runRolesSetup(workspaceDir)
}

func runRolesSetup(workspaceDir string) error {
	// Role routing + model defaults are written to the user-level config (the
	// base / main source of truth) so they apply across every repo. The repo
	// file stays for per-repo overrides only.
	cfgPath := store.UserConfigPath()
	if cfgPath == "" {
		cfgPath = store.ConfigPath(workspaceDir) // no home dir — fall back to repo
	}
	cfg, err := config.LoadV2File(cfgPath)
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
		if err := config.SaveV2File(cfgPath, cfg); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
		fmt.Printf("Updated %s with default role routing.\n", cfgPath)
	} else {
		fmt.Printf("%s already has role routing defaults.\n", cfgPath)
	}

	if err := syncRoleFolders(workspaceDir); err != nil {
		return err
	}
	fmt.Println("Role instructions are set up under .rally/agents/builtin/ (rally-managed) and .rally/agents/user/ (your overrides).")

	return nil
}

// syncRoleFolders ensures the .rally/agents/builtin (rally-managed) and
// .rally/agents/user (user overrides) folders exist, migrates any legacy flat
// .rally/agents/<role>.md files into them, and regenerates the builtin files
// from the binary's embedded defaults so managed roles auto-update when rally is
// updated. It is safe to call on every relay start and from the init commands.
func syncRoleFolders(workspaceDir string) error {
	builtinDir := store.AgentsBuiltinDir(workspaceDir)
	userDir := store.AgentsUserDir(workspaceDir)
	if err := os.MkdirAll(builtinDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		return err
	}

	movedBuiltin, movedUser, err := migrateLegacyRoleFiles(workspaceDir, builtinDir, userDir)
	if err != nil {
		return err
	}

	// (Re)write builtin/<role>.md from the embedded defaults for every embedded
	// role — the auto-update step. Idempotent when content is unchanged.
	for _, role := range agent_prompt.Roles() {
		body, ok := agent_prompt.Role(role)
		if !ok {
			continue
		}
		content := body + "\n"
		path := filepath.Join(builtinDir, role+".md")
		if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}

	if movedBuiltin > 0 || movedUser > 0 {
		fmt.Printf("Migrated %d role file(s) to the new layout: %d managed -> .rally/agents/builtin/ (auto-updating), %d customized -> .rally/agents/user/ (preserved).\n",
			movedBuiltin+movedUser, movedBuiltin, movedUser)
	}
	return nil
}

// migrateLegacyRoleFiles moves legacy flat .rally/agents/<role>.md files into the
// builtin/ or user/ folders: files whose content rally has shipped go to
// builtin/ (where they will be regenerated to the current version), and
// unrecognized (user-customized) files are preserved in user/. The builtin/ and
// user/ subdirectories themselves are skipped.
func migrateLegacyRoleFiles(workspaceDir, builtinDir, userDir string) (movedBuiltin, movedUser int, err error) {
	agentsDir := store.AgentsDir(workspaceDir)
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue // builtin/ and user/ live here; never recurse into them
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		role := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
		src := filepath.Join(agentsDir, name)
		data, readErr := os.ReadFile(src)
		if readErr != nil {
			return movedBuiltin, movedUser, readErr
		}

		if agent_prompt.IsManagedRoleContent(role, string(data), bootstrapInstructionsFor(role)) {
			// Managed: builtin/ is regenerated below, so just drop the legacy copy.
			if err := os.Remove(src); err != nil {
				return movedBuiltin, movedUser, err
			}
			movedBuiltin++
			continue
		}

		// Customized: preserve in user/ without clobbering an existing override.
		dest := filepath.Join(userDir, name)
		if _, statErr := os.Stat(dest); statErr == nil {
			continue // a user override already exists; leave the legacy file in place
		}
		if err := os.Rename(src, dest); err != nil {
			return movedBuiltin, movedUser, err
		}
		movedUser++
	}
	return movedBuiltin, movedUser, nil
}

// bootstrapInstructionsFor returns the hard-coded bootstrap body for a role, used
// as additional canonical content when classifying legacy role files (the flat
// verify.md predates the embedded default and matches this text).
func bootstrapInstructionsFor(role string) string {
	for _, rb := range defaultRoleBootstraps {
		if strings.EqualFold(rb.Name, role) {
			return rb.Instructions
		}
	}
	return ""
}
