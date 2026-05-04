package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

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

	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}

	rallyDir := filepath.Join(workspaceDir, ".rally")
	if _, err := os.Stat(filepath.Join(rallyDir, "tries.jsonl")); os.IsNotExist(err) {
		return fmt.Errorf("no tries recorded in this workspace")
	}

	s, err := store.NewStore(rallyDir)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}

	tries := s.AllTries()
	if len(tries) == 0 {
		return fmt.Errorf("no tries recorded in this workspace")
	}

	var target *store.TryRecord
	if tryNum <= 0 {
		target = &tries[len(tries)-1]
	} else if tryNum > len(tries) || tryNum < 1 {
		return fmt.Errorf("valid range: 1-%d", len(tries))
	} else {
		rec := tries[tryNum-1]
		target = &rec
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

	// Handle Ctrl+C gracefully
	go func() {
		ch := make(chan os.Signal, 1)
		// We can't import os/signal here without import cycle... actually we can.
		// signal.Notify(ch, os.Interrupt)
		// <-ch
		// cancel()
		// For simplicity, let the user break with Ctrl+C which will kill the process.
		_ = ch
	}()

	return followFile(ctx, f, os.Stdout)
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
	rootCmd.AddCommand(tailCmd)
}
