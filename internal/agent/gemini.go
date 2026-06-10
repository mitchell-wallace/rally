package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mitchell-wallace/rally/internal/reliability"
)

type GeminiExecutor struct {
	Model string
}

const (
	geminiUnparseableOutputSummary = "gemini produced no parseable JSON result"
	geminiMissingResponseSummary   = "gemini produced no structured response"
	geminiMissingSummary           = "gemini structured response contained no summary"
)

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

func (g *GeminiExecutor) ResumeSupported() bool        { return false }
func (g *GeminiExecutor) RotateSupported() bool        { return false }
func (g *GeminiExecutor) LivenessProbeSupported() bool { return false }
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
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	// Required for headless/automation mode: without this, gemini CLI refuses
	// to run in untrusted directories when stdin is not a terminal.
	cmd.Env = append(cmd.Environ(), "GEMINI_CLI_TRUST_WORKSPACE=true")
	SetProcessGroup(cmd)
	out, err := runLoggedCommand(cmd, opts.LogPath, false, opts.OnStart)
	stderrTail := tailString(stderrBuf.String(), 4096)
	if stderrTail != "" {
		_ = appendStderrToLog(opts.LogPath, stderrTail)
	}
	if err != nil {
		reason := classifyGeminiExit(err, stderrTail)
		var execErr error
		if stderrTail != "" {
			execErr = fmt.Errorf("gemini exec failed: %w%s\nstderr: %s\noutput: %s", err, reason, stderrTail, string(out))
		} else {
			execErr = fmt.Errorf("gemini exec failed: %w%s\noutput: %s", err, reason, string(out))
		}
		if ev := reliability.ParseGeminiError(stderrBuf.String()); ev != nil {
			return &TryResult{Evidence: ev}, execErr
		}
		return nil, execErr
	}

	return parseGeminiOutput(out)
}

// classifyGeminiExit maps gemini-cli exit codes (defined in gemini-cli's
// FatalError subclasses) to a short human-readable reason. Returns an empty
// string for unknown errors.
func classifyGeminiExit(err error, stderrTail string) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return ""
	}
	switch exitErr.ExitCode() {
	case 41:
		return " (authentication required — gemini-cli sees no valid credentials; run `gemini` interactively to log in or set GEMINI_API_KEY)"
	case 42:
		return " (invalid CLI input)"
	case 44:
		return " (sandbox error)"
	case 52:
		return " (config error)"
	case 53:
		return " (turn limit exceeded)"
	case 54:
		return " (tool execution error)"
	case 55:
		return " (workspace not trusted — GEMINI_CLI_TRUST_WORKSPACE=true is set but gemini still refused; try `gemini --skip-trust` or `gemini folders trust`)"
	case 130:
		return " (cancelled)"
	}
	return ""
}

// tailString returns the last n bytes of s, prefixed with "…" if truncated.
func tailString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func appendStderrToLog(path, stderr string) error {
	if path == "" || stderr == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n--- gemini stderr ---\n%s\n", stderr)
	return err
}

func parseGeminiOutput(out []byte) (*TryResult, error) {
	var wrap geminiWrapper
	if err := json.Unmarshal(out, &wrap); err != nil {
		return &TryResult{Completed: false, Summary: geminiUnparseableOutputSummary}, nil
	}

	toolCalls := 0
	if wrap.Stats != nil {
		toolCalls = wrap.Stats.Tools.TotalCalls
	}

	if strings.TrimSpace(wrap.Response) == "" {
		return &TryResult{Completed: false, Summary: geminiMissingResponseSummary, SessionID: wrap.SessionID, ToolCalls: toolCalls}, nil
	}

	var tr TryResult
	if err := json.Unmarshal([]byte(wrap.Response), &tr); err != nil {
		return &TryResult{
			Completed: true,
			Summary:   boundedExecutorFinalText(wrap.Response),
			SessionID: wrap.SessionID,
			ToolCalls: toolCalls,
		}, nil
	}
	if strings.TrimSpace(tr.Summary) == "" {
		tr.Completed = false
		tr.Summary = geminiMissingSummary
	}
	tr.SessionID = wrap.SessionID
	if tr.ToolCalls == 0 {
		tr.ToolCalls = toolCalls
	}
	return &tr, nil
}
