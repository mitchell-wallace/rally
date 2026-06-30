package config

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

const ExpectedSchemaVersion = 2

// Default wall-clock budgets for the recovery-role timeout lifecycle. They are
// applied when the corresponding [reliability] key is unset/0, and also used by
// the duration accessors so a zero-value ReliabilityConfig still yields a sane
// effective value for the runner.
const (
	DefaultRunTimeoutSecs     = 4500 // 75m: per-run wall-clock budget across retries
	DefaultTryTimeoutSecs     = 3600 // 60m: secondary per-attempt cap
	DefaultHandoffTimeoutSecs = 300  // 5m: bounded handoff-only resume (not counted in run budget)
	MinReliabilityTimeoutSecs = 300  // 5m: minimum accepted wall-clock budget
)

var harnessNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

var modelNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

var numericOnlyPattern = regexp.MustCompile(`^\d+$`)

var builtInAliases = map[string]string{
	"ag":          "antigravity",
	"agy":         "antigravity",
	"antigravity": "antigravity",
	"cc":          "claude",
	"claude":      "claude",
	"cx":          "codex",
	"codex":       "codex",
	"op":          "opencode",
	"opencode":    "opencode",
}

var builtInHarnessLookupOrder = map[string][]string{
	"antigravity": {"antigravity", "ag", "agy"},
	"claude":      {"claude", "cc"},
	"codex":       {"codex", "cx"},
	"opencode":    {"opencode", "op"},
}

var builtInCanonical = map[string]bool{
	"ag":          true,
	"agy":         true,
	"antigravity": true,
	"cc":          true,
	"cx":          true,
	"op":          true,
	"claude":      true,
	"codex":       true,
	"opencode":    true,
}

type RemovedGeminiAliasError struct {
	Alias string
}

func (e RemovedGeminiAliasError) Error() string {
	return fmt.Sprintf("removed agent alias %q: the gemini CLI harness was removed; configure antigravity (ag) instead", e.Alias)
}

func RemovedGeminiAlias(err error) (string, bool) {
	var removed RemovedGeminiAliasError
	if errors.As(err, &removed) {
		return removed.Alias, true
	}
	return "", false
}

type DefaultsConfig struct {
	Iterations       int    `toml:"iterations,omitempty"`
	Mix              string `toml:"mix,omitempty"`
	ClaudeModel      string `toml:"claude_model,omitempty"`
	CodexModel       string `toml:"codex_model,omitempty"`
	OpenCodeModel    string `toml:"opencode_model,omitempty"`
	AntigravityModel string `toml:"antigravity_model,omitempty"`
}

type LapsConfig struct {
	InstructionsFile string `toml:"instructions_file,omitempty"`
}

type FreeRunConfig struct {
	PromptFile string `toml:"prompt_file,omitempty"`
}

type FallbackConfig = FreeRunConfig

type ReliabilityConfig struct {
	StallThresholdSecs     int  `toml:"stall_threshold_secs,omitempty"`
	LivenessProbe          bool `toml:"liveness_probe,omitempty"`
	RetryBudget            int  `toml:"retry_budget,omitempty"`
	RunTimeoutSecs         int  `toml:"run_timeout_secs,omitempty"`
	TryTimeoutSecs         int  `toml:"try_timeout_secs,omitempty"`
	HandoffTimeoutSecs     int  `toml:"handoff_timeout_secs,omitempty"`
	RecentTryCount         int  `toml:"recent_try_count,omitempty"`
	RecentTryCharLimit     int  `toml:"recent_try_char_limit,omitempty"`
	RecentContextCharLimit int  `toml:"recent_context_char_limit,omitempty"`
}

func (r ReliabilityConfig) StallThreshold() time.Duration {
	if r.StallThresholdSecs > 0 {
		return time.Duration(r.StallThresholdSecs) * time.Second
	}
	return 0
}

// RunTimeout returns the effective per-run wall-clock budget (across retries).
// An unset/zero value yields DefaultRunTimeoutSecs.
func (r ReliabilityConfig) RunTimeout() time.Duration {
	return time.Duration(r.effectiveRunTimeoutSecs()) * time.Second
}

// TryTimeout returns the effective per-attempt cap. An unset/zero value yields
// DefaultTryTimeoutSecs.
func (r ReliabilityConfig) TryTimeout() time.Duration {
	return time.Duration(r.effectiveTryTimeoutSecs()) * time.Second
}

// HandoffTimeout returns the effective bounded handoff-only resume limit. An
// unset/zero value yields DefaultHandoffTimeoutSecs; after LoadV2 the value is
// also clamped below the effective try/run bounds.
func (r ReliabilityConfig) HandoffTimeout() time.Duration {
	return time.Duration(r.effectiveHandoffTimeoutSecs()) * time.Second
}

// effectiveRunTimeoutSecs returns the resolved run budget in seconds.
func (r ReliabilityConfig) effectiveRunTimeoutSecs() int {
	if r.RunTimeoutSecs > 0 {
		if r.RunTimeoutSecs < MinReliabilityTimeoutSecs {
			return MinReliabilityTimeoutSecs
		}
		return r.RunTimeoutSecs
	}
	return DefaultRunTimeoutSecs
}

// effectiveTryTimeoutSecs returns the resolved per-try cap in seconds.
func (r ReliabilityConfig) effectiveTryTimeoutSecs() int {
	if r.TryTimeoutSecs > 0 {
		if r.TryTimeoutSecs < MinReliabilityTimeoutSecs {
			return MinReliabilityTimeoutSecs
		}
		return r.TryTimeoutSecs
	}
	return DefaultTryTimeoutSecs
}

// effectiveHandoffTimeoutSecs returns the resolved handoff window in seconds,
// clamped below the effective try/run bounds so the handoff phase can never
// reach or outlast them.
func (r ReliabilityConfig) effectiveHandoffTimeoutSecs() int {
	h := r.HandoffTimeoutSecs
	if h <= 0 {
		h = DefaultHandoffTimeoutSecs
	}
	if h < MinReliabilityTimeoutSecs {
		h = MinReliabilityTimeoutSecs
	}
	if bound := min(r.effectiveRunTimeoutSecs(), r.effectiveTryTimeoutSecs()); h >= bound {
		if bound > MinReliabilityTimeoutSecs {
			h = bound - 1
		} else {
			h = bound
		}
	}
	return h
}

type HarnessConfig struct {
	Models         map[string]string `toml:"models,omitempty"`
	Command        []string          `toml:"command,omitempty"`
	ModelFlag      *string           `toml:"model_flag"`
	OutputStrategy string            `toml:"output_strategy,omitempty"`
	OutputLines    int               `toml:"output_lines,omitempty"`
	TailStream     string            `toml:"tail_stream,omitempty"`
}

type TelemetryConfig struct {
	Enabled                 *bool  `toml:"enabled,omitempty"`
	NewRelicAppName         string `toml:"new_relic_app_name,omitempty"`
	NewRelicHostDisplayName string `toml:"new_relic_host_display_name,omitempty"`
}

type V2Config struct {
	ClaudeModel          string
	CodexModel           string
	OpenCodeModel        string
	AntigravityModel     string
	SchemaVersion        int
	DataDir              string
	RunHooksOnAutoCommit bool
	LapsInstructions     string

	Defaults    DefaultsConfig
	Laps        LapsConfig
	FreeRun     FreeRunConfig
	Reliability ReliabilityConfig
	Harnesses   map[string]*HarnessConfig
	Routes      map[string][]string
	Reasoning   map[string]string
	Providers   map[string]ProviderConfig
	Telemetry   TelemetryConfig

	DeprecationNotes []string
	SchemaWarning    string
}

type rawFallbackAlias struct {
	InstructionsFile string `toml:"instructions_file,omitempty"`
}

type rawConfig struct {
	ClaudeModel          string `toml:"claude_model,omitempty"`
	CodexModel           string `toml:"codex_model,omitempty"`
	OpenCodeModel        string `toml:"opencode_model,omitempty"`
	AntigravityModel     string `toml:"antigravity_model,omitempty"`
	SchemaVersion        int    `toml:"schema_version,omitempty"`
	DataDir              string `toml:"data_dir,omitempty"`
	RunHooksOnAutoCommit bool   `toml:"run_hooks_on_autocommit"`
	LapsInstructions     string `toml:"laps_instructions,omitempty"`

	Defaults    DefaultsConfig            `toml:"defaults"`
	Laps        LapsConfig                `toml:"laps"`
	FreeRun     FreeRunConfig             `toml:"free_run"`
	Fallback    rawFallbackAlias          `toml:"fallback"`
	Reliability ReliabilityConfig         `toml:"reliability"`
	Harnesses   map[string]*HarnessConfig `toml:"harness"`
	Routes      map[string][]string       `toml:"routes"`
	Reasoning   map[string]string         `toml:"reasoning"`
	Providers   map[string]interface{}    `toml:"providers,omitempty"`
	Telemetry   TelemetryConfig           `toml:"telemetry,omitempty"`
}
