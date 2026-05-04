package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

const ExpectedSchemaVersion = 2

type DefaultsConfig struct {
	Iterations    int    `toml:"iterations,omitempty"`
	Mix           string `toml:"mix,omitempty"`
	ClaudeModel   string `toml:"claude_model,omitempty"`
	CodexModel    string `toml:"codex_model,omitempty"`
	GeminiModel   string `toml:"gemini_model,omitempty"`
	OpenCodeModel string `toml:"opencode_model,omitempty"`
}

type MicrobeadsConfig struct {
	InstructionsFile string `toml:"instructions_file,omitempty"`
}

type FallbackConfig struct {
	InstructionsFile string `toml:"instructions_file,omitempty"`
}

type HarnessConfig struct {
	Models         map[string]string `toml:"models,omitempty"`
	Command        []string          `toml:"command,omitempty"`
	ModelFlag      *string           `toml:"model_flag"`
	OutputStrategy string            `toml:"output_strategy,omitempty"`
	OutputLines    int               `toml:"output_lines,omitempty"`
	TailStream     string            `toml:"tail_stream,omitempty"`
}

type V2Config struct {
	ClaudeModel          string
	CodexModel           string
	GeminiModel          string
	OpenCodeModel        string
	SchemaVersion        int
	DataDir              string
	RunHooksOnAutoCommit bool
	LapsInstructions     string

	Defaults   DefaultsConfig
	Microbeads MicrobeadsConfig
	Fallback   FallbackConfig
	Harnesses  map[string]*HarnessConfig

	DeprecationNotes []string
	SchemaWarning    string
}

type rawConfig struct {
	ClaudeModel          string `toml:"claude_model,omitempty"`
	CodexModel           string `toml:"codex_model,omitempty"`
	GeminiModel          string `toml:"gemini_model,omitempty"`
	OpenCodeModel        string `toml:"opencode_model,omitempty"`
	SchemaVersion        int    `toml:"schema_version,omitempty"`
	DataDir              string `toml:"data_dir,omitempty"`
	RunHooksOnAutoCommit bool   `toml:"run_hooks_on_autocommit"`
	LapsInstructions     string `toml:"laps_instructions,omitempty"`

	Defaults   DefaultsConfig            `toml:"defaults"`
	Microbeads MicrobeadsConfig          `toml:"microbeads"`
	Fallback   FallbackConfig            `toml:"fallback"`
	Harnesses  map[string]*HarnessConfig `toml:"harness"`
}

func V2Path(workspaceDir string) string {
	return filepath.Join(workspaceDir, ".rally", "config.toml")
}

func LoadV2(workspaceDir string) (V2Config, error) {
	path := V2Path(workspaceDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
		return V2Config{Harnesses: make(map[string]*HarnessConfig)}, nil
	}
		return V2Config{}, err
	}

	var raw rawConfig
	if err := toml.Unmarshal(data, &raw); err != nil {
		return V2Config{}, err
	}

	cfg := V2Config{
		SchemaVersion:        raw.SchemaVersion,
		DataDir:              raw.DataDir,
		RunHooksOnAutoCommit: raw.RunHooksOnAutoCommit,
		LapsInstructions:     raw.LapsInstructions,
		Defaults:             raw.Defaults,
		Microbeads:           raw.Microbeads,
		Fallback:             raw.Fallback,
		Harnesses:            raw.Harnesses,
	}

	if cfg.Harnesses == nil {
		cfg.Harnesses = make(map[string]*HarnessConfig)
	}

	resolveModel := func(rootVal, defaultsVal string) (string, bool) {
		if defaultsVal != "" {
			return defaultsVal, rootVal != ""
		}
		if rootVal != "" {
			return rootVal, true
		}
		return "", false
	}

	type modelField struct {
		name      string
		rootVal   string
		defaults  string
		assign    func(string)
	}
	fields := []modelField{
		{"claude_model", raw.ClaudeModel, raw.Defaults.ClaudeModel, func(v string) { cfg.ClaudeModel = v }},
		{"codex_model", raw.CodexModel, raw.Defaults.CodexModel, func(v string) { cfg.CodexModel = v }},
		{"gemini_model", raw.GeminiModel, raw.Defaults.GeminiModel, func(v string) { cfg.GeminiModel = v }},
		{"opencode_model", raw.OpenCodeModel, raw.Defaults.OpenCodeModel, func(v string) { cfg.OpenCodeModel = v }},
	}
	for _, f := range fields {
		val, deprecated := resolveModel(f.rootVal, f.defaults)
		f.assign(val)
		if deprecated {
			cfg.DeprecationNotes = append(cfg.DeprecationNotes,
				fmt.Sprintf("config: root-level %s is deprecated; use [defaults].%s instead", f.name, f.name))
		}
	}

	if raw.SchemaVersion != 0 && raw.SchemaVersion != ExpectedSchemaVersion {
		cfg.SchemaWarning = fmt.Sprintf(
			"config: schema_version is %d, expected %d — proceed with caution",
			raw.SchemaVersion, ExpectedSchemaVersion)
	}

	return cfg, nil
}

func SaveV2(workspaceDir string, cfg V2Config) error {
	path := V2Path(workspaceDir)

	raw := rawConfig{
		SchemaVersion:        ExpectedSchemaVersion,
		DataDir:              cfg.DataDir,
		RunHooksOnAutoCommit: cfg.RunHooksOnAutoCommit,
		LapsInstructions:     cfg.LapsInstructions,
		Defaults: DefaultsConfig{
			Iterations:    cfg.Defaults.Iterations,
			Mix:           cfg.Defaults.Mix,
			ClaudeModel:   effectiveModel(cfg.ClaudeModel, cfg.Defaults.ClaudeModel),
			CodexModel:    effectiveModel(cfg.CodexModel, cfg.Defaults.CodexModel),
			GeminiModel:   effectiveModel(cfg.GeminiModel, cfg.Defaults.GeminiModel),
			OpenCodeModel: effectiveModel(cfg.OpenCodeModel, cfg.Defaults.OpenCodeModel),
		},
		Microbeads: cfg.Microbeads,
		Fallback:   cfg.Fallback,
		Harnesses:  cfg.Harnesses,
	}

	data, err := toml.Marshal(raw)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func effectiveModel(topLevel, defaults string) string {
	if defaults != "" {
		return defaults
	}
	return topLevel
}
