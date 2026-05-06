package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRoutesConfig(t *testing.T, workspaceDir, content string) {
	t.Helper()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatalf("mkdir .rally: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func withWorkspaceDir(t *testing.T, workspaceDir string) {
	t.Helper()
	prev := resolveWorkspaceDir
	resolveWorkspaceDir = func() (string, error) { return workspaceDir, nil }
	t.Cleanup(func() { resolveWorkspaceDir = prev })
}

func executeRoutesCheck(t *testing.T, workspaceDir string) (string, error) {
	t.Helper()
	withWorkspaceDir(t, workspaceDir)

	cmd := NewRoutesCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"check"})
	err := cmd.Execute()
	return stdout.String(), err
}

func TestRoutesCheckCleanConfig(t *testing.T) {
	workspaceDir := t.TempDir()
	writeRoutesConfig(t, workspaceDir, `schema_version = 2

[routes]
default = ["cc", "cx"]
`)

	output, err := executeRoutesCheck(t, workspaceDir)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "routes check summary:") {
		t.Fatalf("output = %q, want summary header", output)
	}
	if !strings.Contains(output, "- default: 2 entries") {
		t.Fatalf("output = %q, want default route count", output)
	}
}

func TestRoutesCheckParseError(t *testing.T) {
	workspaceDir := t.TempDir()
	writeRoutesConfig(t, workspaceDir, `schema_version = 2

[routes]
default = ["claude:opus:4.7"]
`)

	_, err := executeRoutesCheck(t, workspaceDir)
	if err == nil {
		t.Fatal("Execute() error = nil, want parse failure")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, `route "default"`) || !strings.Contains(errStr, `claude:opus:4.7`) {
		t.Fatalf("error = %q, want route and offending entry", errStr)
	}
}

func TestRoutesCheckQuotaError(t *testing.T) {
	workspaceDir := t.TempDir()
	writeRoutesConfig(t, workspaceDir, `schema_version = 2

[routes]
default = ["cc:0"]
`)

	_, err := executeRoutesCheck(t, workspaceDir)
	if err == nil {
		t.Fatal("Execute() error = nil, want quota failure")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, `entry "cc:0"`) || !strings.Contains(errStr, "must be positive") {
		t.Fatalf("error = %q, want quota bounds message", errStr)
	}
}

func TestRoutesCheckResolutionErrorShowsDidYouMean(t *testing.T) {
	workspaceDir := t.TempDir()
	writeRoutesConfig(t, workspaceDir, `schema_version = 2

[harness.op]
models = { z = "zai-coding-plan/glm-5.1", gk = "opencode-go/kimi-k2.6", mini = "opencode/mini" }

[routes]
default = ["op:gp"]
`)

	_, err := executeRoutesCheck(t, workspaceDir)
	if err == nil {
		t.Fatal("Execute() error = nil, want resolution failure")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, `entry "op:gp"`) {
		t.Fatalf("error = %q, want offending entry", errStr)
	}
	if !strings.Contains(errStr, "did you mean") || !strings.Contains(errStr, "gk") {
		t.Fatalf("error = %q, want did-you-mean suggestion", errStr)
	}
}

func TestRoutesCheckUnknownAliasShowsDidYouMean(t *testing.T) {
	workspaceDir := t.TempDir()
	writeRoutesConfig(t, workspaceDir, `schema_version = 2

[routes]
default = ["copex"]
`)

	_, err := executeRoutesCheck(t, workspaceDir)
	if err == nil {
		t.Fatal("Execute() error = nil, want alias resolution failure")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, `entry "copex"`) {
		t.Fatalf("error = %q, want offending entry", errStr)
	}
	if !strings.Contains(errStr, "did you mean") || !strings.Contains(errStr, "codex") {
		t.Fatalf("error = %q, want alias did-you-mean suggestion", errStr)
	}
}

func TestRoutesCheckUnreachableRouteInfo(t *testing.T) {
	workspaceDir := t.TempDir()
	writeRoutesConfig(t, workspaceDir, `schema_version = 2

[routes]
default = ["cc"]
MARKETING = ["cx"]
`)

	output, err := executeRoutesCheck(t, workspaceDir)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, `info: route "MARKETING" is declared but not referenced by any current bead assignee`) {
		t.Fatalf("output = %q, want unreachable route info", output)
	}
}

func TestRoutesCheckMissingDefaultWarnsButSucceeds(t *testing.T) {
	workspaceDir := t.TempDir()
	writeRoutesConfig(t, workspaceDir, `schema_version = 2

[routes]
SENIOR = ["cc"]
`)

	output, err := executeRoutesCheck(t, workspaceDir)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output, "warning: no default route is configured") {
		t.Fatalf("output = %q, want missing default warning", output)
	}
}
