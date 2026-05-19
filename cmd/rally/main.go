package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/cli"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/release"
	"github.com/mitchell-wallace/rally/internal/routing"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/spf13/cobra"
)

var Version = "dev"

func main() {
	flushUpdateNotice := startBackgroundUpdateCheck(os.Args[1:], os.Stderr)
	if err := rootCmd.Execute(); err != nil {
		flushUpdateNotice()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	flushUpdateNotice()
}

var rootCmd = &cobra.Command{
	Use:   "rally",
	Short: "Agent orchestrator",
	Long:  `Rally is a CLI agent orchestrator for managing multi-agent relay sessions.`,
}

func init() {
	// Register the --version flag with a -v short alias before Cobra would
	// auto-register a long-only one (Cobra reuses an existing "version" flag if
	// it finds one already declared).
	rootCmd.Flags().BoolP("version", "v", false, "Print version and exit")
	rootCmd.Version = release.DisplayVersion(Version)
	rootCmd.SetVersionTemplate(app.BinaryName + " {{.Version}}\n")
}

var startCmd = &cobra.Command{
	Use:     "start",
	Aliases: []string{"relay"},
	Short:   "Start or resume agent relays",
	RunE:    runRelay,
}

func resolveWorkspaceDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if root, ok, _ := gitx.GitRepoRoot(wd); ok {
		return root, nil
	}
	return wd, nil
}

func runRelay(cmd *cobra.Command, args []string) error {
	iterations, _ := cmd.Flags().GetInt("iterations")
	agentSpecs, _ := cmd.Flags().GetStringArray("agent")
	mixSpecs, _ := cmd.Flags().GetStringArray("mix")
	resume, _ := cmd.Flags().GetBool("resume")
	newBatch, _ := cmd.Flags().GetBool("new")

	if resume && newBatch {
		return fmt.Errorf("cannot use --resume and --new together")
	}

	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}

	rallyDir := filepath.Join(workspaceDir, ".rally")
	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	for _, note := range cfg.DeprecationNotes {
		fmt.Fprintln(os.Stderr, "warning:", note)
	}

	lapsEnabled := laps.Detect(workspaceDir)

	if !cmd.Flags().Changed("iterations") {
		if cfg.Defaults.Iterations > 0 {
			iterations = cfg.Defaults.Iterations
		} else if !lapsEnabled {
			iterations = 50
			fmt.Fprintln(os.Stderr, "warning: no iteration limit specified; defaulting to 50 iterations")
		} else {
			iterations = 10000 // laps-backed runs until queue empty
		}
	}

	selectedSpecs, usedOverride, warning, err := chooseRelayAgentSpecs(agentSpecs, mixSpecs, cfg.Defaults.Mix)
	if err != nil {
		return err
	}
	if warning != "" {
		fmt.Fprintln(os.Stderr, warning)
	}


	validRoutes, err := cli.ValidateRelayStartupRoutes(context.Background(), workspaceDir, cfg, cli.RelayStartupRouteOptions{
		In:          os.Stdin,
		Out:         os.Stderr,
		LapsEnabled: lapsEnabled,
	})
	if err != nil {
		return err
	}
	cfg.Routes = validRoutes

	dataDir := ""
	if home, err := os.UserHomeDir(); err == nil {
		dataDir = filepath.Join(home, ".local", "share", "rally")
	}
	if cfg.DataDir != "" {
		dataDir = cfg.DataDir
	}

	if _, err := os.Stat(rallyDir); os.IsNotExist(err) {
		return fmt.Errorf("rally not initialized; run `rally init` first")
	}

	s, err := store.NewStore(rallyDir)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}

	executors := map[string]agent.Executor{
		"claude":   &agent.ClaudeExecutor{Model: cfg.ClaudeModel},
		"codex":    &agent.CodexExecutor{Model: cfg.CodexModel},
		"gemini":   &agent.GeminiExecutor{Model: cfg.GeminiModel},
		"opencode": &agent.OpenCodeExecutor{Model: cfg.OpenCodeModel},
	}

	for name, hc := range cfg.Harnesses {
		if len(hc.Command) > 0 {
			executors[name] = &agent.GenericExecutor{
				Command:        hc.Command,
				ModelFlag:      hc.ModelFlag,
				OutputStrategy: hc.OutputStrategy,
				OutputLines:    hc.OutputLines,
				TailStream:     hc.TailStream,
			}
		}
	}

	if lapsEnabled {
		lapsDir := filepath.Join(workspaceDir, ".laps")
		changed, err := laps.InstallHooks(lapsDir)
		if err != nil {
			return fmt.Errorf("install laps hooks: %w", err)
		}
		if changed {
			fmt.Printf("Installed rally hooks in %s\n", filepath.Join(lapsDir, "hooks", "rally"))
		}
	}

		runnerCfg := relay.Config{
			WorkspaceDir:             workspaceDir,
			DataDir:                  dataDir,
			AgentMixSpecs:            selectedSpecs,
			RouteSpecs:               cfg.Routes,
			UseOverrideRoute:         usedOverride,
			TargetIterations:         iterations,
			FreezeThreshold:          cfg.Reliability.FreezeThreshold(),
			LivenessProbe:            cfg.Reliability.LivenessProbe,
			RetryBudget:              cfg.Reliability.RetryBudget,
			RunHooksOnAutoCommit:     cfg.RunHooksOnAutoCommit,
			LapsEnabled:              lapsEnabled,
			LapsInstructionsFile:     cfg.Laps.InstructionsFile,
			FallbackInstructionsFile: cfg.Fallback.InstructionsFile,
		}

	runnerCfg.Resolver = func(spec string) (agent.ResolvedAgent, error) {
		ra, err := cfg.ResolveAgent(spec)
		if err != nil {
			return agent.ResolvedAgent{}, err
		}
		return agent.ResolvedAgent{Harness: ra.Harness, Model: ra.Model}, nil
	}

	if usedOverride {
		if _, err := routing.BuildOverrideRoute("override", selectedSpecs, cfg.Routes, routing.AgentResolver(runnerCfg.Resolver)); err != nil {
			return err
		}
	}

	instructionsPath := filepath.Join(rallyDir, "instructions.md")
	if data, err := os.ReadFile(instructionsPath); err == nil {
		runnerCfg.Instructions = string(data)
	}

	if len(args) > 0 {
		runnerCfg.TaskPrompt = strings.Join(args, " ")
	}

	if newBatch {
		relays := s.RecentRelays(1)
		if len(relays) > 0 && relays[0].EndedAt == "" {
			_ = relay.CompleteRelay(s, relays[0].ID)
		}
	}

	// Resume check: if --resume not passed and incomplete relay exists, prompt
	resumedRelay := false
	var storedMix string
	if !resume && !newBatch {
		relays := s.RecentRelays(1)
		if len(relays) > 0 && relays[0].EndedAt == "" {
			storedMix = relays[0].AgentMix
			resumedRelay = true
			fmt.Printf("Unfinished relay #%d is at iteration %d/%d (mix: %s). Resume or start new? [resume/new]: ",
				relays[0].ID, relays[0].CompletedIterations, relays[0].TargetIterations, relay.FormatMixLabel(relays[0].AgentMix))
			var answer string
			fmt.Scanln(&answer)
			if strings.ToLower(answer) == "new" || strings.ToLower(answer) == "n" {
				_ = relay.CompleteRelay(s, relays[0].ID)
				resumedRelay = false
			}
		}
	}

	// Handle mix resolution for resumed relays
	if resumedRelay {
		hasNewAgents := len(selectedSpecs) > 0
		if resume {
			// Non-interactive --resume: overwrite mix if new agents provided
			if hasNewAgents {
				runnerCfg.AgentMixSpecs = selectedSpecs
				runnerCfg.OverwriteMixOnResume = true
			}
			// If no new agents, keep stored mix (default behavior)
		} else {
			// Interactive resume: prompt if new agents provided
			if hasNewAgents {
				fmt.Printf("New --agent flags detected. Keep stored mix (%s) or overwrite with new mix? [keep/overwrite]: ", relay.FormatMixLabel(storedMix))
				var answer string
				fmt.Scanln(&answer)
				if strings.ToLower(answer) == "overwrite" || strings.ToLower(answer) == "o" {
					runnerCfg.AgentMixSpecs = selectedSpecs
					runnerCfg.OverwriteMixOnResume = true
				}
				// If "keep", use stored mix (default behavior)
			}
			// If no new agents, keep stored mix (default behavior)
		}
	}

	r := relay.NewRunner(s, runnerCfg, executors)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	// Ctrl+C is intentionally double-press only: a single press is treated as
	// noise (accidental touches shouldn't kill a long-running relay), and a
	// second press within doublePressWindow is the *one* action — stop the run
	// gracefully and cancel the context. We don't expose a separate
	// single-press action because the harness has Ctrl+S/Ctrl+P/Ctrl+X for
	// finer-grained controls already.
	const doublePressWindow = 3 * time.Second
	go func() {
		var lastPress time.Time
		for {
			select {
			case <-sigCh:
				now := time.Now()
				if !lastPress.IsZero() && now.Sub(lastPress) <= doublePressWindow {
					fmt.Fprintln(os.Stderr, "Stop requested. Completing current try and exiting...")
					r.RequestStop()
					cancel()
					return
				}
				lastPress = now
				fmt.Fprintln(os.Stderr, "Press Ctrl+C again within 3s to stop the relay.")
			case <-ctx.Done():
				return
			}
		}
	}()

	if err := r.Run(ctx); err != nil {
		return err
	}
	fmt.Println("Relay complete.")
	return nil
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize rally workspace",
	RunE:  runInit,
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
	rallyDir := filepath.Join(workspaceDir, ".rally")
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		return err
	}

	// 3. Create .rally/.gitignore
	gitignorePath := filepath.Join(rallyDir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		content := "current_task.md\nrelays/\nrun-state.json\n"
		if err := os.WriteFile(gitignorePath, []byte(content), 0o644); err != nil {
			return err
		}
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
		content := `schema_version = 2
laps_instructions = ""
run_hooks_on_autocommit = false
data_dir = ""

[defaults]
iterations = 5
mix = "cc cx"
claude_model = ""
codex_model = ""
gemini_model = ""
opencode_model = ""
`
		if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
			return err
		}
	}

	// 5. Create .rally/README.md
	readmePath := filepath.Join(rallyDir, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		content := `# Rally Data Directory

This directory contains rally's operational data. You can access this data
directly to understand the project's history and current state.

## JSONL Data Files (source of truth, git-tracked)
- ` + "`tries.jsonl`" + ` — One line per agent execution attempt
- ` + "`messages.jsonl`" + ` — Inbox messages for agents
- ` + "`relays.jsonl`" + ` — Relay session records
- ` + "`agent_status.jsonl`" + ` — Agent pause/freeze state history

## Quick Reference for Agents
- View recent tries (last 10): ` + "`tail -10 .rally/tries.jsonl | jq .`" + `
- View pending messages: ` + "`cat .rally/messages.jsonl | jq 'select(.status==\\\"pending\\\")'`" + `
- View current relay status: ` + "`tail -1 .rally/relays.jsonl | jq .`" + `
- Counts: ` + "`wc -l .rally/*.jsonl`" + `

## Config
- ` + "`config.toml`" + ` — Agent model configuration and runtime settings
`
		if err := os.WriteFile(readmePath, []byte(content), 0o644); err != nil {
			return err
		}
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

var instructionsCmd = &cobra.Command{
	Use:   "instructions",
	Short: "Manage project instructions",
}

var instructionsEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Edit project instructions",
	RunE:  runInstructionsEdit,
}

var instructionsShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show project instructions",
	RunE:  runInstructionsShow,
}

func runInstructionsEdit(cmd *cobra.Command, args []string) error {
	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}
	path := filepath.Join(workspaceDir, ".rally", "instructions.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		header := "# Rally Project Instructions\n\n# Add persistent instructions for rally agents below.\n# These are included in every agent session prompt.\n"
		if err := os.WriteFile(path, []byte(header), 0o644); err != nil {
			return err
		}
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	c := exec.Command(editor, path)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func runInstructionsShow(cmd *cobra.Command, args []string) error {
	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}
	path := filepath.Join(workspaceDir, ".rally", "instructions.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("(no project instructions set)")
			return nil
		}
		return err
	}
	fmt.Print(string(data))
	return nil
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("%s %s\n", app.BinaryName, release.DisplayVersion(Version))
		return nil
	},
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update Rally to the latest release",
	RunE: func(cmd *cobra.Command, args []string) error {
		exePath, err := os.Executable()
		if err != nil {
			return err
		}
		oldVersion, newVersion, updated, err := release.UpdateCurrentBinary(Version, exePath)
		if err != nil {
			return err
		}
		if !updated {
			fmt.Printf("%s is already up to date (%s)\n", app.BinaryName, newVersion)
			return nil
		}
		fmt.Printf("Updated %s from %s to %s\n", app.BinaryName, oldVersion, newVersion)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(instructionsCmd)
	rootCmd.AddCommand(cli.NewRoutesCmd())
	rootCmd.AddCommand(cli.NewHooksCmd())
	rootCmd.AddCommand(cli.NewConfigCmd())
	instructionsCmd.AddCommand(instructionsEditCmd)
	instructionsCmd.AddCommand(instructionsShowCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(updateCmd)
	progressCmd := progress.NewProgressCmd()
	rootCmd.AddCommand(progressCmd)

	// Dynamic visibility: hide progress from help when laps is enabled.
	originalHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		workspaceDir, _ := resolveWorkspaceDir()
		progressCmd.Hidden = laps.Detect(workspaceDir)
		originalHelp(cmd, args)
	})

	startCmd.Flags().IntP("iterations", "i", 0, "Number of iterations (default 50 unless laps-backed)")
	startCmd.Flags().StringArrayP("agent", "a", nil, "Agent mix (repeatable; comma- or space-separated, e.g. \"cc:2,cx:1\" or \"cc:2 cx:1\")")
	startCmd.Flags().StringArrayP("mix", "m", nil, "Legacy synonym for --agent")
	startCmd.Flags().Bool("resume", false, "Resume the last unfinished batch explicitly")
	startCmd.Flags().Bool("new", false, "Start a new batch explicitly, discarding unfinished batch state")
}

func startBackgroundUpdateCheck(argv []string, stderr io.Writer) func() {
	if os.Getenv(app.EnvNoUpdateCheck) == "1" {
		return func() {}
	}
	if len(argv) > 0 && (argv[0] == "update" || argv[0] == "version" || argv[0] == "--version" || argv[0] == "-v") {
		return func() {}
	}

	msgCh := make(chan string, 1)
	go func() {
		msg, err := release.CheckForUpdate(Version)
		if err != nil {
			msg = fmt.Sprintf("update check: %s", err)
		}
		if msg != "" {
			msgCh <- msg
		}
		close(msgCh)
	}()

	return func() {
		select {
		case msg, ok := <-msgCh:
			if ok && msg != "" {
				fmt.Fprintln(stderr, msg)
			}
		case <-time.After(25 * time.Millisecond):
		}
	}
}
