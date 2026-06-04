package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/store"
)

const ExpectedSchemaVersion = 2

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
	"ge":          "gemini",
	"gemini":      "gemini",
	"op":          "opencode",
	"opencode":    "opencode",
}

var builtInCanonical = map[string]bool{
	"ag":          true,
	"agy":         true,
	"antigravity": true,
	"cc":          true,
	"cx":          true,
	"ge":          true,
	"op":          true,
	"claude":      true,
	"codex":       true,
	"gemini":      true,
	"opencode":    true,
}

type DefaultsConfig struct {
	Iterations       int    `toml:"iterations,omitempty"`
	Mix              string `toml:"mix,omitempty"`
	ClaudeModel      string `toml:"claude_model,omitempty"`
	CodexModel       string `toml:"codex_model,omitempty"`
	GeminiModel      string `toml:"gemini_model,omitempty"`
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

type HarnessConfig struct {
	Models         map[string]string `toml:"models,omitempty"`
	Command        []string          `toml:"command,omitempty"`
	ModelFlag      *string           `toml:"model_flag"`
	OutputStrategy string            `toml:"output_strategy,omitempty"`
	OutputLines    int               `toml:"output_lines,omitempty"`
	TailStream     string            `toml:"tail_stream,omitempty"`
}

type TelemetryConfig struct {
	SentryDSN string `toml:"sentry_dsn,omitempty"`
}

type V2Config struct {
	ClaudeModel          string
	CodexModel           string
	GeminiModel          string
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
	GeminiModel          string `toml:"gemini_model,omitempty"`
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
	Telemetry   TelemetryConfig           `toml:"telemetry,omitempty"`
}

func V2Path(workspaceDir string) string {
	return store.ConfigPath(workspaceDir)
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
		Laps:                 raw.Laps,
		FreeRun:              raw.FreeRun,
		Reliability:          raw.Reliability,
		Harnesses:            raw.Harnesses,
		Routes:               raw.Routes,
		Telemetry:            raw.Telemetry,
	}

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
		{"gemini_model", raw.GeminiModel, raw.Defaults.GeminiModel, func(v string) { cfg.GeminiModel = v }},
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

func validateHarnesses(harnesses map[string]*HarnessConfig) error {
	for name, h := range harnesses {
		if !harnessNamePattern.MatchString(name) {
			return fmt.Errorf("config: invalid harness name %q: must match ^[A-Za-z][A-Za-z0-9_-]*$", name)
		}
		if builtInCanonical[name] {
			if err := validateBuiltInHarness(name, h); err != nil {
				return err
			}
		}
		if len(h.Command) > 0 {
			for _, elem := range h.Command {
				if strings.Contains(elem, "$MODEL") {
					return fmt.Errorf("config: harness %q command contains $MODEL; use model_flag instead of $MODEL placeholder", name)
				}
			}
		}
		if h.OutputStrategy != "" && h.OutputStrategy != "tail" {
			return fmt.Errorf("config: harness %q output_strategy %q is not supported; only \"tail\" is accepted in this version", name, h.OutputStrategy)
		}
		if h.TailStream != "" {
			switch h.TailStream {
			case "stdout", "stderr", "combined":
			default:
				return fmt.Errorf("config: harness %q tail_stream %q is invalid; must be one of stdout, stderr, combined", name, h.TailStream)
			}
		}
		for modelName, modelString := range h.Models {
			if !modelNamePattern.MatchString(modelName) || numericOnlyPattern.MatchString(modelName) {
				return fmt.Errorf("config: harness %q model name %q is invalid: must be a non-numeric identifier matching ^[A-Za-z][A-Za-z0-9_-]*$", name, modelName)
			}
			if modelString == "" {
				return fmt.Errorf("config: harness %q model name %q has an empty model string", name, modelName)
			}
		}
	}
	return nil
}

func validateBuiltInHarness(name string, h *HarnessConfig) error {
	if len(h.Command) > 0 {
		return fmt.Errorf("config: built-in harness %q cannot declare command", name)
	}
	if h.ModelFlag != nil {
		return fmt.Errorf("config: built-in harness %q cannot declare model_flag", name)
	}
	if h.OutputStrategy != "" {
		return fmt.Errorf("config: built-in harness %q cannot declare output_strategy", name)
	}
	if h.TailStream != "" {
		return fmt.Errorf("config: built-in harness %q cannot declare tail_stream", name)
	}
	return nil
}

func validateRoutes(routes map[string][]string) error {
	lowerSeen := map[string]string{}
	for key := range routes {
		lower := strings.ToLower(key)
		if prev, exists := lowerSeen[lower]; exists {
			return fmt.Errorf("config: duplicate route keys %q and %q differ only by case", prev, key)
		}
		lowerSeen[lower] = key
	}

	lowerRouteKeys := map[string]bool{}
	for key := range routes {
		lowerRouteKeys[strings.ToLower(key)] = true
	}

	for key, entries := range routes {
		for _, entry := range entries {
			parts := strings.Split(entry, ":")
			idPart := parts[0]
			if lowerRouteKeys[strings.ToLower(idPart)] {
				return fmt.Errorf("config: route %q references role name %q as an entry; role names are only valid in --agent, not in [routes]", key, idPart)
			}
		}
	}

	return nil
}

func ValidateRoutesTable(routes map[string][]string) error {
	return validateRoutes(routes)
}

func (c V2Config) defaultModelForHarness(harness string) string {
	switch harness {
	case "claude":
		return c.ClaudeModel
	case "codex":
		return c.CodexModel
	case "gemini":
		return c.GeminiModel
	case "opencode":
		return c.OpenCodeModel
	case "antigravity":
		return c.AntigravityModel
	default:
		return ""
	}
}

func (c V2Config) ResolveAgent(spec string) (agent.ResolvedAgent, error) {
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) == 3 {
		return agent.ResolvedAgent{}, fmt.Errorf("invalid agent spec %q: weight-on-named-model (e.g. cc:opus:2) is not supported", spec)
	}

	alias := parts[0]
	harness, ok := builtInAliases[alias]
	if !ok {
		if c.Harnesses != nil {
			if _, userOk := c.Harnesses[alias]; userOk {
				harness = alias
				ok = true
			}
		}
	}
	if !ok {
		return agent.ResolvedAgent{}, fmt.Errorf("unknown agent alias %q", alias)
	}

	if len(parts) == 1 {
		return agent.ResolvedAgent{Harness: harness, Model: c.defaultModelForHarness(harness)}, nil
	}

	right := parts[1]
	if numericOnlyPattern.MatchString(right) {
		return agent.ResolvedAgent{Harness: harness, Model: c.defaultModelForHarness(harness)}, nil
	}

	if modelNamePattern.MatchString(right) {
		hc, found := c.Harnesses[harness]
		if !found {
			hc, found = c.Harnesses[alias]
		}
		if found && hc.Models != nil {
			if modelStr, modelOk := hc.Models[right]; modelOk {
				return agent.ResolvedAgent{Harness: harness, Model: modelStr}, nil
			}
		}
		suggestions := didYouMean(right, modelNamesForHarness(c.Harnesses, harness, alias))
		if suggestions != "" {
			return agent.ResolvedAgent{}, fmt.Errorf("unknown model %q for harness %q; did you mean %s?", right, harness, suggestions)
		}
		return agent.ResolvedAgent{}, fmt.Errorf("unknown model %q for harness %q (no models defined for this harness)", right, harness)
	}

	return agent.ResolvedAgent{Harness: harness, Model: right}, nil
}

func modelNamesForHarness(harnesses map[string]*HarnessConfig, harness string, alias string) []string {
	names := []string{}
	seen := map[string]bool{}
	for _, key := range []string{harness, alias} {
		if key == "" {
			continue
		}
		if h, ok := harnesses[key]; ok && h.Models != nil {
			for k := range h.Models {
				if !seen[k] {
					names = append(names, k)
					seen[k] = true
				}
			}
		}
	}
	return names
}

func didYouMean(target string, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	type scored struct {
		name  string
		score int
	}
	var ranked []scored
	for _, c := range candidates {
		d := levenshtein(target, c)
		ranked = append(ranked, scored{c, d})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score < ranked[j].score
	})
	maxSuggestions := 3
	if len(ranked) < maxSuggestions {
		maxSuggestions = len(ranked)
	}
	top := make([]string, maxSuggestions)
	for i := 0; i < maxSuggestions; i++ {
		top[i] = ranked[i].name
	}
	return strings.Join(top, ", ")
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func min(vals ...int) int {
	m := math.MaxInt
	for _, v := range vals {
		if v < m {
			m = v
		}
	}
	return m
}

func SaveV2(workspaceDir string, cfg V2Config) error {
	path := V2Path(workspaceDir)

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
			GeminiModel:      effectiveModel(cfg.GeminiModel, cfg.Defaults.GeminiModel),
			OpenCodeModel:    effectiveModel(cfg.OpenCodeModel, cfg.Defaults.OpenCodeModel),
			AntigravityModel: effectiveModel(cfg.AntigravityModel, cfg.Defaults.AntigravityModel),
		},
		Laps:        cfg.Laps,
		FreeRun:     cfg.FreeRun,
		Reliability: cfg.Reliability,
		Harnesses:   cfg.Harnesses,
		Routes:      cfg.Routes,
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

func effectiveModel(topLevel, defaults string) string {
	if defaults != "" {
		return defaults
	}
	return topLevel
}
