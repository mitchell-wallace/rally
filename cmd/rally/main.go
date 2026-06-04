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
	"github.com/mitchell-wallace/rally/internal/telemetry"
	"github.com/mitchell-wallace/rally/internal/user_prompt"
	"github.com/spf13/cobra"
)

var Version = "dev"

// activeTelemetry holds the process-wide telemetry sink initialised in main.
// It defaults to a no-op so any command that runs before/without init still
// has a safe sink. The relay runner reads it via SetTelemetry.
var activeTelemetry telemetry.Sink = telemetry.NoopSink{}

func main() {
	flushUpdateNotice := startBackgroundUpdateCheck(os.Args[1:], os.Stderr)

	// Init telemetry — no-op when no DSN or RALLY_TELEMETRY=0.
	sink, flushTelemetry := telemetry.Init(loadTelemetryConfig())
	activeTelemetry = sink
	defer flushTelemetry()

	if err := rootCmd.Execute(); err != nil {
		flushUpdateNotice()
		flushTelemetry()
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

// loadTelemetryConfig reads the workspace config and returns the telemetry
// section. Returns a zero Config on any error — telemetry is best-effort
// and must never prevent the CLI from starting.
func loadTelemetryConfig() telemetry.Config {
	wsDir, err := resolveWorkspaceDir()
	if err != nil {
		return telemetry.Config{}
	}
	cfg, err := config.LoadV2(wsDir)
	if err != nil {
		return telemetry.Config{}
	}
	return telemetry.Config{
		SentryDSN: cfg.Telemetry.SentryDSN,
	}
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

	rallyDir := store.RallyDir(workspaceDir)
	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	for _, note := range cfg.DeprecationNotes {
		fmt.Fprintln(os.Stderr, "warning:", note)
	}

	if w := laps.VersionWarning(workspaceDir); w != "" {
		fmt.Fprintln(os.Stderr, w)
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
		"antigravity": &agent.AntigravityExecutor{Model: cfg.AntigravityModel},
		"claude":      &agent.ClaudeExecutor{Model: cfg.ClaudeModel},
		"codex":       &agent.CodexExecutor{Model: cfg.CodexModel},
		"gemini":      &agent.GeminiExecutor{Model: cfg.GeminiModel},
		"opencode":    &agent.OpenCodeExecutor{Model: cfg.OpenCodeModel},
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
			hookPaths := []string{
				".laps/hooks.json",
				".laps/hooks/rally/laps-done-hook.sh",
				".laps/hooks/rally/laps-handoff-hook.sh",
				".laps/hooks/rally/laps-wrapup-hook.sh",
			}
			if committed, err := commitSetupFiles(workspaceDir, hookPaths, "rally: install laps hooks"); err != nil {
				fmt.Fprintf(os.Stderr, "warning: auto-commit laps hooks: %v\n", err)
			} else if committed {
				fmt.Println("Committed laps hook setup.")
			}
		}
	}

	runnerCfg := relay.Config{
		WorkspaceDir:             workspaceDir,
		DataDir:                  dataDir,
		AgentMixSpecs:            selectedSpecs,
		RouteSpecs:               cfg.Routes,
		UseOverrideRoute:         usedOverride,
		TargetIterations:         iterations,
		StallThreshold:           cfg.Reliability.StallThreshold(),
		LivenessProbe:            cfg.Reliability.LivenessProbe,
		RetryBudget:              cfg.Reliability.RetryBudget,
		RunHooksOnAutoCommit:     cfg.RunHooksOnAutoCommit,
		LapsEnabled:              lapsEnabled,
		LapsInstructionsFile:     cfg.Laps.InstructionsFile,
		FreeRunPromptFile:        cfg.FreeRun.PromptFile,
		RecentTryCount:           cfg.Reliability.RecentTryCount,
		RecentTryCharLimit:       cfg.Reliability.RecentTryCharLimit,
		RecentContextCharLimit:   cfg.Reliability.RecentContextCharLimit,
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
		if err := s.ResetAgentStatus(); err != nil {
			return fmt.Errorf("reset agent status: %w", err)
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
			choice, err := user_prompt.Select(
				os.Stdin, os.Stderr,
				fmt.Sprintf("Unfinished relay #%d at iteration %d/%d (mix: %s)", relays[0].ID, relays[0].CompletedIterations, relays[0].TargetIterations, relay.FormatMixLabel(relays[0].AgentMix)),
				"Resume the existing relay or discard it and start a new one?",
				[]user_prompt.Option{
					{Label: "Resume", Value: "resume"},
					{Label: "Start new", Value: "new"},
				},
				"resume",
			)
			if err != nil {
				return err
			}
			if choice == "new" {
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
		} else if hasNewAgents {
			choice, err := user_prompt.Select(
				os.Stdin, os.Stderr,
				"New --agent flags detected",
				fmt.Sprintf("Keep stored mix (%s) or overwrite with the new mix?", relay.FormatMixLabel(storedMix)),
				[]user_prompt.Option{
					{Label: "Keep stored", Value: "keep"},
					{Label: "Overwrite", Value: "overwrite"},
				},
				"keep",
			)
			if err != nil {
				return err
			}
			if choice == "overwrite" {
				runnerCfg.AgentMixSpecs = selectedSpecs
				runnerCfg.OverwriteMixOnResume = true
			}
		}
	}

	r := relay.NewRunner(s, runnerCfg, executors)
	r.SetTelemetry(activeTelemetry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	// Silent double-press: ignore a single Ctrl+C (accidental touches
	// shouldn't kill a long-running relay), fire on the second within the
	// confirm window.
	const doublePressWindow = 3 * time.Second
	go func() {
		var lastPress time.Time
		for {
			select {
			case <-sigCh:
				now := time.Now()
				if !lastPress.IsZero() && now.Sub(lastPress) <= doublePressWindow {
					r.RequestStop()
					cancel()
					return
				}
				lastPress = now
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
antigravity_model = ""

[telemetry]
sentry_dsn = ""
`
		if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
			return err
		}
	}

	// 5. Create .rally/README.md
	readmePath := filepath.Join(rallyDir, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		content := `# Rally Data Directory

This directory contains rally's workspace configuration and local runtime data.

## Tracked Files
- ` + "`config.toml`" + ` — Agent model configuration and runtime settings
- ` + "`agents/`" + ` — Role instruction files
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
	path := store.InstructionsPath(workspaceDir)
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
	path := store.InstructionsPath(workspaceDir)
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
	Short: "Update rally and the bundled laps binary to their latest releases",
	Long: `Update rally to the latest release and upgrade the bundled laps
companion binary alongside it. Laps is installed next to the rally
executable so the two travel together; laps remains independently usable.`,
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
		} else {
			fmt.Printf("Updated %s from %s to %s\n", app.BinaryName, oldVersion, newVersion)
		}

		// Upgrade the bundled laps binary alongside rally. A laps failure is
		// non-fatal: rally itself is already updated, and laps stays
		// independently installable.
		lapsDest := filepath.Join(filepath.Dir(exePath), release.Laps.BinaryName)
		lapsCurrent, _ := laps.CompanionVersion()
		oldLaps, newLaps, lapsUpdated, err := release.UpdateTool(release.Laps, lapsCurrent, lapsDest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not update laps: %v\n", err)
			return nil
		}
		switch {
		case lapsCurrent == "" && lapsUpdated:
			fmt.Printf("Installed %s %s\n", release.Laps.BinaryName, newLaps)
		case !lapsUpdated:
			fmt.Printf("%s is already up to date (%s)\n", release.Laps.BinaryName, newLaps)
		default:
			fmt.Printf("Updated %s from %s to %s\n", release.Laps.BinaryName, oldLaps, newLaps)
		}
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
