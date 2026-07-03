package config

import "testing"

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
