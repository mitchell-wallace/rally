package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/release"
	"github.com/spf13/cobra"
)

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
