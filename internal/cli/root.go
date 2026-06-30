package cli

import (
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/release"
	"github.com/spf13/cobra"
)

type RootOptions struct {
	Version  string
	NewRelic NewRelicOptions
}

type NewRelicOptions struct {
	LicenseKey      string
	AppName         string
	HostDisplayName string
}

func NewRootCommand(opts RootOptions) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "rally",
		Short: "Agent orchestrator",
		Long:  `Rally is a CLI agent orchestrator for managing multi-agent relay sessions.`,
	}

	// Register the --version flag with a -v short alias before Cobra would
	// auto-register a long-only one (Cobra reuses an existing "version" flag if
	// it finds one already declared).
	rootCmd.Flags().BoolP("version", "v", false, "Print version and exit")
	rootCmd.Version = release.DisplayVersion(opts.Version)
	rootCmd.SetVersionTemplate(release.BinaryName + " {{.Version}}\n")

	rootCmd.AddCommand(newStartCmd(opts))
	rootCmd.AddCommand(newInitCmd())
	rootCmd.AddCommand(newInstructionsCmd())
	rootCmd.AddCommand(NewRoutesCmd())
	rootCmd.AddCommand(NewHooksCmd())
	rootCmd.AddCommand(NewConfigCmd())
	rootCmd.AddCommand(newVersionCmd(opts.Version))
	rootCmd.AddCommand(newUpdateCmd(opts.Version))
	progressCmd := progress.NewProgressCmd()
	rootCmd.AddCommand(progressCmd)
	rootCmd.AddCommand(newTailCmd())

	rootCmd.AddCommand(&cobra.Command{
		Use:    "init-roles",
		Short:  "Alias for `rally init all`",
		Hidden: true,
		RunE:   runInitAll,
	})

	// Dynamic visibility: hide progress from help when laps is enabled.
	originalHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		workspaceDir, _ := resolveWorkspaceDir()
		progressCmd.Hidden = laps.Detect(workspaceDir)
		originalHelp(cmd, args)
	})

	return rootCmd
}
