package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/release"
	"github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/spf13/cobra"
)

var Version = "dev"

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

var relayCmd = &cobra.Command{
	Use:   "relay",
	Short: "Start or resume agent relays",
	RunE:  runRelay,
}

func runRelay(cmd *cobra.Command, args []string) error {
	iterations, _ := cmd.Flags().GetInt("iterations")
	agentSpecs, _ := cmd.Flags().GetStringArray("agent")
	resume, _ := cmd.Flags().GetBool("resume")
	newBatch, _ := cmd.Flags().GetBool("new")

	if resume && newBatch {
		return fmt.Errorf("cannot use --resume and --new together")
	}

	var expandedAgents []string
	for _, spec := range agentSpecs {
		fields := strings.Fields(spec)
		if len(fields) == 0 {
			return fmt.Errorf("empty value for --agent")
		}
		expandedAgents = append(expandedAgents, fields...)
	}

	workspaceDir, err := os.Getwd()
	if err != nil {
		return err
	}

	rallyDir := filepath.Join(workspaceDir, ".rally")
	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	dataDir := filepath.Join(os.Getenv("HOME"), ".local", "share", "rally")
	if home, err := os.UserHomeDir(); err == nil {
		dataDir = filepath.Join(home, ".local", "share", "rally")
	}

	if _, err := os.Stat(rallyDir); os.IsNotExist(err) {
		return fmt.Errorf("rally not initialized; run `rally init` first")
	}

	s, err := store.NewStore(rallyDir)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}

	executors := map[string]agent.Executor{
		"claude":   &agent.ClaudeExecutor{Model: cfg.ClaudeModel},
		"codex":    &agent.CodexExecutor{Model: cfg.CodexModel},
		"gemini":   &agent.GeminiExecutor{Model: cfg.GeminiModel},
		"opencode": &agent.OpenCodeExecutor{Model: cfg.OpenCodeModel},
	}

	runnerCfg := relay.Config{
		WorkspaceDir:         workspaceDir,
		DataDir:              dataDir,
		AgentMixSpecs:        expandedAgents,
		TargetIterations:     iterations,
		RunHooksOnAutoCommit: cfg.RunHooksOnAutoCommit,
		BeadsEnabled:         cfg.Beads == "true",
	}

	if newBatch {
		relays := s.RecentRelays(1)
		if len(relays) > 0 && relays[0].EndedAt == "" {
			_ = relay.CompleteRelay(s, relays[0].ID)
		}
	}

	// Resume check: if --resume not passed and incomplete relay exists, prompt
	if !resume && !newBatch {
		relays := s.RecentRelays(1)
		if len(relays) > 0 && relays[0].EndedAt == "" {
			fmt.Printf("Unfinished relay #%d is at iteration %d/%d. Resume or start new? [resume/new]: ",
				relays[0].ID, relays[0].CompletedIterations, relays[0].TargetIterations)
			var answer string
			fmt.Scanln(&answer)
			if strings.ToLower(answer) == "new" || strings.ToLower(answer) == "n" {
				_ = relay.CompleteRelay(s, relays[0].ID)
			}
		}
	}

	r := relay.NewRunner(s, runnerCfg, executors)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := r.Run(ctx); err != nil {
		return err
	}
	fmt.Println("Relay complete.")
	return nil
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize rally workspace",
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	workspaceDir, err := os.Getwd()
	if err != nil {
		return err
	}

	// 1. Run git init if not already a git repo
	if _, err := os.Stat(filepath.Join(workspaceDir, ".git")); os.IsNotExist(err) {
		if err := exec.Command("git", "init").Run(); err != nil {
			return fmt.Errorf("git init failed: %w", err)
		}
		fmt.Println("Initialized empty Git repository")
	}

	// 2. Create .rally/ directory
	rallyDir := filepath.Join(workspaceDir, ".rally")
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		return err
	}

	// 3. Create .rally/.gitignore
	gitignorePath := filepath.Join(rallyDir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		content := "current_task.md\nrelays/\n"
		if err := os.WriteFile(gitignorePath, []byte(content), 0o644); err != nil {
			return err
		}
	}

	// 4. Create .rally/config.toml
	configPath := filepath.Join(rallyDir, "config.toml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		content := `claude_model = ""
codex_model = ""
gemini_model = ""
opencode_model = ""
beads = "auto"
run_hooks_on_autocommit = false
`
		if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
			return err
		}
	}

	// 5. Create .rally/README.md
	readmePath := filepath.Join(rallyDir, "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		content := `# Rally Data Directory

This directory contains rally's operational data.

## Quick Reference
- Recent tries: ` + "`tail -10 .rally/tries.jsonl`" + `
- Messages: ` + "`cat .rally/messages.jsonl`" + `
- Config: ` + "`.rally/config.toml`" + `
`
		if err := os.WriteFile(readmePath, []byte(content), 0o644); err != nil {
			return err
		}
	}

	fmt.Println("Rally workspace initialized.")
	return nil
}

var instructionsCmd = &cobra.Command{
	Use:   "instructions",
	Short: "Manage project instructions",
}

var instructionsEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Edit project instructions",
	RunE:  runInstructionsEdit,
}

var instructionsShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show project instructions",
	RunE:  runInstructionsShow,
}

func runInstructionsEdit(cmd *cobra.Command, args []string) error {
	workspaceDir, err := os.Getwd()
	if err != nil {
		return err
	}
	path := filepath.Join(workspaceDir, ".rally", "instructions.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		header := "# Rally Project Instructions\n\n# Add persistent instructions for rally agents below.\n# These are included in every agent session prompt.\n"
		if err := os.WriteFile(path, []byte(header), 0o644); err != nil {
			return err
		}
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	c := exec.Command(editor, path)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func runInstructionsShow(cmd *cobra.Command, args []string) error {
	workspaceDir, err := os.Getwd()
	if err != nil {
		return err
	}
	path := filepath.Join(workspaceDir, ".rally", "instructions.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("(no project instructions set)")
			return nil
		}
		return err
	}
	fmt.Print(string(data))
	return nil
}

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
	Short: "Update Rally to the latest release",
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
			return nil
		}
		fmt.Printf("Updated %s from %s to %s\n", app.BinaryName, oldVersion, newVersion)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(relayCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(instructionsCmd)
	instructionsCmd.AddCommand(instructionsEditCmd)
	instructionsCmd.AddCommand(instructionsShowCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(updateCmd)

	relayCmd.Flags().Int("iterations", 1, "Number of iterations")
	relayCmd.Flags().StringArray("agent", nil, "Agent mix (repeatable; quoted lists allowed, e.g. \"cc:2 cx:1\")")
	relayCmd.Flags().Bool("resume", false, "Resume the last unfinished batch explicitly")
	relayCmd.Flags().Bool("new", false, "Start a new batch explicitly, discarding unfinished batch state")
}

func startBackgroundUpdateCheck(argv []string, stderr io.Writer) func() {
	if os.Getenv(app.EnvNoUpdateCheck) == "1" {
		return func() {}
	}
	if len(argv) > 0 && (argv[0] == "update" || argv[0] == "version" || argv[0] == "--version") {
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
