package config

import (
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// SaveV2 writes cfg to the repo-level config file for the workspace. It is kept
// for back-compat; callers that target a specific file (e.g. the user-level
// config) use SaveV2File.
func SaveV2(workspaceDir string, cfg V2Config) error {
	return SaveV2File(V2Path(workspaceDir), cfg)
}

// SaveV2File validates and writes cfg to an explicit path, creating parent
// directories as needed.
func SaveV2File(path string, cfg V2Config) error {
	if cfg.Harnesses == nil {
		cfg.Harnesses = make(map[string]*HarnessConfig)
	}
	if err := validateHarnesses(cfg.Harnesses); err != nil {
		return err
	}
	if cfg.Routes == nil {
		cfg.Routes = make(map[string][]string)
	}
	if err := validateRoutes(cfg.Routes); err != nil {
		return err
	}
	reasoning, err := normalizeReasoning(cfg.Reasoning)
	if err != nil {
		return err
	}
	if _, err := cfg.resolveProviders(); err != nil {
		return err
	}

	raw := rawConfig{
		SchemaVersion:        ExpectedSchemaVersion,
		DataDir:              cfg.DataDir,
		RunHooksOnAutoCommit: cfg.RunHooksOnAutoCommit,
		LapsInstructions:     cfg.LapsInstructions,
		Defaults: DefaultsConfig{
			Iterations:       cfg.Defaults.Iterations,
			Mix:              cfg.Defaults.Mix,
			ClaudeModel:      effectiveModel(cfg.ClaudeModel, cfg.Defaults.ClaudeModel),
			CodexModel:       effectiveModel(cfg.CodexModel, cfg.Defaults.CodexModel),
			OpenCodeModel:    effectiveModel(cfg.OpenCodeModel, cfg.Defaults.OpenCodeModel),
			AntigravityModel: effectiveModel(cfg.AntigravityModel, cfg.Defaults.AntigravityModel),
		},
		Laps:        cfg.Laps,
		FreeRun:     cfg.FreeRun,
		Reliability: cfg.Reliability,
		Harnesses:   cfg.Harnesses,
		Routes:      cfg.Routes,
		Reasoning:   reasoning,
		Providers:   providersToRaw(cfg.Providers),
		Telemetry:   cfg.Telemetry,
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
