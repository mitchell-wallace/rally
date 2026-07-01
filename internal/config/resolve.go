package config

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
)

func (c V2Config) defaultModelForHarness(harness string) string {
	switch harness {
	case "claude":
		return c.ClaudeModel
	case "codex":
		return c.CodexModel
	case "opencode":
		return c.OpenCodeModel
	case "antigravity":
		return c.AntigravityModel
	default:
		return ""
	}
}

func (c V2Config) ResolveAgent(spec string) (harnessapi.ResolvedAgent, error) {
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) == 3 {
		return harnessapi.ResolvedAgent{}, fmt.Errorf("invalid agent spec %q: weight-on-named-model (e.g. cc:opus:2) is not supported", spec)
	}

	alias := parts[0]
	if isRemovedGeminiAlias(alias) {
		return harnessapi.ResolvedAgent{}, RemovedGeminiAliasError{Alias: alias}
	}
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
		return harnessapi.ResolvedAgent{}, fmt.Errorf("unknown agent alias %q", alias)
	}

	if len(parts) == 1 {
		return harnessapi.ResolvedAgent{Harness: harness, Model: c.defaultModelForHarness(harness)}, nil
	}

	right := parts[1]
	if numericOnlyPattern.MatchString(right) {
		return harnessapi.ResolvedAgent{Harness: harness, Model: c.defaultModelForHarness(harness)}, nil
	}

	if modelNamePattern.MatchString(right) {
		hc, found := c.Harnesses[harness]
		if !found {
			hc, found = c.Harnesses[alias]
		}
		if found && hc.Models != nil {
			if modelStr, modelOk := hc.Models[right]; modelOk {
				return harnessapi.ResolvedAgent{Harness: harness, Model: modelStr}, nil
			}
		}
		suggestions := didYouMean(right, modelNamesForHarness(c.Harnesses, harness, alias))
		if suggestions != "" {
			return harnessapi.ResolvedAgent{}, fmt.Errorf("unknown model %q for harness %q; did you mean %s?", right, harness, suggestions)
		}
		return harnessapi.ResolvedAgent{}, fmt.Errorf("unknown model %q for harness %q (no models defined for this harness)", right, harness)
	}

	return harnessapi.ResolvedAgent{Harness: harness, Model: right}, nil
}

func (c V2Config) ResolveRoleReasoning(role, selectedHarness, preference string) (string, string, error) {
	roleKey := strings.ToLower(strings.TrimSpace(role))
	selected := canonicalHarnessName(selectedHarness)
	preference = strings.TrimSpace(preference)
	if selected == "" || preference == "" {
		return "", "", nil
	}

	if scopedHarness, alias, scoped := strings.Cut(preference, ":"); scoped {
		scopedHarness = strings.TrimSpace(scopedHarness)
		alias = strings.TrimSpace(alias)
		if alias == "" {
			return "", "", fmt.Errorf("config: [reasoning].%s references an empty model alias for harness %q", roleKey, selected)
		}
		canonicalScoped := canonicalHarnessName(scopedHarness)
		if canonicalScoped != selected {
			return "", "", nil
		}
		if model, ok := c.lookupModelAlias(canonicalScoped, scopedHarness, alias); ok {
			return model, "", nil
		}
		if suggestions := didYouMean(alias, c.modelAliasNamesForHarness(canonicalScoped, scopedHarness)); suggestions != "" {
			return "", "", fmt.Errorf("config: [reasoning].%s references unknown model alias %q for harness %q; did you mean %s?", roleKey, alias, selected, suggestions)
		}
		return "", "", fmt.Errorf("config: [reasoning].%s references unknown model alias %q for harness %q", roleKey, alias, selected)
	}

	if model, ok := c.lookupModelAlias(selected, "", preference); ok {
		return model, "", nil
	}
	return "", preference, nil
}

func canonicalHarnessName(name string) string {
	name = strings.TrimSpace(name)
	if isRemovedGeminiAlias(name) {
		return ""
	}
	if mapped, ok := builtInAliases[strings.ToLower(name)]; ok {
		return mapped
	}
	return name
}

func (c V2Config) lookupModelAlias(harness, preferredAlias, modelName string) (string, bool) {
	if c.Harnesses == nil {
		return "", false
	}
	for _, key := range harnessLookupKeys(harness, preferredAlias) {
		if isRemovedGeminiAlias(key) {
			continue
		}
		h := c.Harnesses[key]
		if h == nil || h.Models == nil {
			continue
		}
		model, ok := h.Models[modelName]
		if ok {
			return model, true
		}
	}
	return "", false
}

func (c V2Config) modelAliasNamesForHarness(harness, preferredAlias string) []string {
	names := []string{}
	seen := map[string]bool{}
	for _, key := range harnessLookupKeys(harness, preferredAlias) {
		if isRemovedGeminiAlias(key) {
			continue
		}
		h := c.Harnesses[key]
		if h == nil || h.Models == nil {
			continue
		}
		for name := range h.Models {
			if !seen[name] {
				names = append(names, name)
				seen[name] = true
			}
		}
	}
	return names
}

func harnessLookupKeys(harness, preferredAlias string) []string {
	seen := map[string]bool{}
	keys := []string{}
	add := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			return
		}
		keys = append(keys, key)
		seen[key] = true
	}

	add(preferredAlias)
	canonical := canonicalHarnessName(harness)
	if order, ok := builtInHarnessLookupOrder[canonical]; ok {
		for _, key := range order {
			add(key)
		}
	} else {
		add(canonical)
	}
	return keys
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

func effectiveModel(topLevel, defaults string) string {
	if defaults != "" {
		return defaults
	}
	return topLevel
}

func isRemovedGeminiAlias(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "ge", "gemini":
		return true
	default:
		return false
	}
}

func RemovedGeminiAliasWarning(role, routeEntry, alias string) string {
	role = strings.TrimSpace(role)
	routeEntry = strings.TrimSpace(routeEntry)
	alias = strings.TrimSpace(alias)
	if role == "" {
		role = "(unknown role)"
	}
	if routeEntry == "" {
		routeEntry = alias
	}
	return fmt.Sprintf("route %q entry %q uses removed gemini alias %q; replace it with antigravity (ag)", role, routeEntry, alias)
}
