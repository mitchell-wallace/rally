package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/presentation/tuisafe"
	"github.com/spf13/cobra"
)

func newTui1Cmd(opts RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "tui-1",
		Short:        "TUI prototype 1: safe alternate-screen transpose of the CLI output",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTui1(cmd, args, opts)
		},
	}
	cmd.Flags().IntP("iterations", "i", 0, "Number of iterations (default 50 unless laps-backed)")
	cmd.Flags().StringArrayP("agent", "a", nil, "Agent mix (repeatable; comma- or space-separated, e.g. \"cc:2,cx:1\" or \"cc:2 cx:1\")")
	cmd.Flags().StringArrayP("mix", "m", nil, "Legacy synonym for --agent")
	cmd.Flags().Bool("resume", false, "Resume the last unfinished batch explicitly")
	cmd.Flags().Bool("new", false, "Start a new batch explicitly, discarding unfinished batch state")
	cmd.Flags().Bool("demo", false, "Run synthetic TUI demo playback without a .rally workspace")
	cmd.Flags().String("view", "", "View a historical relay (latest, or a relay ID)")
	cmd.Flags().Lookup("view").NoOptDefVal = "latest"
	return cmd
}

func runTui1(cmd *cobra.Command, args []string, opts RootOptions) error {
	demo, _ := cmd.Flags().GetBool("demo")
	view, _ := cmd.Flags().GetString("view")
	resume, _ := cmd.Flags().GetBool("resume")
	newBatch, _ := cmd.Flags().GetBool("new")
	if view != "" {
		var conflicts []string
		if demo {
			conflicts = append(conflicts, "--demo")
		}
		if resume {
			conflicts = append(conflicts, "--resume")
		}
		if newBatch {
			conflicts = append(conflicts, "--new")
		}
		if len(conflicts) > 0 {
			return fmt.Errorf("--view cannot be used with %s", strings.Join(conflicts, ", "))
		}
		workspaceDir, err := resolveWorkspaceDir()
		if err != nil {
			return err
		}
		events, relayID, err := synthesizeRelayEvents(workspaceDir, view)
		if err != nil {
			return err
		}
		session := tuisafe.NewSession(tuisafe.Options{
			Title:    "rally tui-1",
			DoneHint: fmt.Sprintf("viewing relay #%d — q to exit", relayID),
		})
		return session.RunView(context.Background(), events)
	}

	session := tuisafe.NewSession(tuisafe.Options{Title: "rally tui-1"})
	if demo {
		return session.RunDemo(context.Background())
	}

	ro, err := prepareRelayStart(cmd, args, opts)
	if err != nil {
		return err
	}
	ro.EventSink = session.Sink()
	ro.Controls = session.Controls()
	ro.StatusWriter = session.StatusWriter()
	ro.Out = session.TranscriptWriter()
	ro.Err = session.TranscriptWriter()
	return session.Run(context.Background(), func(ctx context.Context) error {
		return app.StartRelay(ctx, ro)
	})
}
