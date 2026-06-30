package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/user_prompt"
	"github.com/spf13/cobra"
)

func newStartCmd(opts RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "start",
		Aliases:      []string{"relay"},
		Short:        "Start or resume agent relays",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRelay(cmd, args, opts)
		},
	}
	cmd.Flags().IntP("iterations", "i", 0, "Number of iterations (default 50 unless laps-backed)")
	cmd.Flags().StringArrayP("agent", "a", nil, "Agent mix (repeatable; comma- or space-separated, e.g. \"cc:2,cx:1\" or \"cc:2 cx:1\")")
	cmd.Flags().StringArrayP("mix", "m", nil, "Legacy synonym for --agent")
	cmd.Flags().Bool("resume", false, "Resume the last unfinished batch explicitly")
	cmd.Flags().Bool("new", false, "Start a new batch explicitly, discarding unfinished batch state")
	return cmd
}

func runRelay(cmd *cobra.Command, args []string, opts RootOptions) error {
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

	validRoutes, err := ValidateRelayStartupRoutes(context.Background(), workspaceDir, cfg, RelayStartupRouteOptions{
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
		Telemetry:              app.TelemetryBuild{DefaultNewRelicLicenseKey: opts.NewRelic.LicenseKey, DefaultNewRelicAppName: opts.NewRelic.AppName, DefaultNewRelicHostDisplayName: opts.NewRelic.HostDisplayName},
		DiscardUnfinishedRelay: discardUnfinishedRelay,
		ResetAgentStatus:       resetAgentStatus,
		OverwriteMixOnResume:   overwriteMixOnResume,
		Out:                    os.Stdout,
		Err:                    os.Stderr,
	})
}
