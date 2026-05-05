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
	Type   string          `json:"type"`
	Result json.RawMessage `json:"result"`
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

	cmd := exec.CommandContext(ctx, "claude", args...)
	SetProcessGroup(cmd)
	out, err := runLoggedCommand(cmd, opts.LogPath, true, opts.OnStart)
	if err != nil {
		return nil, fmt.Errorf("claude exec failed: %w\noutput: %s", err, string(out))
	}

	var resultRaw []byte
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev claudeJSONEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == "result" {
			resultRaw = ev.Result
			break
		}
	}

	return parseClaudeResult(out, resultRaw)
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
