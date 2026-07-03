package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadV2_LegacyRootModelFields(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `claude_model = "sonnet"
codex_model = "codex-latest"
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

func TestLoadV2_ReliabilityTimeoutDefaults(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
	`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	const (
		wantDefaultRunTimeoutSecs     = 4500
		wantDefaultTryTimeoutSecs     = 3600
		wantDefaultHandoffTimeoutSecs = 300
	)

	if got, want := cfg.Reliability.RunTimeoutSecs, wantDefaultRunTimeoutSecs; got != want {
		t.Errorf("Default RunTimeoutSecs = %d, want %d", got, want)
	}
	if got, want := cfg.Reliability.TryTimeoutSecs, wantDefaultTryTimeoutSecs; got != want {
		t.Errorf("Default TryTimeoutSecs = %d, want %d", got, want)
	}
	if got, want := cfg.Reliability.HandoffTimeoutSecs, wantDefaultHandoffTimeoutSecs; got != want {
		t.Errorf("Default HandoffTimeoutSecs = %d, want %d", got, want)
	}
	if got, want := cfg.Reliability.RunTimeout(), wantDefaultRunTimeoutSecs*time.Second; got != want {
		t.Errorf("RunTimeout() = %v, want %v", got, want)
	}
	if got, want := cfg.Reliability.TryTimeout(), wantDefaultTryTimeoutSecs*time.Second; got != want {
		t.Errorf("TryTimeout() = %v, want %v", got, want)
	}
	if got, want := cfg.Reliability.HandoffTimeout(), wantDefaultHandoffTimeoutSecs*time.Second; got != want {
		t.Errorf("HandoffTimeout() = %v, want %v", got, want)
	}
}

func TestLoadV2_ReliabilityTimeoutExplicitZeroYieldsDefaults(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[reliability]
run_timeout_secs = 0
try_timeout_secs = 0
handoff_timeout_secs = 0
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	if got, want := cfg.Reliability.RunTimeoutSecs, DefaultRunTimeoutSecs; got != want {
		t.Errorf("RunTimeoutSecs = %d, want default %d", got, want)
	}
	if got, want := cfg.Reliability.TryTimeoutSecs, DefaultTryTimeoutSecs; got != want {
		t.Errorf("TryTimeoutSecs = %d, want default %d", got, want)
	}
	if got, want := cfg.Reliability.HandoffTimeoutSecs, DefaultHandoffTimeoutSecs; got != want {
		t.Errorf("HandoffTimeoutSecs = %d, want default %d", got, want)
	}
}

func TestLoadV2_ReliabilityTimeoutConfigured(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[reliability]
run_timeout_secs = 1800
try_timeout_secs = 1500
handoff_timeout_secs = 300
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	if got, want := cfg.Reliability.RunTimeoutSecs, 1800; got != want {
		t.Errorf("RunTimeoutSecs = %d, want %d", got, want)
	}
	if got, want := cfg.Reliability.TryTimeoutSecs, 1500; got != want {
		t.Errorf("TryTimeoutSecs = %d, want %d", got, want)
	}
	if got, want := cfg.Reliability.HandoffTimeoutSecs, 300; got != want {
		t.Errorf("HandoffTimeoutSecs = %d, want %d", got, want)
	}
	if got, want := cfg.Reliability.RunTimeout(), 1800*time.Second; got != want {
		t.Errorf("RunTimeout() = %v, want %v", got, want)
	}
	if got, want := cfg.Reliability.TryTimeout(), 1500*time.Second; got != want {
		t.Errorf("TryTimeout() = %v, want %v", got, want)
	}
	if got, want := cfg.Reliability.HandoffTimeout(), 300*time.Second; got != want {
		t.Errorf("HandoffTimeout() = %v, want %v", got, want)
	}
}

func TestLoadV2_ReliabilityTimeoutBelowMinimumRoundedUp(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[reliability]
run_timeout_secs = 1
try_timeout_secs = 2
handoff_timeout_secs = 3
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	if got, want := cfg.Reliability.RunTimeoutSecs, MinReliabilityTimeoutSecs; got != want {
		t.Errorf("RunTimeoutSecs = %d, want rounded minimum %d", got, want)
	}
	if got, want := cfg.Reliability.TryTimeoutSecs, MinReliabilityTimeoutSecs; got != want {
		t.Errorf("TryTimeoutSecs = %d, want rounded minimum %d", got, want)
	}
	if got, want := cfg.Reliability.HandoffTimeoutSecs, MinReliabilityTimeoutSecs; got != want {
		t.Errorf("HandoffTimeoutSecs = %d, want rounded minimum %d", got, want)
	}
	assertReliabilityNote(t, cfg, "run_timeout_secs")
	assertReliabilityNote(t, cfg, "try_timeout_secs")
	assertReliabilityNote(t, cfg, "handoff_timeout_secs")
}

func TestLoadV2_ReliabilityHandoffClampedToTry(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[reliability]
run_timeout_secs = 5000
try_timeout_secs = 600
handoff_timeout_secs = 600
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	// handoff == try (the tighter bound) must be clamped strictly below it.
	if got, want := cfg.Reliability.TryTimeoutSecs, 600; got != want {
		t.Fatalf("TryTimeoutSecs = %d, want %d", got, want)
	}
	if got, want := cfg.Reliability.HandoffTimeoutSecs, 599; got != want {
		t.Errorf("HandoffTimeoutSecs = %d, want %d (clamped below try bound)", got, want)
	}
	if cfg.Reliability.HandoffTimeout() >= cfg.Reliability.TryTimeout() {
		t.Errorf("HandoffTimeout() %v >= TryTimeout() %v", cfg.Reliability.HandoffTimeout(), cfg.Reliability.TryTimeout())
	}
	assertReliabilityNote(t, cfg, "handoff_timeout_secs")
}

func TestLoadV2_ReliabilityHandoffClampedToRun(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2

[reliability]
run_timeout_secs = 400
try_timeout_secs = 5000
handoff_timeout_secs = 450
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	// run is the tighter bound here; handoff must be clamped below it.
	if got, want := cfg.Reliability.RunTimeoutSecs, 400; got != want {
		t.Fatalf("RunTimeoutSecs = %d, want %d", got, want)
	}
	if got, want := cfg.Reliability.HandoffTimeoutSecs, 399; got != want {
		t.Errorf("HandoffTimeoutSecs = %d, want %d (clamped below run bound)", got, want)
	}
	if cfg.Reliability.HandoffTimeout() >= cfg.Reliability.RunTimeout() {
		t.Errorf("HandoffTimeout() %v >= RunTimeout() %v", cfg.Reliability.HandoffTimeout(), cfg.Reliability.RunTimeout())
	}
	assertReliabilityNote(t, cfg, "handoff_timeout_secs")
}

func TestLoadV2_ReliabilityTryAtOrAboveRunAccepted(t *testing.T) {
	for _, tc := range []struct {
		name string
		toml string
	}{
		{
			name: "try equals run",
			toml: `schema_version = 2

[reliability]
run_timeout_secs = 1000
try_timeout_secs = 1000
handoff_timeout_secs = 100
`,
		},
		{
			name: "try greater than run",
			toml: `schema_version = 2

[reliability]
run_timeout_secs = 1000
try_timeout_secs = 2000
handoff_timeout_secs = 100
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, tc.toml)

			cfg, err := LoadV2(dir)
			if err != nil {
				t.Fatalf("LoadV2 failed (try >= run should be accepted): %v", err)
			}

			// Configured values are preserved as-is; the run budget subsumes the
			// per-try cap rather than producing an error.
			if got, want := cfg.Reliability.RunTimeoutSecs, 1000; got != want {
				t.Errorf("RunTimeoutSecs = %d, want %d", got, want)
			}
			if got := cfg.Reliability.TryTimeoutSecs; got != 1000 && got != 2000 {
				t.Errorf("TryTimeoutSecs = %d, want configured value", got)
			}
			if got, want := cfg.Reliability.HandoffTimeoutSecs, 300; got != want {
				t.Errorf("HandoffTimeoutSecs = %d, want %d", got, want)
			}
		})
	}
}

func TestReliabilityAccessorsOnZeroValue(t *testing.T) {
	// A zero-value ReliabilityConfig (not run through LoadV2) must still report
	// the effective defaults and clamp the handoff window below them.
	r := ReliabilityConfig{}

	if got, want := r.RunTimeout(), DefaultRunTimeoutSecs*time.Second; got != want {
		t.Errorf("zero RunTimeout() = %v, want %v", got, want)
	}
	if got, want := r.TryTimeout(), DefaultTryTimeoutSecs*time.Second; got != want {
		t.Errorf("zero TryTimeout() = %v, want %v", got, want)
	}
	if got, want := r.HandoffTimeout(), DefaultHandoffTimeoutSecs*time.Second; got != want {
		t.Errorf("zero HandoffTimeout() = %v, want %v", got, want)
	}
	if r.HandoffTimeout() >= r.TryTimeout() || r.HandoffTimeout() >= r.RunTimeout() {
		t.Errorf("HandoffTimeout() %v not strictly below try/run bounds", r.HandoffTimeout())
	}
}

func assertReliabilityNote(t *testing.T, cfg V2Config, needle string) {
	t.Helper()
	found := false
	for _, note := range cfg.DeprecationNotes {
		if strings.Contains(note, needle) && (strings.Contains(note, "clamped") || strings.Contains(note, "rounded up")) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a reliability note mentioning %q, got notes: %v", needle, cfg.DeprecationNotes)
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

func TestLoadV2_LapsAndFreeRun(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
[laps]
instructions_file = ".rally/laps_instructions.md"
[free_run]
prompt_file = ".rally/free_run_prompt.md"
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if cfg.Laps.InstructionsFile != ".rally/laps_instructions.md" {
		t.Errorf("Laps.InstructionsFile = %q, wrong", cfg.Laps.InstructionsFile)
	}
	if cfg.FreeRun.PromptFile != ".rally/free_run_prompt.md" {
		t.Errorf("FreeRun.PromptFile = %q, wrong", cfg.FreeRun.PromptFile)
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

func TestLoadV2_FreeRunLegacyFallbackAlias(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
[fallback]
instructions_file = ".rally/legacy_prompt.md"
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if cfg.FreeRun.PromptFile != ".rally/legacy_prompt.md" {
		t.Errorf("FreeRun.PromptFile = %q, want %q (from legacy [fallback])", cfg.FreeRun.PromptFile, ".rally/legacy_prompt.md")
	}
	found := false
	for _, note := range cfg.DeprecationNotes {
		if strings.Contains(note, "[fallback]") && strings.Contains(note, "deprecated") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected deprecation note for [fallback], got: %v", cfg.DeprecationNotes)
	}
}

func TestLoadV2_FreeRunNewKeyPreferredOverLegacy(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
[free_run]
prompt_file = ".rally/new_prompt.md"
[fallback]
instructions_file = ".rally/legacy_prompt.md"
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if cfg.FreeRun.PromptFile != ".rally/new_prompt.md" {
		t.Errorf("FreeRun.PromptFile = %q, want new key value (new key wins over legacy)", cfg.FreeRun.PromptFile)
	}
}

func TestLoadV2_FreeRunAbsent(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `schema_version = 2
`)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}
	if cfg.FreeRun.PromptFile != "" {
		t.Errorf("FreeRun.PromptFile = %q, want empty when absent", cfg.FreeRun.PromptFile)
	}
}

func TestLoadV2_RejectsNewRelicLicenseKey(t *testing.T) {
	content := `schema_version = 2
[telemetry]
new_relic_license_key = "secret-key"
new_relic_app_name = "test-app"
`
	dir := t.TempDir()
	writeConfig(t, dir, content)

	cfg, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("LoadV2 failed: %v", err)
	}

	if cfg.Telemetry.NewRelicAppName != "test-app" {
		t.Errorf("expected new_relic_app_name = test-app, got %q", cfg.Telemetry.NewRelicAppName)
	}

	// TOML parser successfully ignored 'new_relic_license_key' as it is not present in TelemetryConfig.
}
