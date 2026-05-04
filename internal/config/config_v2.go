package config

import (
	"errors"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

type V2Config struct {
	ClaudeModel          string `toml:"claude_model,omitempty"`
	CodexModel           string `toml:"codex_model,omitempty"`
	GeminiModel          string `toml:"gemini_model,omitempty"`
	OpenCodeModel        string `toml:"opencode_model,omitempty"`
	RunHooksOnAutoCommit bool   `toml:"run_hooks_on_autocommit"`
	DataDir              string `toml:"data_dir,omitempty"`
}

func V2Path(workspaceDir string) string {
	return filepath.Join(workspaceDir, ".rally", "config.toml")
}

func LoadV2(workspaceDir string) (V2Config, error) {
	path := V2Path(workspaceDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return V2Config{}, nil
		}
		return V2Config{}, err
	}
	var cfg V2Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return V2Config{}, err
	}
	return cfg, nil
}

func SaveV2(workspaceDir string, cfg V2Config) error {
	path := V2Path(workspaceDir)
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
