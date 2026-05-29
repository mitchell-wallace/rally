package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type ClaudeExecutor struct {
	Model string
}

type claudeJSONEvent struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	Result    json.RawMessage `json:"result"`
	Message   *claudeMessage  `json:"message,omitempty"`
}

type claudeMessage struct {
	Content []claudeContentBlock `json:"content"`
}

type claudeContentBlock struct {
	Type string `json:"type"`
}

func (c *ClaudeExecutor) Execute(ctx context.Context, opts RunOptions) (*TryResult, error) {
	prompt := BuildPrompt(opts)

	model := c.Model
	if opts.Model != "" {
		model = opts.Model
	}

	args := []string{"-p", prompt, "--dangerously-skip-permissions", "--output-format", "stream-json", "--verbose"}
	if model != "" {
		args = append(args, "--model", model)
	}
	if opts.ResumeSessionID != "" {
		args = append(args, "--resume", opts.ResumeSessionID)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	if opts.WorkspaceDir != "" {
		cmd.Dir = opts.WorkspaceDir
	}
	SetProcessGroup(cmd)
	out, err := runLoggedCommand(cmd, opts.LogPath, true, opts.OnStart)
	if err != nil {
		return nil, fmt.Errorf("claude exec failed: %w\noutput: %s", err, string(out))
	}

	resultRaw, sessionID, toolCalls := scanClaudeOutput(out)
	tr, parseErr := parseClaudeResult(out, resultRaw)
	if parseErr != nil {
		return nil, parseErr
	}
	tr.SessionID = sessionID
	tr.ToolCalls = toolCalls
	return tr, nil
}

func (c *ClaudeExecutor) ResumeSupported() bool        { return true }
func (c *ClaudeExecutor) RotateSupported() bool        { return false }
func (c *ClaudeExecutor) LivenessProbeSupported() bool { return false }
func (c *ClaudeExecutor) RotateModel(string) error {
	return fmt.Errorf("rotate not supported by claude adapter")
}
func (c *ClaudeExecutor) ProbeLiveness(_ context.Context) (bool, error) {
	return false, fmt.Errorf("liveness probe not supported by claude adapter")
}

func scanClaudeOutput(out []byte) (resultRaw []byte, sessionID string, toolCalls int) {
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev claudeJSONEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.SessionID != "" {
			sessionID = ev.SessionID
		}
		if ev.Type == "assistant" && ev.Message != nil {
			for _, block := range ev.Message.Content {
				if block.Type == "tool_use" {
					toolCalls++
				}
			}
		}
		if ev.Type == "result" {
			resultRaw = ev.Result
			break
		}
	}
	return resultRaw, sessionID, toolCalls
}

func parseClaudeResult(out, resultRaw []byte) (*TryResult, error) {
	if resultRaw == nil {
		return &TryResult{Completed: false, Summary: string(out)}, nil
	}

	var tr TryResult
	if err := json.Unmarshal(resultRaw, &tr); err != nil {
		return &TryResult{Completed: true, Summary: string(resultRaw)}, nil
	}

	return &tr, nil
}
