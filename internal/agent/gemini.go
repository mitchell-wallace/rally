package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type GeminiExecutor struct {
	Model string
}

type geminiWrapper struct {
	Response  string          `json:"response"`
	SessionID string          `json:"session_id"`
	Stats     json.RawMessage `json:"stats"`
}

func (g *GeminiExecutor) Execute(ctx context.Context, opts RunOptions) (*TryResult, error) {
	prompt := BuildPrompt(opts)

	args := []string{"--prompt", prompt, "--yolo", "--output-format", "json"}
	if g.Model != "" {
		args = append(args, "--model", g.Model)
	}

	cmd := exec.CommandContext(ctx, "gemini", args...)
	cmd.Stderr = nil // discard noisy stderr
	SetProcessGroup(cmd)
	out, err := runLoggedCommand(cmd, opts.LogPath, false, opts.OnStart)
	if err != nil {
		return nil, fmt.Errorf("gemini exec failed: %w\noutput: %s", err, string(out))
	}

	return parseGeminiOutput(out)
}

func parseGeminiOutput(out []byte) (*TryResult, error) {
	var wrap geminiWrapper
	if err := json.Unmarshal(out, &wrap); err != nil {
		return &TryResult{Completed: false, Summary: string(out)}, nil
	}

	if wrap.Response == "" {
		return &TryResult{Completed: false, Summary: string(out)}, nil
	}

	var tr TryResult
	if err := json.Unmarshal([]byte(wrap.Response), &tr); err != nil {
		return &TryResult{Completed: true, Summary: wrap.Response}, nil
	}
	return &tr, nil
}
