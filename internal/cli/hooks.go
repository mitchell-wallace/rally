package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/spf13/cobra"
)

// NewHooksCmd returns the `rally hooks` command group. It surfaces the laps
// hooks that rally installs into .laps/hooks.json and the audit trail those
// hooks write to .rally/hook-audit.jsonl.
func NewHooksCmd() *cobra.Command {
	hooksCmd := &cobra.Command{
		Use:   "hooks",
		Short: "Inspect rally-installed laps hooks",
		Long: `Inspect the laps hooks rally installs and the audit trail they emit.

Rally wires three scripts into .laps/hooks.json (laps-done, laps-handoff,
laps-wrapup). Each fires when an agent uses the corresponding ` + "`laps`" + ` command;
the script writes an entry to .rally/hook-audit.jsonl so missed or extra
hook firings are visible after the fact.`,
	}

	hooksCmd.AddCommand(newHooksListCmd())
	hooksCmd.AddCommand(newHooksLogsCmd())
	hooksCmd.AddCommand(newHooksHelpCmd())
	return hooksCmd
}

func newHooksListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List rally hooks currently registered in .laps/hooks.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			wsDir, err := resolveWorkspaceDir()
			if err != nil {
				return err
			}
			hooksPath := filepath.Join(wsDir, ".laps", "hooks.json")
			data, err := os.ReadFile(hooksPath)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Fprintln(cmd.OutOrStdout(), "No .laps/hooks.json found. Run a rally command in a laps-enabled workspace to install hooks.")
					return nil
				}
				return fmt.Errorf("read hooks.json: %w", err)
			}
			var hf laps.HooksFile
			if err := json.Unmarshal(data, &hf); err != nil {
				return fmt.Errorf("parse hooks.json: %w", err)
			}
			out := cmd.OutOrStdout()
			if len(hf.Hooks) == 0 {
				fmt.Fprintln(out, "(no hooks registered)")
				return nil
			}
			for _, h := range hf.Hooks {
				owner := "user"
				if strings.HasPrefix(h.Title, "rally:") {
					owner = "rally"
				}
				fmt.Fprintf(out, "%s [%s]\n", h.Title, owner)
				if h.Description != "" {
					fmt.Fprintf(out, "  %s\n", h.Description)
				}
				fmt.Fprintf(out, "  on: %s %s\n", h.When, h.Command)
				fmt.Fprintf(out, "  run: %s\n", h.Run)
			}
			return nil
		},
	}
}

func newHooksLogsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "logs",
		Short: "Print recent rally hook audit entries from .rally/hook-audit.jsonl",
		RunE: func(cmd *cobra.Command, args []string) error {
			wsDir, err := resolveWorkspaceDir()
			if err != nil {
				return err
			}
			tail, _ := cmd.Flags().GetInt("tail")
			auditPath := store.HookAuditPath(wsDir)
			f, err := os.Open(auditPath)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Fprintln(cmd.OutOrStdout(), "No hook audit log yet (.rally/hook-audit.jsonl).")
					return nil
				}
				return fmt.Errorf("open audit log: %w", err)
			}
			defer f.Close()
			var lines []string
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for scanner.Scan() {
				if line := strings.TrimSpace(scanner.Text()); line != "" {
					lines = append(lines, line)
				}
			}
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read audit log: %w", err)
			}
			start := 0
			if tail > 0 && len(lines) > tail {
				start = len(lines) - tail
			}
			for _, line := range lines[start:] {
				fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}
	c.Flags().IntP("tail", "n", 20, "Show only the last N entries (0 = all)")
	return c
}

func newHooksHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "help",
		Short: "Explain how rally's laps hooks work",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, hooksHelpText)
			return nil
		},
	}
}

const hooksHelpText = `Rally + laps hooks — how they fit together

When rally runs in a laps-enabled workspace it installs three shell scripts
under .laps/hooks/rally/ and registers them in .laps/hooks.json:

  • laps-done-hook.sh    — fires after ` + "`laps done`" + `. Records lap completion
                           and prints a passback nudging the agent to wrap up.
  • laps-handoff-hook.sh — fires before ` + "`laps handoff`" + `. Marks the run as
                           handed off and prints a passback for wrapup.
  • laps-wrapup-hook.sh  — fires before ` + "`laps wrapup`" + `. Routes the wrapup
                           to rally so progress and follow-up laps are saved.

Every firing appends a JSONL record to .rally/hook-audit.jsonl with the
timestamp, hook name, and pid. Use ` + "`rally hooks logs`" + ` to inspect that
audit trail (` + "`-n`" + ` for tail size). Use ` + "`rally hooks list`" + ` to see what is
currently registered in hooks.json.

The scripts are re-written every ` + "`rally start`" + ` so manual edits to files in
.laps/hooks/rally/ are overwritten — keep your own hooks under a different
title (anything that does not start with "rally:") and rally will leave them
alone.`
