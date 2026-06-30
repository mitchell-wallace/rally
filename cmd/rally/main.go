package main

import (
	"fmt"
	"os"

	"github.com/mitchell-wallace/rally/internal/cli"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/release"
	"github.com/spf13/cobra"
)

var Version = "dev"

// DefaultNewRelicLicenseKey is the baked-in New Relic license key for release
// binaries. GoReleaser injects it via -X main.DefaultNewRelicLicenseKey=... at
// build time.
// Dev builds (go build) leave it empty, so telemetry only activates when
// explicitly configured via env.
var DefaultNewRelicLicenseKey = ""

// DefaultNewRelicAppName and DefaultNewRelicHostDisplayName are optional
// release-time defaults. Empty values allow telemetry's backend defaults to
// apply.
var DefaultNewRelicAppName = ""
var DefaultNewRelicHostDisplayName = ""

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
	rootCmd.SetVersionTemplate(release.BinaryName + " {{.Version}}\n")
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
