package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/mitchell-wallace/rally/internal/reliability"
)

type CodexExecutor struct {
	Model string

	mu              sync.RWMutex
	activeSessionID string
}

type codexJSONEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id,omitempty"`
	Item     *struct {
		Type string `json:"type"`
	} `json:"item,omitempty"`
}

// codexToolItemTypes lists the item.type values that represent tool
// invocations in codex's --json event stream.
var codexToolItemTypes = map[string]bool{
	"command_execution": true,
	"file_change":       true,
	"web_search":        true,
	"mcp_tool_call":     true,
}

func writeCodexSchema() (string, error) {
	f, err := os.CreateTemp("", "codex-schema-*.json")
	if err != nil {
		return "", err
	}
	schema := `{"type":"object","additionalProperties":false,"required":["completed","summary","remaining_work","message_addressed","files_changed"],"properties":{"completed":{"type":"boolean"},"summary":{"type":"string"},"remaining_work":{"type":"string"},"message_addressed":{"type":["boolean","null"]},"files_changed":{"type":["array","null"],"items":{"type":"string"}}}}`
	if _, err := f.WriteString(schema); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

func parseCodexResult(reportData []byte) (*TryResult, error) {
	var tr TryResult
	if err := json.Unmarshal(reportData, &tr); err != nil {
		return &TryResult{Completed: true, Summary: string(reportData)}, nil
	}
	return &tr, nil
}

func scanCodexSessionID(out []byte) string {
	sessionID, _ := scanCodexEvents(out)
	return sessionID
}

// scanCodexEvents walks the codex --json event stream and returns the session
// id (from the first `thread.started` event) and the count of tool invocations
// (item.completed events where item.type is a known tool type).
func scanCodexEvents(out []byte) (sessionID string, toolCalls int) {
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev codexJSONEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == "thread.started" && ev.ThreadID != "" && sessionID == "" {
			sessionID = ev.ThreadID
		}
		if ev.Type == "item.completed" && ev.Item != nil && codexToolItemTypes[ev.Item.Type] {
			toolCalls++
		}
	}
	return sessionID, toolCalls
}

func (c *CodexExecutor) setActiveSessionID(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.activeSessionID = sessionID
}

func (c *CodexExecutor) currentSessionID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.activeSessionID
}

func (c *CodexExecutor) ResumeSupported() bool        { return true }
func (c *CodexExecutor) RotateSupported() bool        { return false }
func (c *CodexExecutor) LivenessProbeSupported() bool { return true }
func (c *CodexExecutor) RotateModel(string) error {
	return fmt.Errorf("rotate not supported by codex adapter")
}
func (c *CodexExecutor) ProbeLiveness(ctx context.Context) (bool, error) {
	sessionID := c.currentSessionID()
	if sessionID == "" {
		return false, fmt.Errorf("codex probe missing session id")
	}

	reportFile, err := os.CreateTemp("", "codex-probe-*.txt")
	if err != nil {
		return false, fmt.Errorf("codex probe temp file: %w", err)
	}
	reportPath := reportFile.Name()
	reportFile.Close()
	defer os.Remove(reportPath)

	args := []string{
		"exec", "resume",
		"--json",
		"--dangerously-bypass-approvals-and-sandbox",
		"-o", reportPath,
	}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	args = append(args, sessionID, "Respond with exactly OK.")

	cmd := exec.CommandContext(ctx, "codex", args...)
	SetProcessGroup(cmd)
	out, err := runLoggedCommand(cmd, "", true, nil)
	if err != nil {
		return false, fmt.Errorf("codex probe failed: %w\noutput: %s", err, string(out))
	}

	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		return false, fmt.Errorf("codex probe read failed: %w", err)
	}

	return strings.TrimSpace(string(reportData)) == "OK", nil
}

func (c *CodexExecutor) Execute(ctx context.Context, opts RunOptions) (*TryResult, error) {
	prompt := BuildPrompt(opts)
	c.setActiveSessionID(opts.ResumeSessionID)

	schemaPath, err := writeCodexSchema()
	if err != nil {
		return nil, fmt.Errorf("codex schema write failed: %w", err)
	}
	defer os.Remove(schemaPath)

	reportFile, err := os.CreateTemp("", "codex-report-*.json")
	if err != nil {
		return nil, fmt.Errorf("codex report temp file: %w", err)
	}
	reportPath := reportFile.Name()
	reportFile.Close()

	model := c.Model
	if opts.Model != "" {
		model = opts.Model
	}

	args := []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--json"}
	if model != "" {
		args = append(args, "--model", model)
	}
	if opts.ReasoningEffort != "" {
		var warning string
		args, warning = applyReasoningEffort(args, "codex", opts.ReasoningEffort)
		emitReasoningWarning(opts.LogPath, warning)
	}
	if opts.ResumeSessionID != "" {
		args = append(args, "resume", opts.ResumeSessionID)
	}
	args = append(args, "--output-schema", schemaPath, "-o", reportPath, prompt)

	cmd := exec.CommandContext(ctx, "codex", args...)
	if opts.WorkspaceDir != "" {
		cmd.Dir = opts.WorkspaceDir
	}
	SetProcessGroup(cmd)
	out, err := runCodexCommand(cmd, opts.LogPath, opts.OnStart, c.setActiveSessionID)
	if err != nil {
		os.Remove(reportPath)
		execErr := fmt.Errorf("codex exec failed: %w\noutput: %s", err, string(out))
		if ev := reliability.ParseCodexError(string(out)); ev != nil {
			return &TryResult{Evidence: ev}, execErr
		}
		return nil, execErr
	}

	reportData, err := os.ReadFile(reportPath)
	os.Remove(reportPath)
	if err != nil {
		return nil, fmt.Errorf("codex report read failed: %w\noutput: %s", err, string(out))
	}

	tr, err := parseCodexResult(reportData)
	if err != nil {
		return nil, err
	}
	streamSessionID, toolCalls := scanCodexEvents(out)
	if tr.SessionID == "" {
		tr.SessionID = streamSessionID
	}
	tr.ToolCalls = toolCalls
	c.setActiveSessionID(tr.SessionID)
	return tr, nil
}

func runCodexCommand(cmd *exec.Cmd, logPath string, onStart func(pid int), onSession func(string)) ([]byte, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout

	logFile, err := openTryLog(logPath)
	if err != nil {
		return nil, err
	}
	if logFile != nil {
		defer logFile.Close()
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if onStart != nil && cmd.Process != nil {
		onStart(cmd.Process.Pid)
	}

	var buf bytes.Buffer
	scanErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if _, err := io.WriteString(&buf, line+"\n"); err != nil {
				scanErr <- err
				return
			}
			if logFile != nil {
				if _, err := io.WriteString(logFile, line+"\n"); err != nil {
					scanErr <- err
					return
				}
			}
			if onSession != nil {
				var ev codexJSONEvent
				if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &ev); err == nil && ev.Type == "thread.started" && ev.ThreadID != "" {
					onSession(ev.ThreadID)
				}
			}
		}
		scanErr <- scanner.Err()
	}()

	streamErr := <-scanErr
	waitErr := cmd.Wait()
	if streamErr != nil {
		return buf.Bytes(), streamErr
	}
	return buf.Bytes(), waitErr
}
