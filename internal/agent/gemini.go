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

func (g *GeminiExecutor) ResumeSupported() bool                { return false }
func (g *GeminiExecutor) RotateSupported() bool                { return false }
func (g *GeminiExecutor) LivenessProbeSupported() bool         { return false }
func (g *GeminiExecutor) CharsPerToken() float64               { return 0 }
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
