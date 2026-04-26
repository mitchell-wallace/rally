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

func (c *CodexExecutor) Execute(ctx context.Context, opts RunOptions) (*TryResult, error) {
	prompt := BuildPrompt(opts)

	schemaPath := "./schema.json"
	reportPath := "./report.json"

	args := []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--full-auto"}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	args = append(args, "--output-schema", schemaPath, "-o", reportPath, prompt)

	cmd := exec.CommandContext(ctx, "codex", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("codex exec failed: %w\noutput: %s", err, string(out))
	}

	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		_ = os.Remove(reportPath)
		_ = os.Remove(schemaPath)
		return nil, fmt.Errorf("codex report read failed: %w\noutput: %s", err, string(out))
	}
	_ = os.Remove(reportPath)
	_ = os.Remove(schemaPath)

	var tr TryResult
	if err := json.Unmarshal(reportData, &tr); err != nil {
		return &TryResult{Completed: true, Summary: string(reportData)}, nil
	}
	tr.Completed = true
	return &tr, nil
}
