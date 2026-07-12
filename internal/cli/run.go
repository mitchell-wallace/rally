package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/spf13/cobra"
)

func newRunCmd(opts RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <path> [-- <parameter>...]",
		Short: "Run one path-backed task through a role-routed driver",
		Long: `Run a single file or directory through Rally's role routing without a laps queue.

The path is passed to the selected driver as workflow context. Directory
contents are not concatenated; the driver inspects the directory directly.
Arguments after -- are preserved as an ordered JSON parameter array in the
task prompt. The final driver response is written to stdout unless --output
names a file. Diagnostics and retry notices are written to stderr.`,
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDirect(cmd, args, opts)
		},
	}
	cmd.Flags().String("role", "intern", "Role used for driver routing")
	cmd.Flags().StringP("output", "o", "", "Write the final response to FILE instead of stdout")
	return cmd
}

func runDirect(cmd *cobra.Command, args []string, rootOpts RootOptions) error {
	dashAt := cmd.ArgsLenAtDash()
	if dashAt == -1 && len(args) > 1 {
		return fmt.Errorf("extra parameters must follow --")
	}
	if dashAt >= 0 && dashAt != 1 {
		return fmt.Errorf("rally run requires exactly one path before --")
	}

	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}
	if _, err := os.Stat(store.RallyDir(workspaceDir)); os.IsNotExist(err) {
		return fmt.Errorf("rally not initialized; run `rally init` first")
	} else if err != nil {
		return fmt.Errorf("inspect rally workspace: %w", err)
	}
	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	for _, note := range cfg.DeprecationNotes {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning:", note)
	}

	dataDir := ""
	if home, err := os.UserHomeDir(); err == nil {
		dataDir = filepath.Join(home, ".local", "share", "rally")
	}
	if cfg.DataDir != "" {
		dataDir = cfg.DataDir
	}
	role, _ := cmd.Flags().GetString("role")
	output, _ := cmd.Flags().GetString("output")
	params := []string(nil)
	if len(args) > 1 {
		params = append(params, args[1:]...)
	}

	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return app.RunDirect(runCtx, app.DirectRunOptions{
		WorkspaceDir: workspaceDir,
		InputPath:    args[0],
		Params:       params,
		Role:         role,
		OutputPath:   output,
		Config:       cfg,
		DataDir:      dataDir,
		Telemetry: app.TelemetryBuild{
			DefaultNewRelicLicenseKey:      rootOpts.NewRelic.LicenseKey,
			DefaultNewRelicAppName:         rootOpts.NewRelic.AppName,
			DefaultNewRelicHostDisplayName: rootOpts.NewRelic.HostDisplayName,
		},
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
}
