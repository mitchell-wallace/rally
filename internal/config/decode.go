package config

import (
	"fmt"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

func decodeV2(data []byte) (V2Config, error) {
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
		Laps:                 raw.Laps,
		FreeRun:              raw.FreeRun,
		Reliability:          raw.Reliability,
		Harnesses:            raw.Harnesses,
		Routes:               raw.Routes,
		Reasoning:            raw.Reasoning,
		Telemetry:            raw.Telemetry,
	}

	reasoning, err := normalizeReasoning(cfg.Reasoning)
	if err != nil {
		return V2Config{}, err
	}
	cfg.Reasoning = reasoning

	if raw.Fallback.InstructionsFile != "" {
		if cfg.FreeRun.PromptFile == "" {
			cfg.FreeRun.PromptFile = raw.Fallback.InstructionsFile
		}
		cfg.DeprecationNotes = append(cfg.DeprecationNotes,
			"config: [fallback] instructions_file is deprecated; use [free_run] prompt_file instead")
	}

	if cfg.Reliability.StallThresholdSecs == 0 {
		// 900s (15m): a global threshold that avoids false "slowing"/stall signals
		// during multi-minute reasoning bursts from models like opus/glm/kimi/qwen/
		// deepseek. The slowing indicator (0.6× threshold) now fires at ~9m of log
		// silence, so both the warning and the kill move together from one knob.
		//
		// Trade-off accepted: opencode runs that finish fast (~25-30s) then hold the
		// process open now idle ~15m before the stall reaps them. A future
		// completion-detection change (early opencode process reaping) will remove
		// this trade-off. The `DefaultStallThreshold` constant in the reliability
		// package stays at 180s as a bare-code fallback when no config is loaded.
		cfg.Reliability.StallThresholdSecs = 900
	}
	if cfg.Reliability.RetryBudget == 0 {
		cfg.Reliability.RetryBudget = 5
	}
	if cfg.Reliability.RecentTryCount == 0 {
		cfg.Reliability.RecentTryCount = 5
	}

	// Resolve the recovery-role timeout budgets: 0/unset yields the defaults
	// (4500s run, 3600s try, 300s handoff). When try_timeout_secs >=
	// run_timeout_secs the run budget subsumes the per-try cap, so the config
	// is accepted rather than erroring. The handoff window is clamped below the
	// effective try/run bounds so it can never reach or outlast them.
	rawRun := cfg.Reliability.RunTimeoutSecs
	rawTry := cfg.Reliability.TryTimeoutSecs
	rawHandoff := cfg.Reliability.HandoffTimeoutSecs
	cfg.Reliability.RunTimeoutSecs = cfg.Reliability.effectiveRunTimeoutSecs()
	cfg.Reliability.TryTimeoutSecs = cfg.Reliability.effectiveTryTimeoutSecs()
	cfg.Reliability.HandoffTimeoutSecs = cfg.Reliability.effectiveHandoffTimeoutSecs()
	for _, rounded := range []struct {
		key string
		raw int
		got int
	}{
		{"run_timeout_secs", rawRun, cfg.Reliability.RunTimeoutSecs},
		{"try_timeout_secs", rawTry, cfg.Reliability.TryTimeoutSecs},
		{"handoff_timeout_secs", rawHandoff, cfg.Reliability.HandoffTimeoutSecs},
	} {
		if rounded.raw > 0 && rounded.raw < MinReliabilityTimeoutSecs {
			cfg.DeprecationNotes = append(cfg.DeprecationNotes,
				fmt.Sprintf("config: [reliability].%s=%d was rounded up to %d because timeout budgets below 5 minutes are not accepted", rounded.key, rounded.raw, rounded.got))
		}
	}
	if rawHandoff != cfg.Reliability.HandoffTimeoutSecs && rawHandoff >= MinReliabilityTimeoutSecs {
		cfg.DeprecationNotes = append(cfg.DeprecationNotes,
			fmt.Sprintf("config: [reliability].handoff_timeout_secs=%d was clamped to %d to fit within the effective try/run timeout bounds while preserving the 5-minute minimum", rawHandoff, cfg.Reliability.HandoffTimeoutSecs))
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
		name     string
		rootVal  string
		defaults string
		assign   func(string)
	}
	fields := []modelField{
		{"claude_model", raw.ClaudeModel, raw.Defaults.ClaudeModel, func(v string) { cfg.ClaudeModel = v }},
		{"codex_model", raw.CodexModel, raw.Defaults.CodexModel, func(v string) { cfg.CodexModel = v }},
		{"opencode_model", raw.OpenCodeModel, raw.Defaults.OpenCodeModel, func(v string) { cfg.OpenCodeModel = v }},
		{"antigravity_model", raw.AntigravityModel, raw.Defaults.AntigravityModel, func(v string) { cfg.AntigravityModel = v }},
	}
	for _, f := range fields {
		val, deprecated := resolveModel(f.rootVal, f.defaults)
		f.assign(val)
		if deprecated {
			cfg.DeprecationNotes = append(cfg.DeprecationNotes,
				fmt.Sprintf("config: root-level %s is deprecated; use [defaults].%s instead", f.name, f.name))
		}
	}

	if err := validateHarnesses(cfg.Harnesses); err != nil {
		return V2Config{}, err
	}
	cfg.DeprecationNotes = append(cfg.DeprecationNotes, harnessConfigWarnings(cfg.Harnesses)...)

	if cfg.Routes == nil {
		cfg.Routes = make(map[string][]string)
	}

	if err := validateRoutes(cfg.Routes); err != nil {
		return V2Config{}, err
	}

	providers, err := parseProviders(raw.Providers)
	if err != nil {
		return V2Config{}, err
	}
	cfg.Providers = providers
	if _, err := cfg.resolveProviders(); err != nil {
		return V2Config{}, err
	}

	if raw.Defaults.Mix != "" {
		for _, commaPart := range strings.Split(raw.Defaults.Mix, ",") {
			for _, token := range strings.Fields(strings.TrimSpace(commaPart)) {
				if _, err := cfg.ResolveAgent(token); err != nil {
					return V2Config{}, fmt.Errorf("config: [defaults].mix: %w", err)
				}
			}
		}
	}

	if raw.SchemaVersion != 0 && raw.SchemaVersion != ExpectedSchemaVersion {
		cfg.SchemaWarning = fmt.Sprintf(
			"config: schema_version is %d, expected %d — proceed with caution",
			raw.SchemaVersion, ExpectedSchemaVersion)
	}

	return cfg, nil
}

// harnessConfigWarnings returns non-fatal warnings about harness configuration
// that indicate likely misconfigurations but are not hard errors.
func harnessConfigWarnings(harnesses map[string]*HarnessConfig) []string {
	var warnings []string
	for name, h := range harnesses {
		if len(h.Command) > 0 && h.Command[0] == "opencode" {
			hasRun := len(h.Command) > 1 && h.Command[1] == "run"
			if !hasRun {
				warnings = append(warnings,
					fmt.Sprintf("config: harness %q command is [\"opencode\"] without \"run\" subcommand — this starts opencode in TUI mode, which will not exit cleanly; use [\"opencode\", \"run\", \"$PROMPT\", \"--format\", \"json\"] or the built-in \"op\" alias instead", name))
			}
		}
	}
	return warnings
}
