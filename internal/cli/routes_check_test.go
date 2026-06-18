package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/store"
)

func writeRoutesConfig(t *testing.T, workspaceDir, content string) {
	t.Helper()
	rallyDir := store.RallyDir(workspaceDir)
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

func TestRoutesCheckReasoningMissingScopedAlias(t *testing.T) {
	workspaceDir := t.TempDir()
	writeRoutesConfig(t, workspaceDir, `schema_version = 2

[harness.cc.models]
opus-high = "claude-opus-4-8"

[reasoning]
verify = "cc:opus-hihg"

[routes]
default = ["cc"]
`)

	_, err := executeRoutesCheck(t, workspaceDir)
	if err == nil {
		t.Fatal("Execute() error = nil, want missing scoped alias failure")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "[reasoning].verify") {
		t.Fatalf("error = %q, want role name in diagnostic", errStr)
	}
	if !strings.Contains(errStr, "opus-hihg") || !strings.Contains(errStr, "did you mean") {
		t.Fatalf("error = %q, want unknown alias and suggestion", errStr)
	}
}

func TestRoutesCheckReasoningScopedAliasResolves(t *testing.T) {
	workspaceDir := t.TempDir()
	writeRoutesConfig(t, workspaceDir, `schema_version = 2

[harness.cc.models]
opus-high = "claude-opus-4-8"

[reasoning]
verify = "cc:opus-high"

[routes]
default = ["cc"]
`)

	output, err := executeRoutesCheck(t, workspaceDir)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(output, "[reasoning].verify") {
		t.Fatalf("output = %q, want no reasoning warning for a resolvable scoped alias", output)
	}
}

func TestRoutesCheckReasoningBareEffortNoWarning(t *testing.T) {
	workspaceDir := t.TempDir()
	writeRoutesConfig(t, workspaceDir, `schema_version = 2

[reasoning]
verify = "high"

[routes]
default = ["cc"]
`)

	output, err := executeRoutesCheck(t, workspaceDir)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(output, "[reasoning].verify") {
		t.Fatalf("output = %q, want no warning for a documented effort token", output)
	}
}

func TestRoutesCheckReasoningUnknownBareTokenWarns(t *testing.T) {
	workspaceDir := t.TempDir()
	writeRoutesConfig(t, workspaceDir, `schema_version = 2

[reasoning]
verify = "ludicrous"

[routes]
default = ["cc"]
`)

	output, err := executeRoutesCheck(t, workspaceDir)
	if err != nil {
		t.Fatalf("Execute() error = %v, want warning rather than hard failure", err)
	}
	if !strings.Contains(output, "[reasoning].verify") || !strings.Contains(output, "ludicrous") {
		t.Fatalf("output = %q, want pass-through warning for an unknown bare token", output)
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
	if !strings.Contains(output, `info: route "MARKETING" is declared but not referenced by any current lap assignee`) {
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

func TestRoutesCheckRolePromptDiagnosticsEmbedded(t *testing.T) {
	workspaceDir := t.TempDir()
	writeRoutesConfig(t, workspaceDir, `schema_version = 2

[routes]
default = ["cc"]
`)

	output, err := executeRoutesCheck(t, workspaceDir)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !strings.Contains(output, "role prompt diagnostics:") {
		t.Fatalf("output = %q, want role prompt diagnostics header", output)
	}
	if !strings.Contains(output, "- junior: ~") || !strings.Contains(output, "(embedded)") {
		t.Fatalf("output = %q, want embedded junior role count", output)
	}
}

func TestRoutesCheckRolePromptDiagnosticsCustomAndOverlap(t *testing.T) {
	workspaceDir := t.TempDir()
	writeRoutesConfig(t, workspaceDir, `schema_version = 2

[routes]
default = ["cc"]
`)
	agentsDir := filepath.Join(workspaceDir, ".rally", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Custom role with overlap term
	if err := os.WriteFile(filepath.Join(agentsDir, "senior.md"), []byte("This is a custom senior role. laps done is great."), 0o644); err != nil {
		t.Fatal(err)
	}
	// Custom role without overlap
	if err := os.WriteFile(filepath.Join(agentsDir, "qa.md"), []byte("Just a custom QA role, no overlap here."), 0o644); err != nil {
		t.Fatal(err)
	}
	// Custom role with headless overlap
	if err := os.WriteFile(filepath.Join(agentsDir, "script.md"), []byte("Headless script execution context."), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := executeRoutesCheck(t, workspaceDir)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !strings.Contains(output, "- senior: ~") || !strings.Contains(output, "(custom, .rally/agents/senior.md)") {
		t.Fatalf("output = %q, want custom senior role count", output)
	}
	if !strings.Contains(output, "- qa: ~") || !strings.Contains(output, "(custom, .rally/agents/qa.md)") {
		t.Fatalf("output = %q, want custom qa role count", output)
	}
	if !strings.Contains(output, "- script: ~") || !strings.Contains(output, "(custom, .rally/agents/script.md)") {
		t.Fatalf("output = %q, want custom script role count", output)
	}

	if !strings.Contains(output, `advisory: custom role prompt .rally/agents/senior.md references "laps done"`) {
		t.Fatalf("output = %q, want advisory for laps done overlap", output)
	}
	if !strings.Contains(output, `advisory: custom role prompt .rally/agents/script.md references "headless"`) {
		t.Fatalf("output = %q, want advisory for headless overlap", output)
	}
	if strings.Contains(output, `advisory: custom role prompt .rally/agents/qa.md`) {
		t.Fatalf("output = %q, want NO advisory for qa role", output)
	}
}
