package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/routing"
)

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
	Runners  []harnessapi.ResolvedAgent
}

// resolveProviders resolves every provider's model specs to concrete runners,
// validates that each spec resolves, that each provider has at least one model,
// and that no runner belongs to two providers. Excludes are applied to a
// provider's expanded members before the cross-provider ownership check, so an
// excluded runner never triggers a conflict with the provider that legitimately
// owns it. Results are sorted by provider name for deterministic ordering and
// error messages.
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

		// Expand membership specs first. Models require a concrete model and a
		// non-empty wildcard match so a typo can never silently empty a group.
		members, err := c.resolveProviderMembers(name, pc.Models, true, true)
		if err != nil {
			return nil, err
		}

		// Expand exclusions leniently: a filter that matches nothing (e.g. a
		// suffix wildcard for a model not yet configured) is a no-op, not an
		// error. Structural errors — an unknown harness, an unsupported
		// wildcard form — still surface so typos are caught.
		excluded := map[providerRunnerKey]bool{}
		if len(pc.Exclude) > 0 {
			exAgents, err := c.resolveProviderMembers(name, pc.Exclude, false, false)
			if err != nil {
				return nil, err
			}
			for _, r := range exAgents {
				excluded[providerRunnerKey{Harness: r.Harness, Model: r.Model}] = true
			}
		}

		localSeen := map[providerRunnerKey]bool{}
		for _, resolved := range members {
			key := providerRunnerKey{Harness: resolved.Harness, Model: resolved.Model}
			if excluded[key] {
				continue
			}
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
		if len(rp.Runners) == 0 {
			return nil, fmt.Errorf("config: provider %q has no models remaining after exclusions", name)
		}
		out = append(out, rp)
	}
	return out, nil
}

// resolveProviderMembers expands a list of specs (models or excludes) into a
// deduplicated set of concrete runners. requireConcrete rejects specs that
// resolve to a bare harness with no model — wanted for Models so the group keys
// against a real runner, skipped for Excludes where such a spec simply matches
// nothing. requireWildcardMatch controls whether a wildcard that expands to zero
// models is an error (Models) or a harmless no-op (Excludes).
func (c V2Config) resolveProviderMembers(providerName string, specs []string, requireConcrete, requireWildcardMatch bool) ([]harnessapi.ResolvedAgent, error) {
	seen := map[providerRunnerKey]bool{}
	out := make([]harnessapi.ResolvedAgent, 0, len(specs))
	for _, spec := range specs {
		resolvedList, err := c.resolveProviderSpec(spec, requireWildcardMatch)
		if err != nil {
			return nil, fmt.Errorf("config: provider %q: %w", providerName, err)
		}
		for _, resolved := range resolvedList {
			// A member must name a concrete model. A bare harness alias with no
			// default model resolves to an empty model and would key the provider
			// as {harness, ""} — a key no model-specific route runner (e.g.
			// cc:opus) ever matches, silently splitting the group. Reject it so
			// the misconfiguration surfaces instead of mis-bucketing at runtime.
			if requireConcrete && resolved.Model == "" {
				return nil, fmt.Errorf("config: provider %q member %q resolves to no concrete model; name a specific model (e.g. cx:g55) rather than a bare harness alias", providerName, spec)
			}
			key := providerRunnerKey{Harness: resolved.Harness, Model: resolved.Model}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, resolved)
		}
	}
	return out, nil
}

// resolveProviderSpec resolves one provider model entry to concrete runners. It
// accepts the same forms as a route entry — a harness alias (resolves to that
// harness's default model), alias:model, or harness:model — and additionally a
// bare model alias (e.g. "g55") when that alias is defined under exactly one
// harness's [harness.<h>.models] table. Wildcards expand over configured
// concrete models: harness:* matches a harness's configured/default models,
// prefix/* matches configured model strings with that prefix, and *suffix
// matches configured model strings with that suffix. Ambiguous, undefined, or
// (for Models) empty matches are hard errors so quota groups never silently
// mis-bucket a runner.
func (c V2Config) resolveProviderSpec(spec string, requireWildcardMatch bool) ([]harnessapi.ResolvedAgent, error) {
	if strings.Contains(spec, "*") {
		return c.resolveProviderWildcardSpec(spec, requireWildcardMatch)
	}
	resolved, err := c.resolveProviderConcreteSpec(spec)
	if err != nil {
		return nil, err
	}
	return []harnessapi.ResolvedAgent{resolved}, nil
}

func (c V2Config) resolveProviderConcreteSpec(spec string) (harnessapi.ResolvedAgent, error) {
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
		return harnessapi.ResolvedAgent{}, fmt.Errorf("unknown model alias %q; qualify it as harness:model (e.g. cx:%s)", spec, spec)
	case 1:
		return matches[0], nil
	default:
		labels := make([]string, len(matches))
		for i, m := range matches {
			labels[i] = runnerLabel(m)
		}
		sort.Strings(labels)
		return harnessapi.ResolvedAgent{}, fmt.Errorf("ambiguous model alias %q matches %s; qualify it as harness:alias", spec, strings.Join(labels, ", "))
	}
}
func sortedHarnessKeys(harnesses map[string]*HarnessConfig) []string {
	keys := make([]string, 0, len(harnesses))
	for key := range harnesses {
		if isRemovedGeminiAlias(key) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortResolvedAgents(agents []harnessapi.ResolvedAgent) {
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Harness == agents[j].Harness {
			return agents[i].Model < agents[j].Model
		}
		return agents[i].Harness < agents[j].Harness
	})
}

func builtInHarnessNames() []string {
	return []string{"antigravity", "claude", "codex", "opencode"}
}

// lookupBareModelAlias returns every distinct runner whose harness defines a
// model alias named alias under [harness.<h>.models]. Distinctness is by the
// canonical harness + model string, so `cx`/`codex` aliases pointing at the same
// model collapse to one match.
func (c V2Config) lookupBareModelAlias(alias string) []harnessapi.ResolvedAgent {
	seen := map[providerRunnerKey]bool{}
	var matches []harnessapi.ResolvedAgent
	for hkey, hc := range c.Harnesses {
		if isRemovedGeminiAlias(hkey) {
			continue
		}
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
		matches = append(matches, harnessapi.ResolvedAgent{Harness: harness, Model: modelStr})
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

// ProviderMemberCounts returns each provider's concrete runner count after
// wildcard expansion and de-duplication.
func (c V2Config) ProviderMemberCounts() (map[string]int, error) {
	resolved, err := c.resolveProviders()
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(resolved))
	for _, rp := range resolved {
		counts[rp.Name] = len(rp.Runners)
	}
	return counts, nil
}
func runnerLabel(a harnessapi.ResolvedAgent) string {
	if a.Model == "" {
		return a.Harness
	}
	return a.Harness + ":" + a.Model
}
