package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/app"
	rallyconfig "github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/rally/progress"
	"github.com/mitchell-wallace/rally/internal/rally/runner"
	"github.com/mitchell-wallace/rally/internal/rally/state"
	orchtui "github.com/mitchell-wallace/rally/internal/rally/tui"
	"github.com/mitchell-wallace/rally/internal/release"
)

var Version = "dev"

func main() {
	flushUpdateNotice := startBackgroundUpdateCheck(os.Args[1:], os.Stderr)
	if err := run(os.Args[1:]); err != nil {
		flushUpdateNotice()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	flushUpdateNotice()
}

func run(argv []string) error {
	if len(argv) == 0 {
		printUsage()
		return nil
	}
	switch argv[0] {
	case "tui":
		return runTUI(argv[1:])
	case "run":
		return runBatch(argv[1:])
	case "progress":
		return runProgress(argv[1:])
	case "instructions":
		return runInstructions(argv[1:])
	case "init":
		return runInit()
	case "update":
		return runUpdate()
	case "import-legacy":
		return runImportLegacy()
	case "version", "--version":
		fmt.Printf("%s %s\n", app.BinaryName, release.DisplayVersion(Version))
		return nil
	case "--help", "-h", "help":
		printUsage()
		return nil
	default:
		printUsage()
		return nil
	}
}

func printUsage() {
	fmt.Print(`rally - agent orchestrator

Commands:
  run [prompt...]          Run a batch of agent sessions
  tui                      Interactive terminal UI
  init                     Interactive setup wizard
  update                   Update Rally to the latest release
  instructions <cmd>       Manage project instructions (edit, show)
  progress <cmd>           Session progress (record, repair)
  version                  Print version

Flags for run/tui:
  --iterations N           Number of iterations (default: 1, scout: 5)
  --agent SPEC             Agent mix (repeatable; quoted lists allowed, e.g. "cc:2 cx:1")
  --beads [auto|true|false] Beads task source (default: from env/config)
  --resume                 Resume the last unfinished batch explicitly
  --new                    Start a new batch explicitly, discarding unfinished batch state
  --scout [focus]          Scout mode: explore, don't change code

Examples:
  rally run don't touch auth, just fix tests
  rally run --beads --iterations 3
  rally run --scout "error handling"
  rally tui --auto-start --beads auto
`)
}

type batchStartMode string

const (
	batchStartPrompt batchStartMode = ""
	batchStartResume batchStartMode = "resume"
	batchStartNew    batchStartMode = "new"
)

func runTUI(argv []string) error {
	cfg := defaultConfig()
	startMode := batchStartPrompt
	for len(argv) > 0 {
		switch argv[0] {
		case "--iterations":
			if len(argv) < 2 {
				return fmt.Errorf("missing value for --iterations")
			}
			n, err := strconv.Atoi(argv[1])
			if err != nil {
				return fmt.Errorf("invalid iterations: %w", err)
			}
			cfg.Iterations = n
			argv = argv[2:]
		case "--agent":
			if len(argv) < 2 {
				return fmt.Errorf("missing value for --agent")
			}
			var err error
			cfg.AgentSpecs, err = appendAgentSpecs(cfg.AgentSpecs, argv[1])
			if err != nil {
				return err
			}
			argv = argv[2:]
		case "--auto-start":
			cfg.AutoStart = true
			argv = argv[1:]
		case "--exit-when-idle":
			cfg.ExitWhenIdle = true
			argv = argv[1:]
		case "--beads":
			argv = argv[1:]
			cfg.BeadsMode = "true"
			if len(argv) > 0 && !strings.HasPrefix(argv[0], "--") {
				switch argv[0] {
				case "auto", "true", "false":
					cfg.BeadsMode = argv[0]
					argv = argv[1:]
				}
			}
		case "--resume":
			if startMode == batchStartNew {
				return fmt.Errorf("cannot use --resume and --new together")
			}
			startMode = batchStartResume
			argv = argv[1:]
		case "--new":
			if startMode == batchStartResume {
				return fmt.Errorf("cannot use --resume and --new together")
			}
			startMode = batchStartNew
			argv = argv[1:]
		default:
			return fmt.Errorf("unknown tui arg: %s", argv[0])
		}
	}
	if cfg.Iterations == 0 {
		cfg.Iterations = 1
	}
	if err := prepareBatchStart(cfg.DataDir, startMode, os.Stdin, os.Stdout); err != nil {
		return err
	}
	return orchtui.Run(cfg)
}

func runProgress(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("usage: rally progress <record|repair>")
	}
	switch argv[0] {
	case "record":
		return runProgressRecord()
	case "repair":
		return runProgressRepair()
	default:
		return fmt.Errorf("unknown progress command: %s", argv[0])
	}
}

func runBatch(argv []string) error {
	cfg := defaultConfig()
	startMode := batchStartPrompt
	var remaining []string
	beadsMode := cfg.BeadsMode
	scoutMode := false
	scoutFocus := ""
	iterationsExplicit := false
	for len(argv) > 0 {
		switch argv[0] {
		case "--iterations":
			if len(argv) < 2 {
				return fmt.Errorf("missing value for --iterations")
			}
			n, err := strconv.Atoi(argv[1])
			if err != nil {
				return fmt.Errorf("invalid iterations: %w", err)
			}
			cfg.Iterations = n
			iterationsExplicit = true
			argv = argv[2:]
		case "--agent":
			if len(argv) < 2 {
				return fmt.Errorf("missing value for --agent")
			}
			var err error
			cfg.AgentSpecs, err = appendAgentSpecs(cfg.AgentSpecs, argv[1])
			if err != nil {
				return err
			}
			argv = argv[2:]
		case "--beads":
			argv = argv[1:]
			beadsMode = "true"
			if len(argv) > 0 && !strings.HasPrefix(argv[0], "--") {
				switch argv[0] {
				case "auto", "true", "false":
					beadsMode = argv[0]
					argv = argv[1:]
				}
			}
		case "--scout":
			argv = argv[1:]
			scoutMode = true
			if len(argv) > 0 && !strings.HasPrefix(argv[0], "--") {
				scoutFocus = argv[0]
				argv = argv[1:]
			}
		case "--resume":
			if startMode == batchStartNew {
				return fmt.Errorf("cannot use --resume and --new together")
			}
			startMode = batchStartResume
			argv = argv[1:]
		case "--new":
			if startMode == batchStartResume {
				return fmt.Errorf("cannot use --resume and --new together")
			}
			startMode = batchStartNew
			argv = argv[1:]
		default:
			remaining = append(remaining, argv[0])
			argv = argv[1:]
		}
	}

	inlinePrompt := strings.Join(remaining, " ")

	iterations := cfg.Iterations
	if iterations == 0 {
		iterations = 1
	}
	if scoutMode && !iterationsExplicit {
		iterations = 5
	}
	if err := prepareBatchStart(cfg.DataDir, startMode, os.Stdin, os.Stdout); err != nil {
		return err
	}

	r := runner.New(runner.Config{
		WorkspaceDir:     cfg.WorkspaceDir,
		DataDir:          cfg.DataDir,
		RepoProgressPath: cfg.RepoProgressPath,
		Iterations:       iterations,
		AgentSpecs:       cfg.AgentSpecs,
		Stdout:           os.Stdout,
		Stderr:           os.Stderr,
		BeadsMode:        beadsMode,
		InlinePrompt:     inlinePrompt,
		ScoutMode:        scoutMode,
		ScoutFocus:       scoutFocus,
		ClaudeModel:      cfg.ClaudeModel,
		CodexModel:       cfg.CodexModel,
		GeminiModel:      cfg.GeminiModel,
		OpenCodeModel:    cfg.OpenCodeModel,
	})
	if err := r.EnsureInitialized(); err != nil {
		return err
	}
	_, err := r.Run(context.Background())
	return err
}

func runInstructions(argv []string) error {
	if len(argv) == 0 {
		fmt.Println("usage: rally instructions <edit|show>")
		return nil
	}
	cfg := defaultConfig()
	path := filepath.Join(cfg.DataDir, "instructions.md")

	switch argv[0] {
	case "edit":
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
		cmd := exec.Command(editor, path)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	case "show":
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
	default:
		return fmt.Errorf("unknown instructions command: %s", argv[0])
	}
}

func runInit() error {
	cfg := defaultConfig()
	scanner := bufio.NewScanner(os.Stdin)
	instructionsPath := filepath.Join(cfg.DataDir, "instructions.md")
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}

	var instructions []string

	// Question 1: Beads
	fmt.Print("Are you using beads for task tracking? [y/n/auto] ")
	beadsAnswer := "auto"
	if scanner.Scan() {
		switch strings.TrimSpace(strings.ToLower(scanner.Text())) {
		case "y", "yes", "true":
			beadsAnswer = "true"
		case "n", "no", "false":
			beadsAnswer = "false"
		default:
			beadsAnswer = "auto"
		}
	}
	fmt.Printf("  beads = %s\n", beadsAnswer)

	// Write beads setting to rally.toml if in a workspace.
	rallyTomlPath := rallyconfig.WorkspacePath(cfg.WorkspaceDir)
	if err := rallyconfig.Update(rallyTomlPath, func(fileCfg *rallyconfig.Config) {
		fileCfg.Beads = beadsAnswer
	}); err != nil {
		return err
	}

	// Question 2: Task source (if not beads)
	if beadsAnswer == "false" {
		fmt.Println("\nWhere should agents look for plans/specs?")
		fmt.Println("  1. Files (provide path)")
		fmt.Println("  2. MCP tool (describe)")
		fmt.Println("  3. CLI command (describe)")
		fmt.Println("  4. N/A")
		fmt.Print("Choice [1-4]: ")
		if scanner.Scan() {
			choice := strings.TrimSpace(scanner.Text())
			switch choice {
			case "1":
				fmt.Print("Path to plans/specs: ")
				if scanner.Scan() {
					path := strings.TrimSpace(scanner.Text())
					if path != "" {
						instructions = append(instructions, fmt.Sprintf("## Task Source\nLook for plans and specs in: %s", path))
					}
				}
			case "2":
				fmt.Print("Describe the MCP tool to use: ")
				if scanner.Scan() {
					desc := strings.TrimSpace(scanner.Text())
					if desc != "" {
						instructions = append(instructions, fmt.Sprintf("## Task Source\nUse the following MCP tool to find work: %s", desc))
					}
				}
			case "3":
				fmt.Print("CLI command to find tasks: ")
				if scanner.Scan() {
					cmd := strings.TrimSpace(scanner.Text())
					if cmd != "" {
						instructions = append(instructions, fmt.Sprintf("## Task Source\nRun this command to find available tasks: %s", cmd))
					}
				}
			}
		}
	}

	// Question 3: Priorities
	fmt.Print("\nWhat are your priorities for rally agents in review/scout mode?\n(free text, or press enter to skip): ")
	if scanner.Scan() {
		priorities := strings.TrimSpace(scanner.Text())
		if priorities != "" {
			instructions = append(instructions, fmt.Sprintf("## Agent Priorities\n%s", priorities))
		}
	}

	// Write instructions file
	if len(instructions) > 0 {
		content := "# Rally Project Instructions\n\n" + strings.Join(instructions, "\n\n") + "\n"
		if err := os.WriteFile(instructionsPath, []byte(content), 0o644); err != nil {
			return err
		}
		fmt.Printf("\nWrote instructions to %s\n", instructionsPath)
	}

	// Question 4: Scout
	fmt.Print("\nRun a scout session to prepare tasks for future sessions? [y/n] ")
	if scanner.Scan() {
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if answer == "y" || answer == "yes" {
			fmt.Println("Starting scout session (5 iterations)...")
			return runBatch([]string{"--scout", "--beads", beadsAnswer})
		}
	}

	fmt.Printf("\nWrote config to %s\n", rallyTomlPath)
	fmt.Println("Done! Run `rally run` to start an agent session.")
	return nil
}

func runUpdate() error {
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
}

func runProgressRecord() error {
	dataDir := getenvOr(app.EnvDataDir, app.ContainerDataRoot)
	repoPath := getenvOr(app.EnvRepoProgressPath, app.RepoProgressPath("/workspace"))
	sessionRaw := os.Getenv(app.EnvSessionID)
	if sessionRaw == "" {
		return fmt.Errorf("%s is required for progress record", app.EnvSessionID)
	}
	if stdinIsTerminal() {
		return fmt.Errorf("rally progress record reads YAML from stdin; pipe it in, for example:\ncat <<'YAML' | rally progress record\nsummary: what changed\nstatus: completed\nfiles_touched:\n  - path/to/file\ncommits:\n  - <commit-hash>\nYAML\nIf that still fails, add what you can directly to %s", repoPath)
	}
	sessionID, err := strconv.Atoi(sessionRaw)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "rally progress record: reading YAML from stdin for session %d\n", sessionID)
	input, err := progress.ParseRecordInput(os.Stdin)
	if err != nil {
		return fmt.Errorf("rally progress record: could not parse YAML input: %w\nIf this keeps failing, add what you can directly to %s", err, repoPath)
	}
	fmt.Fprintf(os.Stderr, "rally progress record: updating session metadata in %s\n", progress.SessionMetaPath(dataDir, sessionID))
	if err := progress.UpdateSessionMeta(dataDir, sessionID, func(meta *progress.SessionMeta) error {
		progress.ApplyRecord(meta, input)
		return nil
	}); err != nil {
		return fmt.Errorf("rally progress record: failed to update session metadata: %w\nIf this keeps failing, add what you can directly to %s", err, repoPath)
	}
	st, err := state.NewStore(dataDir).Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "rally progress record: rebuilding repo progress at %s\n", repoPath)
	_, err = progress.RebuildRepoProgress(dataDir, repoPath, activeBatchMap(st.ActiveBatch))
	if err != nil {
		return fmt.Errorf("rally progress record: failed to rebuild repo progress: %w\nIf this keeps failing, add what you can directly to %s", err, repoPath)
	}
	fmt.Fprintln(os.Stderr, "rally progress record: done")
	return nil
}

func runProgressRepair() error {
	cfg := defaultConfig()
	st, err := state.NewStore(cfg.DataDir).Load()
	if err != nil {
		return err
	}
	_, err = progress.RebuildRepoProgress(cfg.DataDir, cfg.RepoProgressPath, activeBatchMap(st.ActiveBatch))
	return err
}

func runImportLegacy() error {
	cfg := defaultConfig()
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	st := state.NewStore(cfg.DataDir)
	current, err := st.Load()
	if err != nil {
		return err
	}
	return st.Save(current)
}

func defaultConfig() orchtui.Config {
	containerName := getenvOr(app.EnvContainerName, "local")
	env := app.ContainerEnv(containerName)
	workspaceDir := getenvOr(app.EnvWorkspaceDir, defaultWorkspaceDir())
	dataDir := getenvOr(app.EnvDataDir, defaultDataDir(env[app.EnvDataDir]))
	repoPath := getenvOr(app.EnvRepoProgressPath, app.RepoProgressPath(workspaceDir))
	modelDefaults, _ := rallyconfig.LoadWorkspace(workspaceDir)
	beadsMode := getenvOr(app.EnvBeads, modelDefaults.Beads)
	return orchtui.Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		RepoProgressPath: repoPath,
		Iterations:       1,
		BeadsMode:        beadsMode,
		ClaudeModel:      modelDefaults.ClaudeModel,
		CodexModel:       modelDefaults.CodexModel,
		GeminiModel:      modelDefaults.GeminiModel,
		OpenCodeModel:    modelDefaults.OpenCodeModel,
	}
}

func defaultWorkspaceDir() string {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return "/workspace"
	}
	return wd
}

func defaultDataDir(containerDefault string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return containerDefault
	}
	return filepath.Join(home, ".local", "share", app.BinaryName)
}

func getenvOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func appendAgentSpecs(dst []string, value string) ([]string, error) {
	specs := strings.Fields(value)
	if len(specs) == 0 {
		return dst, fmt.Errorf("empty value for --agent")
	}
	return append(dst, specs...), nil
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func prepareBatchStart(dataDir string, mode batchStartMode, in io.Reader, out io.Writer) error {
	store := state.NewStore(dataDir)
	st, err := store.Load()
	if err != nil {
		return err
	}
	if st.ActiveBatch == nil {
		return nil
	}

	switch mode {
	case batchStartResume:
		return nil
	case batchStartNew:
		st.ActiveBatch = nil
		st.StopAfterCurrent = false
		return store.Save(st)
	}

	if !readerIsTerminal(in) || !writerIsTerminal(out) {
		return fmt.Errorf("an unfinished rally batch exists; rerun with --resume to continue it or --new to start a fresh batch")
	}

	reader := bufio.NewReader(in)
	for {
		fmt.Fprintf(out, "rally: unfinished batch #%d is at iteration %d/%d. Resume or start a new batch? [resume/new]: ",
			st.ActiveBatch.BatchID, st.ActiveBatch.CompletedIterations, st.ActiveBatch.TargetIterations)
		answer, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "resume", "r":
			return nil
		case "new", "n":
			st.ActiveBatch = nil
			st.StopAfterCurrent = false
			return store.Save(st)
		}
	}
}

func readerIsTerminal(r io.Reader) bool {
	file, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func writerIsTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func activeBatchMap(batch *state.BatchState) map[string]any {
	if batch == nil {
		return nil
	}
	return map[string]any{
		"batch_id":             batch.BatchID,
		"target_iterations":    batch.TargetIterations,
		"completed_iterations": batch.CompletedIterations,
		"agent_mix":            batch.AgentMix,
		"started_at":           batch.StartedAt,
		"ended_at":             batch.EndedAt,
	}
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
			msg = fmt.Sprintf("unable to check for updates: %s", err)
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
