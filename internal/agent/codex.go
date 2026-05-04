package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

type CodexExecutor struct {
	Model string
}

func writeCodexSchema() (string, error) {
	f, err := os.CreateTemp("", "codex-schema-*.json")
	if err != nil {
		return "", err
	}
	schema := `{"type":"object","properties":{"completed":{"type":"boolean"},"summary":{"type":"string"},"remaining_work":{"type":"string"},"message_addressed":{"type":"boolean"},"files_changed":{"type":"array","items":{"type":"string"}}}}`
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

func (c *CodexExecutor) Execute(ctx context.Context, opts RunOptions) (*TryResult, error) {
	prompt := BuildPrompt(opts)

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

	args := []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--full-auto"}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	args = append(args, "--output-schema", schemaPath, "-o", reportPath, prompt)

	cmd := exec.CommandContext(ctx, "codex", args...)
	SetProcessGroup(cmd)
	out, err := runLoggedCommand(cmd, opts.LogPath, true, opts.OnStart)
	if err != nil {
		os.Remove(reportPath)
		return nil, fmt.Errorf("codex exec failed: %w\noutput: %s", err, string(out))
	}

	reportData, err := os.ReadFile(reportPath)
	os.Remove(reportPath)
	if err != nil {
		return nil, fmt.Errorf("codex report read failed: %w\noutput: %s", err, string(out))
	}

	return parseCodexResult(reportData)
}
