package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/rally/messages"
	"github.com/mitchell-wallace/rally/internal/rally/progress"
	"github.com/mitchell-wallace/rally/internal/rally/prompt"
	"github.com/mitchell-wallace/rally/internal/rally/state"
)

type AgentMix struct {
	Weights map[string]int
	Order   []string
	Cycle   []string
	Label   string
}

type Config struct {
	WorkspaceDir         string
	DataDir              string
	RepoProgressPath     string
	AgentSpecs           []string
	Iterations           int
	Stdout               io.Writer
	Stderr               io.Writer
	BeadsMode            string // "auto", "true", "false", or "" (use env default)
	InlinePrompt         string
	ScoutMode            bool
	ScoutFocus           string
	ClaudeModel          string
	CodexModel           string
	GeminiModel          string
	OpenCodeModel        string
	RunHooksOnAutoCommit bool
}

const defaultScoutIterations = 5
const opencodePermissionYolo = `{"*":"allow"}`
const repoBatchLogCacheLimit = 10

type Runner struct {
	cfg          Config
	stateStore   *state.Store
	messageStore *messages.Store
	beadsCache   *bool // cached result of detectBeads
}

type SessionResult struct {
	SessionID      int
	BatchID        int
	IterationIndex int
	Agent          string
	ExitCode       int
}

type geminiHeadlessOutput struct {
	Response string `json:"response"`
}

type claudeStreamEvent struct {
	Type   string `json:"type"`
	Result string `json:"result"`
}

type opencodeJSONEvent struct {
	Type string `json:"type"`
	Part struct {
		Type      string `json:"type"`
		MessageID string `json:"messageID"`
		Text      string `json:"text"`
	} `json:"part"`
}

func New(cfg Config) *Runner {
	return &Runner{
		cfg:          cfg,
		stateStore:   state.NewStore(cfg.DataDir),
		messageStore: messages.NewStore(cfg.DataDir),
	}
}

func ParseAgentMix(specs []string) (AgentMix, error) {
	weights := map[string]int{"claude": 0, "codex": 0, "gemini": 0, "opencode": 0}
	order := []string{}
	addWeight := func(agent string, amount int) error {
		if amount < 1 {
			return fmt.Errorf("agent weight must be >= 1")
		}
		if weights[agent] == 0 {
			order = append(order, agent)
		}
		weights[agent] += amount
		return nil
	}

	if len(specs) == 0 {
		_ = addWeight("claude", 1)
		_ = addWeight("codex", 2)
	} else {
		aliases := map[string]string{
			"cc": "claude", "claude": "claude",
			"cx": "codex", "codex": "codex",
			"ge": "gemini", "gemini": "gemini",
			"op": "opencode", "opencode": "opencode",
		}
		for _, spec := range specs {
			parts := strings.SplitN(spec, ":", 2)
			agent, ok := aliases[parts[0]]
			if !ok {
				return AgentMix{}, fmt.Errorf("unknown agent alias %q", parts[0])
			}
			weight := 1
			if len(parts) == 2 {
				n, err := strconv.Atoi(parts[1])
				if err != nil || n < 1 {
					return AgentMix{}, fmt.Errorf("invalid agent weight %q", spec)
				}
				weight = n
			}
			if err := addWeight(agent, weight); err != nil {
				return AgentMix{}, err
			}
		}
	}

	cycle := []string{}
	labelParts := []string{}
	for _, agent := range order {
		for i := 0; i < weights[agent]; i++ {
			cycle = append(cycle, agent)
		}
		labelParts = append(labelParts, fmt.Sprintf("%s:%d", agent, weights[agent]))
	}
	return AgentMix{
		Weights: weights,
		Order:   order,
		Cycle:   cycle,
		Label:   strings.Join(labelParts, " "),
	}, nil
}

func AgentForSession(sessionID int, mix AgentMix) string {
	if len(mix.Cycle) == 0 {
		return "claude"
	}
	return mix.Cycle[(sessionID-1)%len(mix.Cycle)]
}

func BuildAgentCommand(cfg Config, agentName, prompt string) ([]string, bool, error) {
	switch agentName {
	case "claude":
		command := []string{"claude", "-p", "--dangerously-skip-permissions"}
		if cfg.ClaudeModel != "" {
			command = append(command, "--model", cfg.ClaudeModel)
		}
		command = append(command, "--output-format", "stream-json", "--verbose", prompt)
		return command, true, nil
	case "codex":
		command := []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox"}
		if cfg.CodexModel != "" {
			command = append(command, "--model", cfg.CodexModel)
		}
		command = append(command, prompt)
		return command, true, nil
	case "gemini":
		command := []string{"gemini"}
		if cfg.GeminiModel != "" {
			command = append(command, "--model", cfg.GeminiModel)
		}
		command = append(command, "--prompt", prompt, "--yolo", "--output-format", "json")
		return command, true, nil
	case "opencode":
		command := []string{"opencode", "run"}
		if cfg.OpenCodeModel != "" {
			command = append(command, "--model", cfg.OpenCodeModel)
		}
		command = append(command, "--format", "json", prompt)
		return command, true, nil
	default:
		return nil, false, fmt.Errorf("unsupported agent %q", agentName)
	}
}

func AgentEnvOverrides(agentName string) []string {
	switch agentName {
	case "opencode":
		return []string{"OPENCODE_PERMISSION=" + opencodePermissionYolo}
	default:
		return nil
	}
}

func (r *Runner) EnsureInitialized() error {
	st, err := r.stateStore.Load()
	if err != nil {
		return err
	}
	return r.stateStore.Save(st)
}

func (r *Runner) StartOrResumeBatch(iterations int) (state.State, error) {
	st, err := r.stateStore.Load()
	if err != nil {
		return state.State{}, err
	}
	mix, err := ParseAgentMix(r.cfg.AgentSpecs)
	if err != nil {
		return state.State{}, err
	}
	// Finalize stale batches that already completed all iterations (e.g. from
	// a previous run that was killed after incrementing CompletedIterations but
	// before cleaning up ActiveBatch).
	if st.ActiveBatch != nil && st.ActiveBatch.CompletedIterations >= st.ActiveBatch.TargetIterations {
		if st.ActiveBatch.EndedAt == "" {
			st.ActiveBatch.EndedAt = time.Now().UTC().Format(time.RFC3339)
		}
		st.ActiveBatch = nil
		st.StopAfterCurrent = false
	}
	if st.ActiveBatch == nil {
		st.ActiveBatch = &state.BatchState{
			BatchID:          st.NextBatchID,
			TargetIterations: iterations,
			AgentMix:         append([]string{}, r.cfg.AgentSpecs...),
			StartedAt:        time.Now().UTC().Format(time.RFC3339),
		}
		st.NextBatchID++
	} else {
		if iterations > 0 && iterations < st.ActiveBatch.CompletedIterations {
			iterations = st.ActiveBatch.CompletedIterations
		}
		if iterations > 0 {
			st.ActiveBatch.TargetIterations = iterations
		}
	}
	if len(st.ActiveBatch.AgentMix) == 0 {
		st.ActiveBatch.AgentMix = mix.Order
	}
	if err := r.stateStore.Save(st); err != nil {
		return state.State{}, err
	}
	return st, nil
}

func (r *Runner) RequestStopAfterCurrent() error {
	st, err := r.stateStore.Load()
	if err != nil {
		return err
	}
	st.StopAfterCurrent = true
	return r.stateStore.Save(st)
}

func (r *Runner) ResizeBatch(target int) error {
	st, err := r.stateStore.Load()
	if err != nil {
		return err
	}
	if st.ActiveBatch == nil {
		return nil
	}
	if target < st.ActiveBatch.CompletedIterations {
		target = st.ActiveBatch.CompletedIterations
	}
	st.ActiveBatch.TargetIterations = target
	return r.stateStore.Save(st)
}

func (r *Runner) Run(ctx context.Context) ([]SessionResult, error) {
	if err := os.MkdirAll(r.cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	iterations := r.cfg.Iterations
	if r.cfg.ScoutMode && iterations <= 1 {
		iterations = defaultScoutIterations
	}
	st, err := r.StartOrResumeBatch(iterations)
	if err != nil {
		return nil, err
	}
	mix, err := ParseAgentMix(r.cfg.AgentSpecs)
	if err != nil {
		return nil, err
	}

	if err := ensureRepoBatchLogsIgnored(r.cfg.WorkspaceDir); err != nil {
		fmt.Fprintf(r.cfg.Stderr, "rally: batch log ignore warning: %v\n", err)
	}
	batchLog, err := openBatchLog(r.cfg.DataDir, r.cfg.WorkspaceDir, st.ActiveBatch.BatchID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = batchLog.Close()
		_ = pruneRepoBatchLogs(r.cfg.WorkspaceDir, repoBatchLogCacheLimit)
	}()

	writeConsoleAndBatch(r.cfg.Stderr, batchLog, "rally: batch %d — %d iteration(s), agents: %s\n",
		st.ActiveBatch.BatchID, st.ActiveBatch.TargetIterations, mix.Label)
	if st.ActiveBatch.CompletedIterations > 0 {
		writeConsoleAndBatch(r.cfg.Stderr, batchLog, "rally: resuming from iteration %d\n", st.ActiveBatch.CompletedIterations+1)
	}

	var results []SessionResult
	for st.ActiveBatch != nil && st.ActiveBatch.CompletedIterations < st.ActiveBatch.TargetIterations {
		if ctx.Err() != nil {
			writeConsoleAndBatch(r.cfg.Stderr, batchLog, "rally: cancelled after %d iteration(s)\n", len(results))
			return results, ctx.Err()
		}
		current, err := r.runOne(ctx, &st, mix, batchLog)
		if err != nil {
			writeConsoleAndBatch(r.cfg.Stderr, batchLog, "rally: iteration %d failed: %v\n", current.IterationIndex, err)
			return results, err
		}
		results = append(results, current)
		st, err = r.stateStore.Load()
		if err != nil {
			return results, err
		}
		if st.StopAfterCurrent {
			writeConsoleAndBatch(r.cfg.Stderr, batchLog, "rally: stop requested after iteration %d\n", current.IterationIndex)
			break
		}
	}
	writeConsoleAndBatch(r.cfg.Stderr, batchLog, "rally: batch %d complete — %d session(s) ran\n", st.NextBatchID-1, len(results))
	return results, nil
}

func (r *Runner) runOne(ctx context.Context, st *state.State, mix AgentMix, batchLog io.Writer) (SessionResult, error) {
	sessionID := st.NextSessionID
	st.NextSessionID++
	st.ActiveBatch.CompletedIterations++
	iterationIndex := st.ActiveBatch.CompletedIterations
	agent := AgentForSession(sessionID, mix)
	startedAt := time.Now().UTC()

	writeConsoleAndBatch(r.cfg.Stderr, batchLog, "rally: [%d/%d] session %d — agent: %s\n",
		iterationIndex, st.ActiveBatch.TargetIterations, sessionID, agent)

	if err := r.stateStore.Save(*st); err != nil {
		return SessionResult{}, err
	}

	sessionDir, err := progress.EnsureSessionDir(r.cfg.DataDir, sessionID)
	if err != nil {
		return SessionResult{}, err
	}
	transcriptPath := progress.TranscriptPath(r.cfg.DataDir, sessionID)
	logFile, err := os.OpenFile(transcriptPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return SessionResult{}, err
	}
	defer logFile.Close()

	messageIDs, promptBody, err := r.buildPrompt(st.ActiveBatch.BatchID, sessionID, iterationIndex, st.ActiveBatch.TargetIterations, agent)
	if err != nil {
		return SessionResult{}, err
	}

	cmdArgs, suppressStderr, err := BuildAgentCommand(r.cfg, agent, promptBody)
	if err != nil {
		return SessionResult{}, err
	}
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = r.cfg.WorkspaceDir
	cmd.Env = append(os.Environ(),
		app.EnvDataDir+"="+r.cfg.DataDir,
		app.EnvRepoProgressPath+"="+r.cfg.RepoProgressPath,
		app.EnvWorkspaceDir+"="+r.cfg.WorkspaceDir,
		app.EnvSessionID+"="+strconv.Itoa(sessionID),
		app.EnvBatchID+"="+strconv.Itoa(st.ActiveBatch.BatchID),
		app.EnvIterationIndex+"="+strconv.Itoa(iterationIndex),
		app.EnvAgent+"="+agent,
		app.EnvSessionDir+"="+sessionDir,
	)
	cmd.Env = append(cmd.Env, AgentEnvOverrides(agent)...)

	agentLog := logFile
	filteredOutput := io.MultiWriter(batchLog, r.cfg.Stdout)
	stdout := io.MultiWriter(logFile, batchLog, r.cfg.Stdout)
	stderrTarget := io.MultiWriter(logFile, r.cfg.Stderr)
	var claudeStdout bytes.Buffer
	var geminiStdout bytes.Buffer
	var opencodeStdout bytes.Buffer
	if agent == "claude" {
		cmd.Stdout = &claudeStdout
	} else if agent == "gemini" {
		cmd.Stdout = &geminiStdout
	} else if agent == "opencode" {
		cmd.Stdout = &opencodeStdout
	} else {
		cmd.Stdout = stdout
	}
	if suppressStderr {
		cmd.Stderr = agentLog
	} else {
		cmd.Stderr = stderrTarget
	}

	sessionMeta := progress.SessionMeta{
		Version: app.SchemaVersion,
		Session: progress.SessionProgress{
			SessionID:      sessionID,
			BatchID:        st.ActiveBatch.BatchID,
			IterationIndex: iterationIndex,
			Agent:          agent,
			Status:         "running",
			StartedAt:      startedAt.Format(time.RFC3339),
			MessageIDs:     messageIDs,
			TranscriptPath: transcriptPath,
		},
	}
	if err := progress.WriteSessionMeta(progress.SessionMetaPath(r.cfg.DataDir, sessionID), sessionMeta); err != nil {
		return SessionResult{}, err
	}

	runErr := cmd.Run()
	if agent == "claude" {
		if _, writeErr := logFile.Write(claudeStdout.Bytes()); writeErr != nil && runErr == nil {
			runErr = writeErr
		}
		formatted, err := formatClaudeStreamJSONResponse(claudeStdout.Bytes())
		if err != nil {
			fmt.Fprintf(agentLog, "rally: warning: failed to parse Claude JSON output: %v\n", err)
			if _, writeErr := filteredOutput.Write(claudeStdout.Bytes()); writeErr != nil && runErr == nil {
				runErr = writeErr
			}
		} else if _, writeErr := filteredOutput.Write(formatted); writeErr != nil && runErr == nil {
			runErr = writeErr
		}
	} else if agent == "gemini" {
		if _, writeErr := logFile.Write(geminiStdout.Bytes()); writeErr != nil && runErr == nil {
			runErr = writeErr
		}
		formatted, err := formatGeminiHeadlessResponse(geminiStdout.Bytes())
		if err != nil {
			fmt.Fprintf(agentLog, "rally: warning: failed to parse Gemini JSON output: %v\n", err)
			if _, writeErr := filteredOutput.Write(geminiStdout.Bytes()); writeErr != nil && runErr == nil {
				runErr = writeErr
			}
		} else if _, writeErr := filteredOutput.Write(formatted); writeErr != nil && runErr == nil {
			runErr = writeErr
		}
	} else if agent == "opencode" {
		if _, writeErr := logFile.Write(opencodeStdout.Bytes()); writeErr != nil && runErr == nil {
			runErr = writeErr
		}
		formatted, err := formatOpenCodeJSONResponse(opencodeStdout.Bytes())
		if err != nil {
			fmt.Fprintf(agentLog, "rally: warning: failed to parse Opencode JSON output: %v\n", err)
			if _, writeErr := filteredOutput.Write(opencodeStdout.Bytes()); writeErr != nil && runErr == nil {
				runErr = writeErr
			}
		} else if _, writeErr := filteredOutput.Write(formatted); writeErr != nil && runErr == nil {
			runErr = writeErr
		}
	}
	endedAt := time.Now().UTC()
	exitCode := 0
	status := "completed"
	if runErr != nil {
		status = "failed"
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	runtimeSeconds := int(endedAt.Sub(startedAt).Seconds())
	writeConsoleAndBatch(r.cfg.Stderr, batchLog, "rally: [%d/%d] session %d %s (exit %d, %ds)\n",
		iterationIndex, st.ActiveBatch.TargetIterations, sessionID, status, exitCode, runtimeSeconds)
	if err := progress.UpdateSessionMeta(r.cfg.DataDir, sessionID, func(meta *progress.SessionMeta) error {
		meta.Session.Status = status
		meta.Session.EndedAt = endedAt.Format(time.RFC3339)
		meta.Session.RuntimeSeconds = runtimeSeconds
		return nil
	}); err != nil {
		return SessionResult{}, err
	}

	if st.StopAfterCurrent || st.ActiveBatch.CompletedIterations >= st.ActiveBatch.TargetIterations {
		st.ActiveBatch.EndedAt = endedAt.Format(time.RFC3339)
		st.ActiveBatch = nil
		st.StopAfterCurrent = false
	}
	if err := r.stateStore.Save(*st); err != nil {
		return SessionResult{}, err
	}
	if _, err := progress.RebuildRepoProgress(r.cfg.DataDir, r.cfg.RepoProgressPath, activeBatchMap(st.ActiveBatch)); err != nil {
		return SessionResult{}, err
	}
	if commitHash, err := autoCommitWorkspace(r.cfg.WorkspaceDir, sessionID, iterationIndex, agent, r.cfg.RunHooksOnAutoCommit); err != nil {
		writeConsoleAndBatch(r.cfg.Stderr, batchLog, "rally: session %d auto-commit warning: %v\n", sessionID, err)
	} else if commitHash != "" {
		writeConsoleAndBatch(r.cfg.Stderr, batchLog, "rally: session %d auto-committed workspace changes (%s)\n", sessionID, commitHash)
	}

	return SessionResult{
		SessionID:      sessionID,
		BatchID:        sessionMeta.Session.BatchID,
		IterationIndex: iterationIndex,
		Agent:          agent,
		ExitCode:       exitCode,
	}, runErr
}

func formatGeminiHeadlessResponse(raw []byte) ([]byte, error) {
	var payload geminiHeadlessOutput
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	response := strings.TrimSpace(payload.Response)
	if response == "" {
		return nil, fmt.Errorf("missing response field")
	}
	return []byte(response + "\n"), nil
}

func formatClaudeStreamJSONResponse(raw []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var response string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event claudeStreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, err
		}
		if event.Type == "result" {
			response = event.Result
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	response = strings.TrimSpace(response)
	if response == "" {
		return nil, fmt.Errorf("missing result event")
	}
	return []byte(response + "\n"), nil
}

func formatOpenCodeJSONResponse(raw []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var lastMessageID string
	var textParts []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event opencodeJSONEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, err
		}
		if event.Type != "text" && event.Part.Type != "text" {
			continue
		}
		if event.Part.Text == "" {
			continue
		}
		if event.Part.MessageID != "" && event.Part.MessageID != lastMessageID {
			lastMessageID = event.Part.MessageID
			textParts = textParts[:0]
		}
		textParts = append(textParts, event.Part.Text)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	response := strings.TrimSpace(strings.Join(textParts, ""))
	if response == "" {
		return nil, fmt.Errorf("missing text event")
	}
	return []byte(response + "\n"), nil
}

type batchLog struct {
	files  []*os.File
	writer io.Writer
}

func (l *batchLog) Write(p []byte) (int, error) {
	return l.writer.Write(p)
}

func (l *batchLog) Close() error {
	var closeErr error
	for _, file := range l.files {
		if err := file.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func openBatchLog(dataDir, workspaceDir string, batchID int) (*batchLog, error) {
	paths := []string{
		BatchLogPath(dataDir, batchID),
		RepoBatchLogPath(workspaceDir, batchID),
	}
	var files []*os.File
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			for _, opened := range files {
				_ = opened.Close()
			}
			return nil, err
		}
		files = append(files, file)
	}
	writers := make([]io.Writer, 0, len(files))
	for _, file := range files {
		writers = append(writers, file)
	}
	return &batchLog{files: files, writer: io.MultiWriter(writers...)}, nil
}

func BatchLogPath(dataDir string, batchID int) string {
	return filepath.Join(dataDir, "batches", fmt.Sprintf("batch-%d.log", batchID))
}

func RepoBatchLogPath(workspaceDir string, batchID int) string {
	return filepath.Join(workspaceDir, ".rally", "batches", fmt.Sprintf("batch-%d.log", batchID))
}

func pruneRepoBatchLogs(workspaceDir string, keep int) error {
	dir := filepath.Join(workspaceDir, ".rally", "batches")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var logs []os.DirEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "batch-") || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		logs = append(logs, entry)
	}
	if len(logs) <= keep {
		return nil
	}
	sort.Slice(logs, func(i, j int) bool {
		return batchLogID(logs[i].Name()) < batchLogID(logs[j].Name())
	})
	for _, entry := range logs[:len(logs)-keep] {
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func batchLogID(name string) int {
	value := strings.TrimSuffix(strings.TrimPrefix(name, "batch-"), ".log")
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return n
}

func ensureRepoBatchLogsIgnored(workspaceDir string) error {
	repoRoot, ok, err := gitRepoRoot(workspaceDir)
	if err != nil || !ok {
		return err
	}

	checkCmd := exec.Command("git", "-C", repoRoot, "check-ignore", "-q", "--", ".rally/batches/.rally-keep")
	if err := checkCmd.Run(); err == nil {
		return nil
	} else {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return gitCommandError([]string{"check-ignore", "-q", "--", ".rally/batches/.rally-keep"}, nil, err)
		}
	}

	pathOutput, err := gitOutput(repoRoot, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	excludePath := strings.TrimSpace(string(pathOutput))
	if excludePath == "" {
		return nil
	}
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(repoRoot, excludePath)
	}

	data, err := os.ReadFile(excludePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if strings.Contains(string(data), ".rally/batches/") || strings.Contains(string(data), "/.rally/batches/") {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return err
	}

	var builder strings.Builder
	if len(data) > 0 {
		builder.Write(data)
		if !strings.HasSuffix(string(data), "\n") {
			builder.WriteByte('\n')
		}
	}
	builder.WriteString("# Rally runtime batch log cache.\n")
	builder.WriteString(".rally/batches/\n")
	return os.WriteFile(excludePath, []byte(builder.String()), 0o644)
}

func writeConsoleAndBatch(console io.Writer, batchLog io.Writer, format string, args ...any) {
	if batchLog == nil {
		fmt.Fprintf(console, format, args...)
		return
	}
	fmt.Fprintf(io.MultiWriter(console, batchLog), format, args...)
}

func (r *Runner) detectBeads() bool {
	if r.beadsCache != nil {
		return *r.beadsCache
	}
	result := false
	switch r.cfg.BeadsMode {
	case "true":
		result = true
	case "false", "":
		result = false
	case "auto":
		cmd := exec.Command("bd", "ready", "--json", "--limit", "1")
		cmd.Dir = r.cfg.WorkspaceDir
		result = cmd.Run() == nil
	}
	r.beadsCache = &result
	return result
}

func (r *Runner) buildPrompt(batchID, sessionID, iterationIndex, targetIterations int, agent string) ([]int, string, error) {
	var batchBodies []string
	var sessionBody string
	var consumed []int

	// When an inline prompt is provided, use it exclusively and skip
	// the message store entirely.
	if r.cfg.InlinePrompt != "" {
		batchBodies = []string{r.cfg.InlinePrompt}
	} else {
		events, err := r.messageStore.Load()
		if err != nil {
			return nil, "", err
		}
		folded := messages.Fold(events)
		ordered := messages.OrderedMessages(folded)

		st, err := r.stateStore.Load()
		if err != nil {
			return nil, "", err
		}

		for _, msg := range ordered {
			switch msg.Scope {
			case messages.ScopeBatch:
				if msg.ApplyBatchID != nil && *msg.ApplyBatchID == batchID && !msg.Canceled {
					batchBodies = append(batchBodies, msg.Body)
					continue
				}
				if !msg.Pending() {
					continue
				}
				target := 0
				if msg.TargetBatchID != nil {
					target = *msg.TargetBatchID
				}
				if target == 0 || target == batchID {
					batchBodies = append(batchBodies, msg.Body)
					applyBatchID := batchID
					if err := r.messageStore.Append(messages.Event{
						EventID:      st.NextEventID,
						MessageID:    msg.MessageID,
						Scope:        messages.ScopeBatch,
						EventType:    messages.EventMessageConsumed,
						ConsumedAt:   messages.Timestamp(),
						ApplyBatchID: &applyBatchID,
					}); err != nil {
						return nil, "", err
					}
					st.NextEventID++
					consumed = append(consumed, msg.MessageID)
				}
			case messages.ScopeSession:
				if !msg.Pending() {
					continue
				}
				if sessionBody == "" {
					sessionBody = msg.Body
					targetSessionID := sessionID
					if err := r.messageStore.Append(messages.Event{
						EventID:         st.NextEventID,
						MessageID:       msg.MessageID,
						Scope:           messages.ScopeSession,
						EventType:       messages.EventMessageConsumed,
						ConsumedAt:      messages.Timestamp(),
						TargetSessionID: &targetSessionID,
					}); err != nil {
						return nil, "", err
					}
					st.NextEventID++
					consumed = append(consumed, msg.MessageID)
				}
			}
		}
		if err := r.stateStore.Save(st); err != nil {
			return nil, "", err
		}
	}

	data := prompt.PromptData{
		SessionID:           sessionID,
		BatchID:             batchID,
		IterationIndex:      iterationIndex,
		TargetIterations:    targetIterations,
		Agent:               agent,
		BeadsEnabled:        r.detectBeads(),
		ScoutMode:           r.cfg.ScoutMode,
		ScoutFocus:          r.cfg.ScoutFocus,
		ProjectInstructions: prompt.LoadProjectInstructions(r.cfg.DataDir),
		BatchMessages:       batchBodies,
		SessionDirective:    sessionBody,
		RepoProgressPath:    r.cfg.RepoProgressPath,
	}

	body, err := prompt.Build(data)
	if err != nil {
		return nil, "", err
	}
	return consumed, body, nil
}

func autoCommitWorkspace(workspaceDir string, sessionID, iterationIndex int, agent string, runHooks bool) (string, error) {
	repoRoot, ok, err := gitRepoRoot(workspaceDir)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}

	statusOutput, err := gitOutput(repoRoot, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(statusOutput)) == "" {
		return "", nil
	}

	if _, err := gitOutput(repoRoot, "add", "-A"); err != nil {
		return "", err
	}

	diffOutput, err := gitOutput(repoRoot, "diff", "--cached", "--quiet")
	if err == nil {
		statusOutput, statusErr := gitOutput(repoRoot, "status", "--porcelain")
		if statusErr != nil {
			return "", statusErr
		}
		if strings.TrimSpace(string(statusOutput)) != "" {
			return "", fmt.Errorf("workspace is dirty, but git add -A did not stage any committable changes:\n%s", strings.TrimSpace(string(statusOutput)))
		}
		return "", nil
	} else {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return "", gitCommandError([]string{"diff", "--cached", "--quiet"}, diffOutput, err)
		}
	}

	commitArgs := append(gitUserFallbackConfig(repoRoot), "commit")
	if !runHooks {
		commitArgs = append(commitArgs, "--no-verify")
	}
	commitArgs = append(commitArgs, "-m", fmt.Sprintf("rally: session %d iteration %d (%s)", sessionID, iterationIndex, agent))
	if _, err := gitOutput(repoRoot, commitArgs...); err != nil {
		return "", err
	}

	hashOutput, err := gitOutput(repoRoot, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(hashOutput)), nil
}

func gitRepoRoot(workspaceDir string) (string, bool, error) {
	output, err := gitOutput(workspaceDir, "rev-parse", "--show-toplevel")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", false, nil
		}
		return "", false, err
	}
	repoRoot := strings.TrimSpace(string(output))
	if repoRoot == "" {
		return "", false, nil
	}
	return repoRoot, true, nil
}

func gitUserFallbackConfig(repoRoot string) []string {
	var args []string
	if value, err := gitOutput(repoRoot, "config", "--get", "user.name"); err != nil || strings.TrimSpace(string(value)) == "" {
		args = append(args, "-c", "user.name=Rally")
	}
	if value, err := gitOutput(repoRoot, "config", "--get", "user.email"); err != nil || strings.TrimSpace(string(value)) == "" {
		args = append(args, "-c", "user.email=rally@localhost")
	}
	return args
}

func gitOutput(repoRoot string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, gitCommandError(args, output, err)
	}
	return output, nil
}

func gitCommandError(args []string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, detail)
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
