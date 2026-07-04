package cli

import (
	"context"

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
	return cmd
}

func runTui1(cmd *cobra.Command, args []string, opts RootOptions) error {
	demo, _ := cmd.Flags().GetBool("demo")
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
