package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/store"
)

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

func TestSaveV2_PreservesReasoning(t *testing.T) {
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := V2Config{
		Reasoning: map[string]string{
			"VERIFY": "g55-xh",
		},
	}

	if err := SaveV2(dir, cfg); err != nil {
		t.Fatalf("SaveV2 failed: %v", err)
	}

	cfg2, err := LoadV2(dir)
	if err != nil {
		t.Fatalf("re-load failed: %v", err)
	}
	if cfg2.Reasoning["verify"] != "g55-xh" {
		t.Fatalf("Reasoning[verify] = %q, want g55-xh", cfg2.Reasoning["verify"])
	}
}
