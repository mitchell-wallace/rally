package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/routing"
)

// ProviderConfig is a user-defined quota group: a set of runners (named model
// shortcuts or harness:model specs) that share a single usage-limit budget, plus
// an operator switch to disable them all at once. When any member of a provider
// hits a usage limit, every sibling is benched until the reset so Rally does not
// burn retries on models that draw from the same exhausted account.
//
// It decodes from either the concise array form:
//
//	[providers]
//	codex = ['g55', 'g54', 'opencode:openai/gpt-5.5']
//
// or the table form, used when a disable switch is needed (TOML cannot attach a
// `disabled` key to an array value):
//
//	[providers.codex]
//	models   = ['g55', 'g54']
//	disabled = true
type ProviderConfig struct {
	Models   []string
	Disabled bool
}

// providerRunnerKey identifies a resolved runner for conflict detection.
type providerRunnerKey struct {
	Harness string
	Model   string
}

// resolvedProvider is a provider with its model specs resolved to concrete
// runners, deduplicated within the provider.
type resolvedProvider struct {
	Name     string
	Disabled bool
	Runners  []agent.ResolvedAgent
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
			case "models", "disabled":
			default:
				return ProviderConfig{}, fmt.Errorf("config: provider %q has unknown key %q (expected \"models\" or \"disabled\")", name, key)
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

// resolveProviders resolves every provider's model specs to concrete runners,
// validating that each spec resolves, that each provider has at least one model,
// and that no runner belongs to two providers. Results are sorted by provider
// name for deterministic ordering and error messages.
func (c V2Config) resolveProviders() ([]resolvedProvider, error) {
	if len(c.Providers) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(c.Providers))
	for name := range c.Providers {
		names = append(names, name)
	}
	sort.Strings(names)

	owner := map[providerRunnerKey]string{}
	out := make([]resolvedProvider, 0, len(names))
	for _, name := range names {
		pc := c.Providers[name]
		if len(pc.Models) == 0 {
			return nil, fmt.Errorf("config: provider %q has no models", name)
		}
		rp := resolvedProvider{Name: name, Disabled: pc.Disabled}
		localSeen := map[providerRunnerKey]bool{}
		for _, spec := range pc.Models {
			resolved, err := c.resolveProviderSpec(spec)
			if err != nil {
				return nil, fmt.Errorf("config: provider %q: %w", name, err)
			}
			key := providerRunnerKey{Harness: resolved.Harness, Model: resolved.Model}
			if existing, ok := owner[key]; ok && existing != name {
				return nil, fmt.Errorf("config: runner %s is claimed by providers %q and %q; a runner may belong to only one provider", runnerLabel(resolved), existing, name)
			}
			owner[key] = name
			if localSeen[key] {
				continue
			}
			localSeen[key] = true
			rp.Runners = append(rp.Runners, resolved)
		}
		out = append(out, rp)
	}
	return out, nil
}

// resolveProviderSpec resolves one provider model entry to a concrete runner. It
// accepts the same forms as a route entry — a harness alias (resolves to that
// harness's default model), alias:model, or harness:model — and additionally a
// bare model alias (e.g. "g55") when that alias is defined under exactly one
// harness's [harness.<h>.models] table. A bare alias that is ambiguous or
// undefined is a hard error so quota groups never silently mis-bucket a runner.
func (c V2Config) resolveProviderSpec(spec string) (agent.ResolvedAgent, error) {
	if strings.Contains(spec, ":") {
		return c.ResolveAgent(spec)
	}
	// A bare token may name a harness (or alias) whose default model we want, or
	// a model alias to be searched across harness tables. Prefer the harness
	// reading when the token is a known harness so `cc` keeps meaning "claude
	// default model" rather than scanning model tables for an alias named "cc".
	if _, ok := builtInAliases[spec]; ok {
		return c.ResolveAgent(spec)
	}
	if _, ok := c.Harnesses[spec]; ok {
		return c.ResolveAgent(spec)
	}
	matches := c.lookupBareModelAlias(spec)
	switch len(matches) {
	case 0:
		return agent.ResolvedAgent{}, fmt.Errorf("unknown model alias %q; qualify it as harness:model (e.g. cx:%s)", spec, spec)
	case 1:
		return matches[0], nil
	default:
		labels := make([]string, len(matches))
		for i, m := range matches {
			labels[i] = runnerLabel(m)
		}
		sort.Strings(labels)
		return agent.ResolvedAgent{}, fmt.Errorf("ambiguous model alias %q matches %s; qualify it as harness:alias", spec, strings.Join(labels, ", "))
	}
}

// lookupBareModelAlias returns every distinct runner whose harness defines a
// model alias named alias under [harness.<h>.models]. Distinctness is by the
// canonical harness + model string, so `cx`/`codex` aliases pointing at the same
// model collapse to one match.
func (c V2Config) lookupBareModelAlias(alias string) []agent.ResolvedAgent {
	seen := map[providerRunnerKey]bool{}
	var matches []agent.ResolvedAgent
	for hkey, hc := range c.Harnesses {
		if hc == nil || hc.Models == nil {
			continue
		}
		modelStr, ok := hc.Models[alias]
		if !ok {
			continue
		}
		harness := canonicalHarnessName(hkey)
		key := providerRunnerKey{Harness: harness, Model: modelStr}
		if seen[key] {
			continue
		}
		seen[key] = true
		matches = append(matches, agent.ResolvedAgent{Harness: harness, Model: modelStr})
	}
	return matches
}

// BuildProviderIndex resolves the [providers] config into a routing.ProviderIndex
// used at relay runtime for quota-scope grouping and operator disable switches. A
// config with no providers yields a nil index, which the index treats as "no
// providers configured".
func (c V2Config) BuildProviderIndex() (*routing.ProviderIndex, error) {
	resolved, err := c.resolveProviders()
	if err != nil {
		return nil, err
	}
	if len(resolved) == 0 {
		return nil, nil
	}
	idx := routing.NewProviderIndex()
	for _, rp := range resolved {
		for _, r := range rp.Runners {
			idx.Add(rp.Name, r.Harness, r.Model, rp.Disabled)
		}
	}
	return idx, nil
}

// providersToRaw renders typed providers back to the generic map form used for
// TOML marshaling: the concise array form when a provider is enabled, and the
// table form when it is disabled (so the disabled flag round-trips).
func providersToRaw(providers map[string]ProviderConfig) map[string]interface{} {
	if len(providers) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(providers))
	for name, pc := range providers {
		models := toAnySlice(pc.Models)
		if pc.Disabled {
			out[name] = map[string]interface{}{
				"models":   models,
				"disabled": true,
			}
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

func runnerLabel(a agent.ResolvedAgent) string {
	if a.Model == "" {
		return a.Harness
	}
	return a.Harness + ":" + a.Model
}
