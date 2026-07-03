package config

import (
	"strings"
	"testing"
)

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
[harness.op]
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
