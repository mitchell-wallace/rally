package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/spf13/cobra"
)

var tailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Stream a try's log file",
	RunE:  runTail,
}

func runTail(cmd *cobra.Command, args []string) error {
	tryNum, _ := cmd.Flags().GetInt("try")
	highlight, _ := cmd.Flags().GetString("highlight")

	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}

	target, err := tailTarget(workspaceDir, tryNum)
	if err != nil {
		return err
	}

	if target.LogPath == "" {
		return fmt.Errorf("try %d has no log file", tryNum)
	}

	f, err := os.Open(target.LogPath)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var out io.Writer = os.Stdout
	if highlight != "off" && highlight != "" {
		if highlight != "heuristic" && highlight != "chroma" {
			return fmt.Errorf("invalid highlight mode %q, expected one of: off, heuristic, chroma", highlight)
		}
		out = newHighlightWriter(out, highlight)
	}

	return followFile(ctx, f, out)
}

func tailTarget(workspaceDir string, tryNum int) (*store.TryRecord, error) {
	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		return nil, fmt.Errorf("load store: %w", err)
	}

	if tryNum <= 0 {
		rs, err := progress.LoadRunState(workspaceDir)
		if err == nil && rs.ActiveLogPath != "" {
			isStale := false
			var staleReason string

			if _, err := os.Stat(rs.ActiveLogPath); os.IsNotExist(err) {
				isStale = true
				staleReason = "missing log path"
			} else if startedAt, err := time.Parse(time.RFC3339, rs.ActiveStartedAt); err == nil && time.Since(startedAt) > 24*time.Hour {
				isStale = true
				staleReason = "implausibly old active_started_at"
			} else {
				relay := s.GetRelay(rs.ActiveRelayID)
				if relay == nil || relay.EndedAt != "" {
					isStale = true
					staleReason = "metadata belonging to no unfinished relay/run"
				}
			}

			if isStale {
				fmt.Fprintf(os.Stderr, "warning: stale active try ignored (%s), falling back to newest completed try\n", staleReason)
			} else {
				return &store.TryRecord{LogPath: rs.ActiveLogPath}, nil
			}
		}

		tries := s.AllTries()
		if len(tries) == 0 {
			return nil, fmt.Errorf("no tries recorded in this workspace")
		}

		for i := len(tries) - 1; i >= 0; i-- {
			if tries[i].Completed {
				return &tries[i], nil
			}
		}
		return &tries[len(tries)-1], nil
	}

	tries := s.AllTries()
	if len(tries) == 0 {
		return nil, fmt.Errorf("no tries recorded in this workspace")
	}

	if tryNum > len(tries) || tryNum < 1 {
		return nil, fmt.Errorf("valid range: 1-%d", len(tries))
	}

	rec := tries[tryNum-1]
	return &rec, nil
}

func followFile(ctx context.Context, f *os.File, out io.Writer) error {
	// Read existing content
	if _, err := io.Copy(out, f); err != nil {
		return err
	}

	info, err := f.Stat()
	if err != nil {
		return err
	}
	lastSize := info.Size()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			info, err := f.Stat()
			if err != nil {
				return err
			}
			if info.Size() < lastSize {
				// File truncated; start over
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					return err
				}
				lastSize = 0
			}
			if info.Size() > lastSize {
				if _, err := io.Copy(out, f); err != nil {
					return err
				}
				lastSize = info.Size()
			}
		}
	}
}

func init() {
	tailCmd.Flags().Int("try", 0, "Try number to tail (1-based, default: latest)")
	tailCmd.Flags().String("highlight", "off", "Syntax highlighting mode: off, heuristic, chroma")
	rootCmd.AddCommand(tailCmd)
}
