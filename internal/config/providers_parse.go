package config

import (
	"fmt"
	"strings"
)

// ProviderConfig is a user-defined quota group: a set of runners (named model
// shortcuts, harness:model specs, or wildcard specs) that share a single
// usage-limit budget, plus an operator switch to disable them all at once. When
// any member of a provider hits a usage limit, every sibling is benched until
// the reset so Rally does not burn retries on models that draw from the same
// exhausted account.
//
// Exclude optionally removes runners from the group after the Models specs
// expand. It uses the same spec forms as Models, so a wildcard like codex:* can
// pull in every configured codex model while an Exclude entry like codex:*spark
// carves a separately metered model back out into its own provider.
//
// ProviderConfig decodes from either the concise array form:
//
//	[providers]
//	codex = ['g55', 'g54', 'opencode:openai/gpt-5.5']
//
// or the table form, used when a disable switch or exclusions are needed (TOML
// cannot attach extra keys to an array value):
//
//	[providers.codex]
//	models   = ['codex:*']
//	exclude  = ['codex:*spark']
//	disabled = true
type ProviderConfig struct {
	Models   []string
	Exclude  []string
	Disabled bool
}

// parseProviders converts the raw [providers] table (decoded as a generic map so
// the array and table forms can coexist) into typed ProviderConfig values. It
// validates structural shape only; spec resolution happens in resolveProviders
// once harnesses and default models are known.
func parseProviders(raw map[string]interface{}) (map[string]ProviderConfig, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]ProviderConfig, len(raw))
	seen := map[string]string{}
	for name, val := range raw {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return nil, fmt.Errorf("config: [providers] contains an empty provider name")
		}
		lower := strings.ToLower(trimmed)
		if prev, exists := seen[lower]; exists {
			return nil, fmt.Errorf("config: duplicate provider keys %q and %q differ only by case", prev, name)
		}
		seen[lower] = name

		pc, err := parseProviderValue(name, val)
		if err != nil {
			return nil, err
		}
		out[name] = pc
	}
	return out, nil
}

func parseProviderValue(name string, val interface{}) (ProviderConfig, error) {
	switch v := val.(type) {
	case []interface{}:
		models, err := toModelList(name, v)
		if err != nil {
			return ProviderConfig{}, err
		}
		return ProviderConfig{Models: models}, nil
	case map[string]interface{}:
		pc := ProviderConfig{}
		for key := range v {
			switch key {
			case "models", "exclude", "disabled":
			default:
				return ProviderConfig{}, fmt.Errorf("config: provider %q has unknown key %q (expected \"models\", \"exclude\", or \"disabled\")", name, key)
			}
		}
		if rawModels, ok := v["models"]; ok {
			arr, ok := rawModels.([]interface{})
			if !ok {
				return ProviderConfig{}, fmt.Errorf("config: provider %q models must be an array of strings", name)
			}
			models, err := toModelList(name, arr)
			if err != nil {
				return ProviderConfig{}, err
			}
			pc.Models = models
		}
		if rawExclude, ok := v["exclude"]; ok {
			arr, ok := rawExclude.([]interface{})
			if !ok {
				return ProviderConfig{}, fmt.Errorf("config: provider %q exclude must be an array of strings", name)
			}
			exclude, err := toModelList(name, arr)
			if err != nil {
				return ProviderConfig{}, err
			}
			pc.Exclude = exclude
		}
		if rawDisabled, ok := v["disabled"]; ok {
			b, ok := rawDisabled.(bool)
			if !ok {
				return ProviderConfig{}, fmt.Errorf("config: provider %q disabled must be a boolean", name)
			}
			pc.Disabled = b
		}
		return pc, nil
	default:
		return ProviderConfig{}, fmt.Errorf("config: provider %q must be an array of model specs or a table with \"models\"/\"disabled\" keys", name)
	}
}

func toModelList(name string, arr []interface{}) ([]string, error) {
	models := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("config: provider %q model entries must be strings", name)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("config: provider %q contains an empty model spec", name)
		}
		models = append(models, s)
	}
	return models, nil
}

// providersToRaw renders typed providers back to the generic map form used for
// TOML marshaling: the concise array form when a provider has only models and is
// enabled, and the table form when it carries an exclude list or a disabled
// flag (so those round-trip).
func providersToRaw(providers map[string]ProviderConfig) map[string]interface{} {
	if len(providers) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(providers))
	for name, pc := range providers {
		models := toAnySlice(pc.Models)
		if len(pc.Exclude) > 0 || pc.Disabled {
			table := map[string]interface{}{"models": models}
			if len(pc.Exclude) > 0 {
				table["exclude"] = toAnySlice(pc.Exclude)
			}
			if pc.Disabled {
				table["disabled"] = true
			}
			out[name] = table
		} else {
			out[name] = models
		}
	}
	return out
}

func toAnySlice(in []string) []interface{} {
	out := make([]interface{}, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
