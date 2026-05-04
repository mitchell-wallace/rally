package progress

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// getWorkspaceDir resolves the workspace directory (git root or cwd).
// It is overridable for testing.
var getWorkspaceDir = defaultGetWorkspaceDir

func defaultGetWorkspaceDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = wd
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	return wd, nil
}

// execLapsAddHead shells out to `laps add head`. It is overridable for testing.
var execLapsAddHead = func(workspaceDir, title, description string) (string, error) {
	cmd := exec.Command("laps", "add", "head", "--title", title, "--description", description)
	cmd.Dir = workspaceDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("laps add head failed: %w\noutput: %s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// NewProgressCmd returns the cobra command definition for `rally progress`.
func NewProgressCmd() *cobra.Command {
	var recordLaps []string
	var complete, handoff, setHandoffFlag, wrapup bool
	var summary string
	var followups []string

	cmd := &cobra.Command{
		Use:   "progress",
		Short: "Manage rally progress log and run state",
		RunE: func(cmd *cobra.Command, args []string) error {
			workspaceDir, err := getWorkspaceDir()
			if err != nil {
				return err
			}

			// Handle simple state mutations.
			if len(recordLaps) > 0 {
				for _, lapID := range recordLaps {
					if err := RecordLap(workspaceDir, lapID); err != nil {
						return fmt.Errorf("record lap: %w", err)
					}
				}
			}

			if setHandoffFlag {
				if err := SetHandoff(workspaceDir); err != nil {
					return fmt.Errorf("set handoff: %w", err)
				}
			}

			// Determine finalize mode.
			mode := ""
			if complete {
				mode = "complete"
			} else if handoff {
				mode = "handoff"
			} else if wrapup {
				mode = "wrapup"
			}

			if mode == "" {
				if len(recordLaps) > 0 || setHandoffFlag {
					return nil
				}
				return fmt.Errorf("no action specified")
			}

			if summary == "" {
				return fmt.Errorf("--summary is required for --%s", mode)
			}

			return runFinalize(workspaceDir, mode, summary, followups)
		},
	}

	cmd.Flags().StringArrayVar(&recordLaps, "record-lap", nil, "Record a lap ID (repeatable)")
	cmd.Flags().BoolVar(&complete, "complete", false, "Mark run as complete")
	cmd.Flags().BoolVar(&handoff, "handoff", false, "Mark run as handoff")
	cmd.Flags().BoolVar(&setHandoffFlag, "set-handoff", false, "Set handoff state")
	cmd.Flags().BoolVar(&wrapup, "wrapup", false, "Wrap up run based on handoff state")
	cmd.Flags().StringVar(&summary, "summary", "", "Summary of the run")
	cmd.Flags().StringArrayVar(&followups, "followup", nil, "Follow-up items (repeatable)")

	return cmd
}

func runFinalize(workspaceDir string, mode string, summary string, followups []string) error {
	rs, err := LoadRunState(workspaceDir)
	if err != nil {
		return fmt.Errorf("load run state: %w", err)
	}

	runID := rs.RunID
	if runID == "" {
		runID = time.Now().UTC().Format("20060102-150405")
	}

	var lapsCompleted interface{}
	if len(rs.RecordedLaps) > 0 {
		lapsCompleted = rs.RecordedLaps
	} else {
		lapsCompleted = "none"
	}

	entry := RunEntry{
		RunID:         runID,
		Summary:       summary,
		LapsCompleted: lapsCompleted,
	}

	// Determine whether this is a handoff entry.
	isHandoff := mode == "handoff"
	if mode == "wrapup" && rs.HandoffState == 1 {
		isHandoff = true
	}

	if isHandoff {
		he := &HandoffEntry{
			Summary:   summary,
			Followups: followups,
		}

		for _, f := range followups {
			title := f
			if len(title) > 30 {
				title = title[:30] + "..."
			}
			lapID, err := execLapsAddHead(workspaceDir, title, f)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warn: failed to create lap for followup %q: %v\n", f, err)
				continue
			}
			he.CreatedLapIDs = append(he.CreatedLapIDs, lapID)
		}

		entry.Handoff = he
	}

	if err := AppendRunEntry(workspaceDir, entry); err != nil {
		return fmt.Errorf("append run entry: %w", err)
	}

	if err := ClearRunState(workspaceDir); err != nil {
		return fmt.Errorf("clear run state: %w", err)
	}

	return nil
}
