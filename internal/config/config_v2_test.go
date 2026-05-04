package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	rallyDir := filepath.Join(dir, ".rally")
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

func TestLoadV2_MicrobeadsAndFallback(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
[microbeads]
instructions_file = ".rally/mb_instructions.md"
[fallback]
instructions_file = ".rally/fallback_instructions.md"
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if cfg.Microbeads.InstructionsFile != ".rally/mb_instructions.md" {
		t.Errorf("Microbeads.InstructionsFile = %q, wrong", cfg.Microbeads.InstructionsFile)
	}
	if cfg.Fallback.InstructionsFile != ".rally/fallback_instructions.md" {
		t.Errorf("Fallback.InstructionsFile = %q, wrong", cfg.Fallback.InstructionsFile)
	}
}

func TestSaveV2_WritesNewShapeOnly(t *testing.T) {
	dir := t.TempDir()
	rallyDir := filepath.Join(dir, ".rally")
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
	rallyDir := filepath.Join(dir, ".rally")
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
	rallyDir := filepath.Join(dir, ".rally")
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
