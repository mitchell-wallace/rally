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
	Response  string       `json:"response"`
	SessionID string       `json:"session_id"`
	Stats     *geminiStats `json:"stats,omitempty"`
}

type geminiStats struct {
	Tools struct {
		TotalCalls int `json:"totalCalls"`
	} `json:"tools"`
}

func (g *GeminiExecutor) ResumeSupported() bool                { return true }
func (g *GeminiExecutor) RotateSupported() bool                { return false }
func (g *GeminiExecutor) LivenessProbeSupported() bool         { return false }
func (g *GeminiExecutor) RotateModel(string) error {
	return fmt.Errorf("rotate not supported by gemini adapter")
}
func (g *GeminiExecutor) ProbeLiveness(_ context.Context) (bool, error) {
	return false, fmt.Errorf("liveness probe not supported by gemini adapter")
}

func (g *GeminiExecutor) Execute(ctx context.Context, opts RunOptions) (*TryResult, error) {
	prompt := BuildPrompt(opts)

	model := g.Model
	if opts.Model != "" {
		model = opts.Model
	}

	args := []string{"--prompt", prompt, "--yolo", "--output-format", "json"}
	if model != "" {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(ctx, "gemini", args...)
	if opts.WorkspaceDir != "" {
		cmd.Dir = opts.WorkspaceDir
	}
	cmd.Stderr = nil // discard noisy stderr
	// Required for headless/automation mode: without this, gemini CLI refuses
	// to run in untrusted directories when stdin is not a terminal.
	cmd.Env = append(cmd.Environ(), "GEMINI_CLI_TRUST_WORKSPACE=true")
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

	toolCalls := 0
	if wrap.Stats != nil {
		toolCalls = wrap.Stats.Tools.TotalCalls
	}

	if wrap.Response == "" {
		return &TryResult{Completed: false, Summary: string(out), SessionID: wrap.SessionID, ToolCalls: toolCalls}, nil
	}

	var tr TryResult
	if err := json.Unmarshal([]byte(wrap.Response), &tr); err != nil {
		return &TryResult{Completed: true, Summary: wrap.Response, SessionID: wrap.SessionID, ToolCalls: toolCalls}, nil
	}
	tr.SessionID = wrap.SessionID
	if tr.ToolCalls == 0 {
		tr.ToolCalls = toolCalls
	}
	return &tr, nil
}
