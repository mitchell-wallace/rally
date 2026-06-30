package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/cli"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/relay"
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

	discardUnfinishedRelay := newBatch
	resetAgentStatus := newBatch
	overwriteMixOnResume := false

	// Resume check: if --resume not passed and incomplete relay exists, prompt.
	resumedRelay := false
	var storedMix string
	if !resume && !newBatch {
		resumeInfo, err := app.InspectResume(workspaceDir)
		if err != nil {
			return err
		}
		if resumeInfo.HasUnfinished {
			storedMix = resumeInfo.AgentMix
			resumedRelay = true
			choice, err := user_prompt.Select(
				os.Stdin, os.Stderr,
				fmt.Sprintf("Unfinished relay #%d at iteration %d/%d (mix: %s)", resumeInfo.RelayID, resumeInfo.CompletedIterations, resumeInfo.TargetIterations, relay.FormatMixLabel(resumeInfo.AgentMix)),
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
				discardUnfinishedRelay = true
				resumedRelay = false
			}
		}
	}

	// Handle mix resolution for resumed relays
	if resumedRelay {
		hasNewAgents := len(selectedSpecs) > 0
		if hasNewAgents {
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
				overwriteMixOnResume = true
			}
		}
	}

	taskPrompt := ""
	if len(args) > 0 {
		taskPrompt = strings.Join(args, " ")
	}

	return app.StartRelay(context.Background(), app.RelayStartOptions{
		WorkspaceDir:           workspaceDir,
		Config:                 cfg,
		TaskPrompt:             taskPrompt,
		AgentMixSpecs:          selectedSpecs,
		UsedOverride:           usedOverride,
		TargetIters:            iterations,
		LapsEnabled:            lapsEnabled,
		DataDir:                dataDir,
		Telemetry:              app.TelemetryBuild{DefaultNewRelicLicenseKey: DefaultNewRelicLicenseKey, DefaultNewRelicAppName: DefaultNewRelicAppName, DefaultNewRelicHostDisplayName: DefaultNewRelicHostDisplayName},
		DiscardUnfinishedRelay: discardUnfinishedRelay,
		ResetAgentStatus:       resetAgentStatus,
		OverwriteMixOnResume:   overwriteMixOnResume,
		Out:                    os.Stdout,
		Err:                    os.Stderr,
	})
}
