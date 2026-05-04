package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type OpenCodeExecutor struct {
	Model string
}

type opencodeJSONEvent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (o *OpenCodeExecutor) Execute(ctx context.Context, opts RunOptions) (*TryResult, error) {
	prompt := BuildPrompt(opts)

	args := []string{"run", prompt, "--format", "json"}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}

	cmd := exec.CommandContext(ctx, "opencode", args...)
	cmd.Env = append(os.Environ(), `OPENCODE_PERMISSION={"*":"allow"}`)
	SetProcessGroup(cmd)
	out, err := runLoggedCommand(cmd, opts.LogPath, true, opts.OnStart)
	if err != nil {
		return nil, fmt.Errorf("opencode exec failed: %w\noutput: %s", err, string(out))
	}

	var textParts []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev opencodeJSONEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == "text" {
			textParts = append(textParts, ev.Text)
		}
	}

	return parseOpenCodeOutput(out, textParts)
}

func parseOpenCodeOutput(out []byte, textParts []string) (*TryResult, error) {
	combined := strings.Join(textParts, "")
	if combined == "" {
		return &TryResult{Completed: false, Summary: string(out)}, nil
	}

	var tr TryResult
	if err := json.Unmarshal([]byte(combined), &tr); err != nil {
		return &TryResult{Completed: true, Summary: combined}, nil
	}
	return &tr, nil
}
