package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mitchell-wallace/rally/internal/cli"
	"github.com/mitchell-wallace/rally/internal/release"
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

	rootCmd := cli.NewRootCommand(cli.RootOptions{
		Version: Version,
		NewRelic: cli.NewRelicOptions{
			LicenseKey:      DefaultNewRelicLicenseKey,
			AppName:         DefaultNewRelicAppName,
			HostDisplayName: DefaultNewRelicHostDisplayName,
		},
	})
	if err := rootCmd.Execute(); err != nil {
		flushUpdateNotice()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	flushUpdateNotice()
}

func startBackgroundUpdateCheck(argv []string, stderr io.Writer) func() {
	if os.Getenv(release.EnvNoUpdateCheck) == "1" {
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
