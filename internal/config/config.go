package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

type Config struct {
	ClaudeModel   string `toml:"claude_model,omitempty"`
	CodexModel    string `toml:"codex_model,omitempty"`
	GeminiModel   string `toml:"gemini_model,omitempty"`
	OpenCodeModel string `toml:"opencode_model,omitempty"`
	Beads         string `toml:"beads,omitempty"`
}

func WorkspacePath(workspaceDir string) string {
	return filepath.Join(workspaceDir, "rally.toml")
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.Beads = normalizeBeads(cfg.Beads)
	return cfg, nil
}

func LoadWorkspace(workspaceDir string) (Config, error) {
	return Load(WorkspacePath(workspaceDir))
}

func Save(path string, cfg Config) error {
	cfg.Beads = normalizeBeads(cfg.Beads)
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func Update(path string, update func(*Config)) error {
	cfg, err := Load(path)
	if err != nil {
		return err
	}
	update(&cfg)
	return Save(path, cfg)
}

func normalizeBeads(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "true", "false", "auto":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return ""
	}
}
