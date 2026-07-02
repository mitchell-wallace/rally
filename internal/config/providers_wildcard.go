package config

import (
	"fmt"
	"strings"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
)

func (c V2Config) resolveProviderWildcardSpec(spec string, requireMatch bool) ([]harnessapi.ResolvedAgent, error) {
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

func (c V2Config) expandProviderHarnessModels(spec, harness, preferredAlias string, filter modelFilter, requireMatch bool) ([]harnessapi.ResolvedAgent, error) {
	seen := map[providerRunnerKey]bool{}
	var matches []harnessapi.ResolvedAgent
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
		matches = append(matches, harnessapi.ResolvedAgent{Harness: harness, Model: model})
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

func (c V2Config) expandProviderModels(spec string, filter modelFilter, requireMatch bool) ([]harnessapi.ResolvedAgent, error) {
	seen := map[providerRunnerKey]bool{}
	var matches []harnessapi.ResolvedAgent
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
		matches = append(matches, harnessapi.ResolvedAgent{Harness: harness, Model: model})
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
