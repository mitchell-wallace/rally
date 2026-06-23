package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/routing"
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
func (c V2Config) resolveProviderMembers(providerName string, specs []string, requireConcrete, requireWildcardMatch bool) ([]agent.ResolvedAgent, error) {
	seen := map[providerRunnerKey]bool{}
	out := make([]agent.ResolvedAgent, 0, len(specs))
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
func (c V2Config) resolveProviderSpec(spec string, requireWildcardMatch bool) ([]agent.ResolvedAgent, error) {
	if strings.Contains(spec, "*") {
		return c.resolveProviderWildcardSpec(spec, requireWildcardMatch)
	}
	resolved, err := c.resolveProviderConcreteSpec(spec)
	if err != nil {
		return nil, err
	}
	return []agent.ResolvedAgent{resolved}, nil
}

func (c V2Config) resolveProviderConcreteSpec(spec string) (agent.ResolvedAgent, error) {
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

func (c V2Config) resolveProviderWildcardSpec(spec string, requireMatch bool) ([]agent.ResolvedAgent, error) {
	if strings.Count(spec, "*") != 1 {
		return nil, fmt.Errorf("unsupported wildcard %q; use harness:*, harness:prefix/*, harness:*suffix, prefix/*, or *suffix", spec)
	}

	if strings.HasSuffix(spec, ":*") {
		harnessSpec := strings.TrimSuffix(spec, ":*")
		harness, preferred, err := c.resolveProviderWildcardHarness(spec, harnessSpec)
		if err != nil {
			return nil, err
		}
		return c.expandProviderHarnessModels(spec, harness, preferred, matchAll, requireMatch)
	}

	if scopedHarness, pattern, scoped := strings.Cut(spec, ":"); scoped {
		if prefix, ok := providerPrefixWildcard(pattern); ok {
			harness, preferred, err := c.resolveProviderWildcardHarness(spec, scopedHarness)
			if err != nil {
				return nil, err
			}
			return c.expandProviderHarnessModels(spec, harness, preferred, matchPrefix(prefix), requireMatch)
		}
		if suffix, ok := providerSuffixWildcard(pattern); ok {
			harness, preferred, err := c.resolveProviderWildcardHarness(spec, scopedHarness)
			if err != nil {
				return nil, err
			}
			return c.expandProviderHarnessModels(spec, harness, preferred, matchSuffix(suffix), requireMatch)
		}
		return nil, fmt.Errorf("unsupported wildcard %q; use harness:*, harness:prefix/*, harness:*suffix, prefix/*, or *suffix", spec)
	}

	if prefix, ok := providerPrefixWildcard(spec); ok {
		return c.expandProviderModels(spec, matchPrefix(prefix), requireMatch)
	}
	if suffix, ok := providerSuffixWildcard(spec); ok {
		return c.expandProviderModels(spec, matchSuffix(suffix), requireMatch)
	}
	return nil, fmt.Errorf("unsupported wildcard %q; use harness:*, harness:prefix/*, harness:*suffix, prefix/*, or *suffix", spec)
}

func providerPrefixWildcard(pattern string) (string, bool) {
	if !strings.HasSuffix(pattern, "/*") {
		return "", false
	}
	prefix := strings.TrimSuffix(pattern, "*")
	if strings.TrimSuffix(prefix, "/") == "" {
		return "", false
	}
	return prefix, true
}

// providerSuffixWildcard recognizes a leading-star pattern like "*spark" and
// returns the suffix a model must end with. A bare "*" is intentionally not a
// suffix wildcard; the all-models case is handled by the harness:* / * form.
func providerSuffixWildcard(pattern string) (string, bool) {
	if !strings.HasPrefix(pattern, "*") {
		return "", false
	}
	suffix := strings.TrimPrefix(pattern, "*")
	if suffix == "" {
		return "", false
	}
	return suffix, true
}

// modelFilter decides whether an expanded model string belongs in the result.
type modelFilter func(string) bool

func matchAll(string) bool { return true }

func matchPrefix(prefix string) modelFilter {
	return func(model string) bool { return strings.HasPrefix(model, prefix) }
}

func matchSuffix(suffix string) modelFilter {
	return func(model string) bool { return strings.HasSuffix(model, suffix) }
}

func (c V2Config) resolveProviderWildcardHarness(spec, harnessSpec string) (string, string, error) {
	harnessSpec = strings.TrimSpace(harnessSpec)
	if harnessSpec == "" {
		return "", "", fmt.Errorf("provider wildcard %q has an empty harness", spec)
	}
	if harness, ok := builtInAliases[harnessSpec]; ok {
		return harness, harnessSpec, nil
	}
	if c.Harnesses != nil {
		if _, ok := c.Harnesses[harnessSpec]; ok {
			return canonicalHarnessName(harnessSpec), harnessSpec, nil
		}
	}
	return "", "", fmt.Errorf("provider wildcard %q references unknown harness %q", spec, harnessSpec)
}

func (c V2Config) expandProviderHarnessModels(spec, harness, preferredAlias string, filter modelFilter, requireMatch bool) ([]agent.ResolvedAgent, error) {
	seen := map[providerRunnerKey]bool{}
	var matches []agent.ResolvedAgent
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" || !filter(model) {
			return
		}
		key := providerRunnerKey{Harness: harness, Model: model}
		if seen[key] {
			return
		}
		seen[key] = true
		matches = append(matches, agent.ResolvedAgent{Harness: harness, Model: model})
	}

	add(c.defaultModelForHarness(harness))

	for _, key := range harnessLookupKeys(harness, preferredAlias) {
		hc := c.Harnesses[key]
		if hc == nil || hc.Models == nil {
			continue
		}
		modelAliases := sortedMapKeys(hc.Models)
		for _, alias := range modelAliases {
			add(hc.Models[alias])
		}
	}

	sortResolvedAgents(matches)
	if len(matches) == 0 && requireMatch {
		return nil, fmt.Errorf("provider wildcard %q matched no configured models", spec)
	}
	return matches, nil
}

func (c V2Config) expandProviderModels(spec string, filter modelFilter, requireMatch bool) ([]agent.ResolvedAgent, error) {
	seen := map[providerRunnerKey]bool{}
	var matches []agent.ResolvedAgent
	add := func(harness, model string) {
		model = strings.TrimSpace(model)
		if model == "" || !filter(model) {
			return
		}
		key := providerRunnerKey{Harness: harness, Model: model}
		if seen[key] {
			return
		}
		seen[key] = true
		matches = append(matches, agent.ResolvedAgent{Harness: harness, Model: model})
	}

	for _, harness := range builtInHarnessNames() {
		add(harness, c.defaultModelForHarness(harness))
	}

	harnessKeys := sortedHarnessKeys(c.Harnesses)
	for _, key := range harnessKeys {
		hc := c.Harnesses[key]
		if hc == nil || hc.Models == nil {
			continue
		}
		harness := canonicalHarnessName(key)
		modelAliases := sortedMapKeys(hc.Models)
		for _, alias := range modelAliases {
			add(harness, hc.Models[alias])
		}
	}

	sortResolvedAgents(matches)
	if len(matches) == 0 && requireMatch {
		return nil, fmt.Errorf("provider wildcard %q matched no configured models", spec)
	}
	return matches, nil
}

func sortedHarnessKeys(harnesses map[string]*HarnessConfig) []string {
	keys := make([]string, 0, len(harnesses))
	for key := range harnesses {
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

func sortResolvedAgents(agents []agent.ResolvedAgent) {
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Harness == agents[j].Harness {
			return agents[i].Model < agents[j].Model
		}
		return agents[i].Harness < agents[j].Harness
	})
}

func builtInHarnessNames() []string {
	return []string{"antigravity", "claude", "codex", "gemini", "opencode"}
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

func runnerLabel(a agent.ResolvedAgent) string {
	if a.Model == "" {
		return a.Harness
	}
	return a.Harness + ":" + a.Model
}
