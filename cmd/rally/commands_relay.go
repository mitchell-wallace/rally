package main

import (
	"context"
	"fmt"
	"os"
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
	"github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/relay/runner"
	"github.com/mitchell-wallace/rally/internal/routing"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
	"github.com/mitchell-wallace/rally/internal/user_prompt"
	"github.com/spf13/cobra"
)

// activeTelemetry defaults to a no-op so mechanical commands stay telemetry-free.
// Relay execution initializes a real sink when telemetry is configured.
var activeTelemetry telemetry.Sink = telemetry.NoopSink{}

// activeMachineID is empty until relay execution initializes telemetry.
var activeMachineID string

var startCmd = &cobra.Command{
	Use:          "start",
	Aliases:      []string{"relay"},
	Short:        "Start or resume agent relays",
	SilenceUsage: true,
	RunE:         runRelay,
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

func telemetryConfigForRelay(cfg config.V2Config, dataDir string) telemetry.Config {
	appName := cfg.Telemetry.NewRelicAppName
	if appName == "" {
		appName = DefaultNewRelicAppName
	}
	hostDisplayName := cfg.Telemetry.NewRelicHostDisplayName
	if hostDisplayName == "" {
		hostDisplayName = DefaultNewRelicHostDisplayName
	}
	trueValue := true
	falseValue := false
	return telemetry.Config{
		Enabled:                                  cfg.Telemetry.Enabled,
		DefaultNewRelicLicenseKey:                DefaultNewRelicLicenseKey,
		NewRelicAppName:                          appName,
		NewRelicHostDisplayName:                  hostDisplayName,
		NewRelicAppLogEnabled:                    &trueValue,
		NewRelicAppLogForwardingEnabled:          &trueValue,
		NewRelicAppLogMetricsEnabled:             &trueValue,
		NewRelicAppLogDecoratingEnabled:          &falseValue,
		NewRelicAppLogForwardingMaxSamplesStored: 1000,
		DataDir:                                  dataDir,
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

	// Migrate/auto-update role instruction folders for repos that use roles.
	// Skipped when the repo never set up .rally/agents/ so roles are never
	// created implicitly for repos that don't use them.
	if _, err := os.Stat(store.AgentsDir(workspaceDir)); err == nil {
		if err := syncRoleFolders(workspaceDir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: sync role folders: %v\n", err)
		}
	}

	s, err := store.NewStore(rallyDir)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}

	executors := app.BuildExecutors(cfg)
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
			if committed, err := gitx.CommitSetupFiles(workspaceDir, hookPaths, "rally: install laps hooks"); err != nil {
				fmt.Fprintf(os.Stderr, "warning: auto-commit laps hooks: %v\n", err)
			} else if committed {
				fmt.Println("Committed laps hook setup.")
			}
		}
	}

	// Init telemetry only for relay execution. Mechanical commands like help,
	// version, and update never reach this path, so baked release credentials
	// cannot create machine-id files or open a telemetry client for those
	// commands.
	telemetryResult := telemetry.InitWithIdentity(telemetryConfigForRelay(cfg, dataDir))
	activeTelemetry = telemetryResult.Sink
	activeMachineID = telemetryResult.MachineID
	defer telemetryResult.Cleanup()

	providerIndex, err := cfg.BuildProviderIndex()
	if err != nil {
		return fmt.Errorf("resolve providers: %w", err)
	}

	runnerCfg := runner.Config{
		WorkspaceDir:           workspaceDir,
		DataDir:                dataDir,
		MachineID:              activeMachineID,
		AgentMixSpecs:          selectedSpecs,
		RouteSpecs:             cfg.Routes,
		Reasoning:              cfg.Reasoning,
		ReasoningResolver:      cfg.ResolveRoleReasoning,
		Providers:              providerIndex,
		UseOverrideRoute:       usedOverride,
		TargetIterations:       iterations,
		StallThreshold:         cfg.Reliability.StallThreshold(),
		LivenessProbe:          cfg.Reliability.LivenessProbe,
		RetryBudget:            cfg.Reliability.RetryBudget,
		RunTimeout:             cfg.Reliability.RunTimeout(),
		TryTimeout:             cfg.Reliability.TryTimeout(),
		HandoffTimeout:         cfg.Reliability.HandoffTimeout(),
		RunHooksOnAutoCommit:   cfg.RunHooksOnAutoCommit,
		LapsEnabled:            lapsEnabled,
		LapsInstructionsFile:   cfg.Laps.InstructionsFile,
		FreeRunPromptFile:      cfg.FreeRun.PromptFile,
		RecentTryCount:         cfg.Reliability.RecentTryCount,
		RecentTryCharLimit:     cfg.Reliability.RecentTryCharLimit,
		RecentContextCharLimit: cfg.Reliability.RecentContextCharLimit,
	}

	runnerCfg.Resolver = func(spec string) (agent.ResolvedAgent, error) {
		ra, err := cfg.ResolveAgent(spec)
		if err != nil {
			return agent.ResolvedAgent{}, err
		}
		return ra, nil
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

	r := runner.NewRunner(s, runnerCfg, executors)
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
