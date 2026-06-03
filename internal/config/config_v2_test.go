package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/store"
)

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	rallyDir := store.RallyDir(dir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadV2_LegacyRootModelFields(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `claude_model = "sonnet"
codex_model = "codex-latest"
gemini_model = ""
opencode_model = ""
data_dir = "/tmp/data"
run_hooks_on_autocommit = true
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	if cfg.ClaudeModel != "sonnet" {
		t.Errorf("ClaudeModel = %q, want %q", cfg.ClaudeModel, "sonnet")
	}
	if cfg.CodexModel != "codex-latest" {
		t.Errorf("CodexModel = %q, want %q", cfg.CodexModel, "codex-latest")
	}
	if len(cfg.DeprecationNotes) != 2 {
		t.Errorf("expected 2 deprecation notes, got %d: %v", len(cfg.DeprecationNotes), cfg.DeprecationNotes)
	}
	for _, note := range cfg.DeprecationNotes {
		if !strings.Contains(note, "deprecated") {
			t.Errorf("deprecation note missing 'deprecated': %q", note)
		}
	}
	if cfg.DataDir != "/tmp/data" {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, "/tmp/data")
	}
	if !cfg.RunHooksOnAutoCommit {
		t.Error("RunHooksOnAutoCommit should be true")
	}
}

func TestLoadV2_DefaultsSectionModelFields(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[defaults]
claude_model = "opus"
codex_model = "codex-v2"
antigravity_model = "Gemini 3.5 Flash (High)"
iterations = 10
mix = "cc cx"
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	if cfg.ClaudeModel != "opus" {
		t.Errorf("ClaudeModel = %q, want %q", cfg.ClaudeModel, "opus")
	}
	if cfg.CodexModel != "codex-v2" {
		t.Errorf("CodexModel = %q, want %q", cfg.CodexModel, "codex-v2")
	}
	if cfg.AntigravityModel != "Gemini 3.5 Flash (High)" {
		t.Errorf("AntigravityModel = %q, want %q", cfg.AntigravityModel, "Gemini 3.5 Flash (High)")
	}
	if cfg.Defaults.Iterations != 10 {
		t.Errorf("Defaults.Iterations = %d, want 10", cfg.Defaults.Iterations)
	}
	if cfg.Defaults.Mix != "cc cx" {
		t.Errorf("Defaults.Mix = %q, want %q", cfg.Defaults.Mix, "cc cx")
	}
	if len(cfg.DeprecationNotes) != 0 {
		t.Errorf("expected 0 deprecation notes, got %d: %v", len(cfg.DeprecationNotes), cfg.DeprecationNotes)
	}
	if cfg.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", cfg.SchemaVersion)
	}
}

func TestLoadV2_ReliabilityStallThreshold(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[reliability]
stall_threshold_secs = 90
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	if got, want := cfg.Reliability.StallThresholdSecs, 90; got != want {
		t.Fatalf("Reliability.StallThresholdSecs = %d, want %d", got, want)
	}
	if got, want := cfg.Reliability.StallThreshold(), 90*time.Second; got != want {
		t.Fatalf("Reliability.StallThreshold() = %v, want %v", got, want)
	}
}

func TestLoadV2_ReliabilityLivenessProbe(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[reliability]
liveness_probe = true
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	if !cfg.Reliability.LivenessProbe {
		t.Fatal("Reliability.LivenessProbe = false, want true")
	}
}

func TestLoadV2_ReliabilityDefaults(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	if got, want := cfg.Reliability.StallThresholdSecs, 900; got != want {
		t.Errorf("Default StallThresholdSecs = %d, want %d", got, want)
	}
	if got, want := cfg.Reliability.RetryBudget, 5; got != want {
		t.Errorf("Default RetryBudget = %d, want %d", got, want)
	}
	if cfg.Reliability.LivenessProbe {
		t.Errorf("Default LivenessProbe = true, want false")
	}
}

func TestLoadV2_ReliabilityOverrides(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[reliability]
stall_threshold_secs = 120
liveness_probe = true
retry_budget = 10
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	if got, want := cfg.Reliability.StallThresholdSecs, 120; got != want {
		t.Errorf("StallThresholdSecs = %d, want %d", got, want)
	}
	if got, want := cfg.Reliability.RetryBudget, 10; got != want {
		t.Errorf("RetryBudget = %d, want %d", got, want)
	}
	if !cfg.Reliability.LivenessProbe {
		t.Errorf("LivenessProbe = false, want true")
	}
}

func TestLoadV2_DefaultsOverridesRoot(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `claude_model = "root-value"
codex_model = "root-codex"

[defaults]
claude_model = "defaults-value"
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	if cfg.ClaudeModel != "defaults-value" {
		t.Errorf("ClaudeModel = %q, want %q (defaults should win)", cfg.ClaudeModel, "defaults-value")
	}
	if cfg.CodexModel != "root-codex" {
		t.Errorf("CodexModel = %q, want %q (root fallback)", cfg.CodexModel, "root-codex")
	}

	found := false
	for _, note := range cfg.DeprecationNotes {
		if strings.Contains(note, "claude_model") {
			found = true
		}
	}
	if !found {
		t.Error("expected deprecation note for shadowed root-level claude_model")
	}
}

func TestLoadV2_SchemaVersionAbsent(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `data_dir = "/tmp"
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if cfg.SchemaVersion != 0 {
		t.Errorf("SchemaVersion = %d, want 0 (absent)", cfg.SchemaVersion)
	}
	if cfg.SchemaWarning != "" {
		t.Errorf("SchemaWarning = %q, want empty for absent version", cfg.SchemaWarning)
	}
}

func TestLoadV2_SchemaVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 99
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if cfg.SchemaWarning == "" {
		t.Error("expected SchemaWarning for mismatched version")
	}
	if !strings.Contains(cfg.SchemaWarning, "99") || !strings.Contains(cfg.SchemaWarning, "2") {
		t.Errorf("SchemaWarning = %q, should mention 99 and 2", cfg.SchemaWarning)
	}
}

func TestLoadV2_SchemaVersionMatch(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if cfg.SchemaWarning != "" {
		t.Errorf("SchemaWarning = %q, want empty for matching version", cfg.SchemaWarning)
	}
}

func TestLoadV2_HarnessSections(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[harness.droid]
command = ["droid", "run"]
model_flag = "--model"
output_strategy = "tail"
output_lines = 40
tail_stream = "combined"

[harness.droid.models]
default = "droid-v1"
fast = "droid-v1-turbo"
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	h, ok := cfg.Harnesses["droid"]
	if !ok {
		t.Fatal("expected harness 'droid' to be present")
	}
	if len(h.Command) != 2 || h.Command[0] != "droid" || h.Command[1] != "run" {
		t.Errorf("Command = %v, want [droid run]", h.Command)
	}
	if h.ModelFlag == nil || *h.ModelFlag != "--model" {
		t.Errorf("ModelFlag = %v, want pointer to '--model'", h.ModelFlag)
	}
	if h.OutputStrategy != "tail" {
		t.Errorf("OutputStrategy = %q, want 'tail'", h.OutputStrategy)
	}
	if h.OutputLines != 40 {
		t.Errorf("OutputLines = %d, want 40", h.OutputLines)
	}
	if h.TailStream != "combined" {
		t.Errorf("TailStream = %q, want 'combined'", h.TailStream)
	}
	if h.Models["default"] != "droid-v1" {
		t.Errorf("Models['default'] = %q, want 'droid-v1'", h.Models["default"])
	}
	if h.Models["fast"] != "droid-v1-turbo" {
		t.Errorf("Models['fast'] = %q, want 'droid-v1-turbo'", h.Models["fast"])
	}
}

func TestLoadV2_ModelFlagEmptyString(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `[harness.tool]
command = ["tool"]
model_flag = ""
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	h := cfg.Harnesses["tool"]
	if h.ModelFlag == nil {
		t.Fatal("ModelFlag should be non-nil (set to empty string)")
	}
	if *h.ModelFlag != "" {
		t.Errorf("ModelFlag = %q, want empty string", *h.ModelFlag)
	}
}

func TestLoadV2_ModelFlagAbsent(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `[harness.tool]
command = ["tool"]
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	h := cfg.Harnesses["tool"]
	if h.ModelFlag != nil {
		t.Errorf("ModelFlag should be nil when absent, got %q", *h.ModelFlag)
	}
}

func TestLoadV2_LapsAndFallback(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
[laps]
instructions_file = ".rally/laps_instructions.md"
[fallback]
instructions_file = ".rally/fallback_instructions.md"
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if cfg.Laps.InstructionsFile != ".rally/laps_instructions.md" {
		t.Errorf("Laps.InstructionsFile = %q, wrong", cfg.Laps.InstructionsFile)
	}
	if cfg.Fallback.InstructionsFile != ".rally/fallback_instructions.md" {
		t.Errorf("Fallback.InstructionsFile = %q, wrong", cfg.Fallback.InstructionsFile)
	}
}

func TestSaveV2_WritesNewShapeOnly(t *testing.T) {
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := V2Config{
		ClaudeModel:          "sonnet",
		CodexModel:           "codex-v2",
		DataDir:              "/tmp/data",
		RunHooksOnAutoCommit: true,
	}

	if err := SaveV2(dir, cfg); err != nil {
		t.Fatalf("SaveV2 failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rallyDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if strings.Contains(content, "claude_model =") && !strings.Contains(content, "[defaults]") {
		t.Error("written config should NOT have root-level claude_model without [defaults]")
	}
	if !strings.Contains(content, "[defaults]") {
		t.Error("written config missing [defaults] section")
	}
	if !strings.Contains(content, "schema_version = 2") {
		t.Error("written config missing schema_version = 2")
	}

	cfg2, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("re-load failed: %v", err)
	}
	if cfg2.ClaudeModel != "sonnet" {
		t.Errorf("round-trip: ClaudeModel = %q, want 'sonnet'", cfg2.ClaudeModel)
	}
	if cfg2.SchemaVersion != 2 {
		t.Errorf("round-trip: SchemaVersion = %d, want 2", cfg2.SchemaVersion)
	}
}

func TestSaveV2_NoRootLevelModelFields(t *testing.T) {
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := V2Config{
		ClaudeModel: "opus",
	}
	if err := SaveV2(dir, cfg); err != nil {
		t.Fatalf("SaveV2 failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rallyDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	lines := strings.Split(content, "\n")
	inDefaults := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[defaults]" {
			inDefaults = true
			continue
		}
		if strings.HasPrefix(trimmed, "[") && trimmed != "[defaults]" {
			inDefaults = false
		}
		if !inDefaults && strings.HasPrefix(trimmed, "claude_model") {
			t.Errorf("found root-level claude_model in written config: %q", line)
		}
	}
}

func TestSaveV2_PreservesHarnesses(t *testing.T) {
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	flag := "--model"
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"droid": {
				Command:   []string{"droid", "run"},
				ModelFlag: &flag,
				Models:    map[string]string{"v1": "droid-v1"},
			},
		},
	}

	if err := SaveV2(dir, cfg); err != nil {
		t.Fatalf("SaveV2 failed: %v", err)
	}

	cfg2, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("re-load failed: %v", err)
	}
	h := cfg2.Harnesses["droid"]
	if h == nil {
		t.Fatal("harness 'droid' lost in round-trip")
	}
	if len(h.Command) != 2 {
		t.Errorf("Command length = %d, want 2", len(h.Command))
	}
	if h.Models["v1"] != "droid-v1" {
		t.Errorf("Models['v1'] = %q, want 'droid-v1'", h.Models["v1"])
	}
}

func TestLoadV2_NoConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 should not error on missing file: %v", err)
	}
	if cfg.ClaudeModel != "" {
		t.Errorf("ClaudeModel = %q, want empty", cfg.ClaudeModel)
	}
	if cfg.Harnesses == nil {
		t.Error("Harnesses should be non-nil map")
	}
}

func TestLoadV2_InvalidHarnessName(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[harness."bad name"]
command = ["tool"]
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for invalid harness name with spaces")
	}
	if !strings.Contains(err.Error(), "invalid harness name") {
		t.Errorf("error = %q, want 'invalid harness name'", err.Error())
	}
}

func TestLoadV2_BuiltInRejectsCommand(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[harness.cc]
command = ["echo", "hi"]
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for built-in harness with command")
	}
	if !strings.Contains(err.Error(), "cannot declare command") {
		t.Errorf("error = %q, want 'cannot declare command'", err.Error())
	}
}

func TestLoadV2_BuiltInRejectsModelFlag(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[harness.claude]
model_flag = "--model"
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for built-in harness with model_flag")
	}
	if !strings.Contains(err.Error(), "cannot declare model_flag") {
		t.Errorf("error = %q, want 'cannot declare model_flag'", err.Error())
	}
}

func TestLoadV2_BuiltInRejectsOutputStrategy(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[harness.op]
output_strategy = "tail"
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for built-in harness with output_strategy")
	}
	if !strings.Contains(err.Error(), "cannot declare output_strategy") {
		t.Errorf("error = %q, want 'cannot declare output_strategy'", err.Error())
	}
}

func TestLoadV2_BuiltInRejectsTailStream(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[harness.gemini]
tail_stream = "stdout"
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for built-in harness with tail_stream")
	}
	if !strings.Contains(err.Error(), "cannot declare tail_stream") {
		t.Errorf("error = %q, want 'cannot declare tail_stream'", err.Error())
	}
}

func TestLoadV2_BuiltInAllowsModelsOnly(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[harness.cc.models]
opus = "claude-opus-4-7"
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("expected no error for built-in harness with only models: %v", err)
	}
	h := cfg.Harnesses["cc"]
	if h.Models["opus"] != "claude-opus-4-7" {
		t.Errorf("Models['opus'] = %q, want 'claude-opus-4-7'", h.Models["opus"])
	}
}

func TestLoadV2_NumericModelNameRejected(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[harness.droid]
command = ["droid"]

[harness.droid.models]
4 = "some-model"
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for numeric-only model name")
	}
	if !strings.Contains(err.Error(), "4") || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error = %q, want complaint about numeric model name", err.Error())
	}
}

func TestLoadV2_EmptyModelStringRejected(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[harness.droid]
command = ["droid"]

[harness.droid.models]
fast = ""
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for empty model string")
	}
	if !strings.Contains(err.Error(), "empty model string") {
		t.Errorf("error = %q, want 'empty model string'", err.Error())
	}
}

func TestLoadV2_DollarModelInCommandRejected(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[harness.droid]
command = ["droid", "$MODEL"]
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for $MODEL in command")
	}
	if !strings.Contains(err.Error(), "$MODEL") || !strings.Contains(err.Error(), "model_flag") {
		t.Errorf("error = %q, want $MODEL and model_flag", err.Error())
	}
}

func TestLoadV2_DollarModelPartialInCommandRejected(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[harness.droid]
command = ["droid", "--model=$MODEL"]
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for $MODEL in command element")
	}
	if !strings.Contains(err.Error(), "$MODEL") {
		t.Errorf("error = %q, want $MODEL", err.Error())
	}
}

func TestLoadV2_UserHarnessRejectsBadOutputStrategy(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[harness.droid]
command = ["droid"]
output_strategy = "json"
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for unsupported output_strategy")
	}
	if !strings.Contains(err.Error(), "output_strategy") || !strings.Contains(err.Error(), "tail") {
		t.Errorf("error = %q, want complaint about output_strategy", err.Error())
	}
}

func TestLoadV2_UserHarnessRejectsBadTailStream(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[harness.droid]
command = ["droid"]
output_strategy = "tail"
tail_stream = "both"
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for unsupported tail_stream")
	}
	if !strings.Contains(err.Error(), "tail_stream") {
		t.Errorf("error = %q, want complaint about tail_stream", err.Error())
	}
}

func TestLoadV2_UserHarnessAcceptsValidOutputStrategy(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
[harness.droid]
command = ["droid"]
output_strategy = "tail"
output_lines = 100
tail_stream = "stderr"
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("expected no error: %v", err)
	}
	h := cfg.Harnesses["droid"]
	if h.OutputLines != 100 {
		t.Errorf("OutputLines = %d, want 100", h.OutputLines)
	}
	if h.TailStream != "stderr" {
		t.Errorf("TailStream = %q, want 'stderr'", h.TailStream)
	}
}

func TestResolveAgent_BareAlias_NoDefault(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{}}
	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "claude" {
		t.Errorf("Harness = %q, want 'claude'", resolved.Harness)
	}
	if resolved.Model != "" {
		t.Errorf("Model = %q, want empty when no default configured", resolved.Model)
	}
}

func TestResolveAgent_BareAlias_WithDefault(t *testing.T) {
	cfg := V2Config{ClaudeModel: "sonnet-4", Harnesses: map[string]*HarnessConfig{}}
	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "claude" {
		t.Errorf("Harness = %q, want 'claude'", resolved.Harness)
	}
	if resolved.Model != "sonnet-4" {
		t.Errorf("Model = %q, want 'sonnet-4' from config default", resolved.Model)
	}
}

func TestResolveAgent_WeightPassthrough(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{}}
	resolved, err := cfg.ResolveAgent("cc:2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "claude" {
		t.Errorf("Harness = %q, want 'claude'", resolved.Harness)
	}
	if resolved.Model != "" {
		t.Errorf("Model = %q, want empty for weight passthrough with no default", resolved.Model)
	}
}

func TestResolveAgent_WeightPassthrough_WithDefault(t *testing.T) {
	cfg := V2Config{ClaudeModel: "sonnet-4", Harnesses: map[string]*HarnessConfig{}}
	resolved, err := cfg.ResolveAgent("cc:2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "claude" {
		t.Errorf("Harness = %q, want 'claude'", resolved.Harness)
	}
	if resolved.Model != "sonnet-4" {
		t.Errorf("Model = %q, want 'sonnet-4' from config default", resolved.Model)
	}
}

func TestResolveAgent_NamedModel(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"op": {Models: map[string]string{
				"z":  "zai-coding-plan/glm-5.1",
				"gk": "opencode-go/kimi-k2.6",
			}},
		},
	}
	resolved, err := cfg.ResolveAgent("op:z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "opencode" {
		t.Errorf("Harness = %q, want 'opencode'", resolved.Harness)
	}
	if resolved.Model != "zai-coding-plan/glm-5.1" {
		t.Errorf("Model = %q, want 'zai-coding-plan/glm-5.1'", resolved.Model)
	}
}

func TestResolveAgent_AntigravityAliases(t *testing.T) {
	cfg := V2Config{AntigravityModel: "Gemini 3.5 Flash (High)", Harnesses: map[string]*HarnessConfig{}}
	for _, alias := range []string{"ag", "agy", "antigravity"} {
		t.Run(alias, func(t *testing.T) {
			resolved, err := cfg.ResolveAgent(alias)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resolved.Harness != "antigravity" {
				t.Errorf("Harness = %q, want 'antigravity'", resolved.Harness)
			}
			if resolved.Model != "Gemini 3.5 Flash (High)" {
				t.Errorf("Model = %q, want configured Antigravity model", resolved.Model)
			}
		})
	}
}

func TestResolveAgent_AntigravityNamedModel(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"ag": {Models: map[string]string{
				"flash": "Gemini 3.5 Flash (High)",
			}},
		},
	}
	resolved, err := cfg.ResolveAgent("ag:flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "antigravity" {
		t.Errorf("Harness = %q, want 'antigravity'", resolved.Harness)
	}
	if resolved.Model != "Gemini 3.5 Flash (High)" {
		t.Errorf("Model = %q, want 'Gemini 3.5 Flash (High)'", resolved.Model)
	}
}

func TestResolveAgent_RawModelString(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{}}
	resolved, err := cfg.ResolveAgent("opencode:zai-coding-plan/glm-5.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "opencode" {
		t.Errorf("Harness = %q, want 'opencode'", resolved.Harness)
	}
	if resolved.Model != "zai-coding-plan/glm-5.1" {
		t.Errorf("Model = %q, want 'zai-coding-plan/glm-5.1'", resolved.Model)
	}
}

func TestResolveAgent_UnknownHarness(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{}}
	_, err := cfg.ResolveAgent("unknown")
	if err == nil {
		t.Fatal("expected error for unknown harness")
	}
	if !strings.Contains(err.Error(), "unknown agent alias") {
		t.Errorf("error = %q, want 'unknown agent alias'", err.Error())
	}
}

func TestResolveAgent_UnresolvedModelDidYouMean(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"op": {Models: map[string]string{
				"z":  "zai-coding-plan/glm-5.1",
				"gk": "opencode-go/kimi-k2.6",
			}},
		},
	}
	_, err := cfg.ResolveAgent("op:gp")
	if err == nil {
		t.Fatal("expected error for unresolved model name")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("error = %q, want 'did you mean'", err.Error())
	}
	if !strings.Contains(err.Error(), "gk") {
		t.Errorf("error = %q, should suggest 'gk'", err.Error())
	}
}

func TestResolveAgent_NumericOnlyModelRejected(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{}}
	_, err := cfg.ResolveAgent("cc:2")
	if err != nil {
		t.Fatalf("numeric right side should be treated as weight: %v", err)
	}
}

func TestResolveAgent_ThirdSegmentRejected(t *testing.T) {
	cfg := V2Config{Harnesses: map[string]*HarnessConfig{}}
	_, err := cfg.ResolveAgent("cc:opus:2")
	if err == nil {
		t.Fatal("expected error for third colon segment")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %q, want 'not supported'", err.Error())
	}
}

func TestResolveAgent_UserDefinedHarness(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"droid": {
				Command: []string{"droid", "run"},
				Models:  map[string]string{"v1": "droid-v1"},
			},
		},
	}
	resolved, err := cfg.ResolveAgent("droid:v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "droid" {
		t.Errorf("Harness = %q, want 'droid'", resolved.Harness)
	}
	if resolved.Model != "droid-v1" {
		t.Errorf("Model = %q, want 'droid-v1'", resolved.Model)
	}
}

func TestResolveAgent_BuiltInNamedModelUsesCanonicalKey(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"cc": {Models: map[string]string{
				"opus": "claude-opus-4-7",
			}},
		},
	}
	resolved, err := cfg.ResolveAgent("cc:opus")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Harness != "claude" {
		t.Errorf("Harness = %q, want 'claude'", resolved.Harness)
	}
	if resolved.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want 'claude-opus-4-7'", resolved.Model)
	}
}

func TestResolveAgent_DidYouMeanTop3(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"cc": {Models: map[string]string{
				"opus":   "claude-opus-4-7",
				"sonnet": "claude-sonnet-4-6",
				"haiku":  "claude-haiku-3-5",
				"mini":   "claude-mini",
			}},
		},
	}
	_, err := cfg.ResolveAgent("cc:opux")
	if err == nil {
		t.Fatal("expected error for typo")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "opus") {
		t.Errorf("error = %q, should suggest 'opus' as closest", errStr)
	}
}

func TestResolveAgent_NoModelsDefined(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"droid": {Command: []string{"droid"}},
		},
	}
	_, err := cfg.ResolveAgent("droid:fast")
	if err == nil {
		t.Fatal("expected error for unresolved model with no models defined")
	}
	if !strings.Contains(err.Error(), "no models defined") {
		t.Errorf("error = %q, want 'no models defined'", err.Error())
	}
}

func TestLoadV2_DefaultsMixValidatesAtLoad(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
[defaults]
mix = "cc cx"
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("valid mix should load: %v", err)
	}
	if cfg.Defaults.Mix != "cc cx" {
		t.Errorf("Mix = %q, want 'cc cx'", cfg.Defaults.Mix)
	}
}

func TestLoadV2_DefaultsMixInvalidRejects(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
[defaults]
mix = "cc unknown_harness"
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for invalid mix")
	}
	if !strings.Contains(err.Error(), "[defaults].mix") {
		t.Errorf("error = %q, want reference to [defaults].mix", err.Error())
	}
}

func TestLoadV2_DefaultsMixWithNamedModel(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
[defaults]
mix = "cc cx:1"

[harness.cc.models]
opus = "claude-opus-4-7"
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("valid named-model mix should load: %v", err)
	}
	_ = cfg
}

func TestLoadV2_DefaultsMixInvalidModelName(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
[defaults]
mix = "cc:nonexistent"
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for unresolved model name in mix")
	}
	if !strings.Contains(err.Error(), "[defaults].mix") {
		t.Errorf("error = %q, want reference to [defaults].mix", err.Error())
	}
}

func TestResolveAgent_BareAlias_PrefersDefaultsOverRoot(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `claude_model = "root-sonnet"

[defaults]
claude_model = "defaults-opus"
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Model != "defaults-opus" {
		t.Errorf("Model = %q, want 'defaults-opus' (defaults should win)", resolved.Model)
	}
}

func TestResolveAgent_BareAlias_FallsBackToRoot(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `claude_model = "root-sonnet"
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Model != "root-sonnet" {
		t.Errorf("Model = %q, want 'root-sonnet' (root fallback)", resolved.Model)
	}
}

func TestResolveAgent_BareAlias_FallsThroughWhenNothingSet(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Model != "" {
		t.Errorf("Model = %q, want empty when nothing configured", resolved.Model)
	}
}

func TestResolveAgent_UserDefinedHarness_BareAlias_NoModel(t *testing.T) {
	cfg := V2Config{
		Harnesses: map[string]*HarnessConfig{
			"droid": {Command: []string{"droid"}},
		},
	}
	resolved, err := cfg.ResolveAgent("droid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Model != "" {
		t.Errorf("Model = %q, want empty for user-defined bare alias", resolved.Model)
	}
}

func TestLoadV2_RoutesEmptySection(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[routes]
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 with empty routes failed: %v", err)
	}
	if cfg.Routes == nil {
		t.Error("Routes should be non-nil map when [routes] is present but empty")
	}
	if len(cfg.Routes) != 0 {
		t.Errorf("Routes = %v, want empty map", cfg.Routes)
	}
}

func TestLoadV2_RoutesOnlyDefault(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[routes]
default = ["cc", "cx"]
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("Routes has %d entries, want 1", len(cfg.Routes))
	}
	if len(cfg.Routes["default"]) != 2 || cfg.Routes["default"][0] != "cc" || cfg.Routes["default"][1] != "cx" {
		t.Errorf("Routes['default'] = %v, want [cc cx]", cfg.Routes["default"])
	}
}

func TestLoadV2_RoutesOnlyNonDefault(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[routes]
SENIOR = ["cc:opus", "cx"]
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("Routes has %d entries, want 1", len(cfg.Routes))
	}
	if len(cfg.Routes["SENIOR"]) != 2 {
		t.Errorf("Routes['SENIOR'] = %v, want 2 entries", cfg.Routes["SENIOR"])
	}
}

func TestLoadV2_RoutesDuplicateByCaseRejected(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[routes]
SENIOR = ["cc"]
senior = ["cx"]
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for duplicate route keys differing only by case")
	}
	if !strings.Contains(err.Error(), "differ only by case") {
		t.Errorf("error = %q, want 'differ only by case'", err.Error())
	}
}

func TestLoadV2_RoutesRoleNameAsEntryRejected(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[routes]
default = ["cc", "cx"]
SENIOR = ["default"]
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for role name used as entry in [routes]")
	}
	if !strings.Contains(err.Error(), "role name") || !strings.Contains(err.Error(), "--agent") {
		t.Errorf("error = %q, want role name and --agent reference", err.Error())
	}
}

func TestLoadV2_RoutesAbsentEmptyMap(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if cfg.Routes == nil {
		t.Error("Routes should be non-nil empty map when [routes] absent")
	}
	if len(cfg.Routes) != 0 {
		t.Errorf("Routes should be empty, got %v", cfg.Routes)
	}
}

func TestLoadV2_RoutesWithQuotaSyntax(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[routes]
default = ["cc:opus:1", "cx:3", "op:z"]
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if len(cfg.Routes["default"]) != 3 {
		t.Fatalf("Routes['default'] has %d entries, want 3", len(cfg.Routes["default"]))
	}
	if cfg.Routes["default"][0] != "cc:opus:1" {
		t.Errorf("entry[0] = %q, want 'cc:opus:1'", cfg.Routes["default"][0])
	}
}

func TestLoadV2_RoutesRoleNameCaseInsensitiveMatch(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[routes]
DEFAULT = ["cc"]
SENIOR = ["DEFAULT"]
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error: SENIOR references 'DEFAULT' which is a route key (role name)")
	}
	if !strings.Contains(err.Error(), "role name") {
		t.Errorf("error = %q, want 'role name'", err.Error())
	}
}

func TestLoadV2_RoutesCaseInsensitiveKeyCollision(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[routes]
default = ["cc"]
DEFAULT = ["cx"]
`)
	_, err := LoadV2(dir)
	if err == nil {
		t.Fatal("expected error for duplicate route keys differing only by case")
	}
	if !strings.Contains(err.Error(), "differ only by case") {
		t.Errorf("error = %q, want 'differ only by case'", err.Error())
	}
}

func TestLoadV2_RoutesEntryNotMatchingRouteKeyAllowed(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[routes]
default = ["cc", "cx"]
SENIOR = ["cc:opus", "cx"]
`)
	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if len(cfg.Routes) != 2 {
		t.Errorf("Routes has %d entries, want 2", len(cfg.Routes))
	}
}

func TestValidateRoutes_EmptyMapAllowed(t *testing.T) {
	if err := validateRoutes(map[string][]string{}); err != nil {
		t.Errorf("empty routes map should be allowed: %v", err)
	}
}

func TestLoadV2_OpencodeWithoutRunWarning(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[harness.mycode]
command = ["opencode"]
model_flag = "--model"
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	found := false
	for _, note := range cfg.DeprecationNotes {
		if strings.Contains(note, "mycode") && strings.Contains(note, "TUI") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected TUI mode warning for harness 'mycode', got notes: %v", cfg.DeprecationNotes)
	}
}

func TestLoadV2_OpencodeWithRunNoWarning(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[harness.mycode]
command = ["opencode", "run", "$PROMPT", "--format", "json"]
model_flag = "--model"
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	for _, note := range cfg.DeprecationNotes {
		if strings.Contains(note, "TUI") {
			t.Errorf("unexpected TUI mode warning for correct opencode config: %q", note)
		}
	}
}
