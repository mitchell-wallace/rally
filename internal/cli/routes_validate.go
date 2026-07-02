package cli

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/routing"
)

// validateReasoning checks the `[reasoning]` table. A harness-scoped model
// alias (e.g. `cc:opus-high`) names its harness, so a missing alias is almost
// certainly an operator typo and is reported as a hard error. A bare token is
// resolved against the route-selected harness only at runtime — it may be a
// model alias or a passthrough effort value — so it never hard-fails; it only
// warns when it matches neither a configured model alias nor a documented
// reasoning effort.
func validateReasoning(cfg config.V2Config) ([]string, error) {
	if len(cfg.Reasoning) == 0 {
		return nil, nil
	}

	roles := make([]string, 0, len(cfg.Reasoning))
	for role := range cfg.Reasoning {
		roles = append(roles, role)
	}
	sort.Strings(roles)

	var warnings []string
	for _, role := range roles {
		preference := strings.TrimSpace(cfg.Reasoning[role])
		if preference == "" {
			continue
		}

		if scopedHarness, _, scoped := strings.Cut(preference, ":"); scoped {
			if _, _, err := cfg.ResolveRoleReasoning(role, strings.TrimSpace(scopedHarness), preference); err != nil {
				return nil, fmt.Errorf("routes check: %w", err)
			}
			continue
		}

		if reasoningTokenRecognised(cfg, preference) {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"warning: [reasoning].%s value %q is not a known model alias or documented reasoning effort; it will be passed through to the selected harness as-is",
			role, preference))
	}

	return warnings, nil
}

func reasoningTokenRecognised(cfg config.V2Config, token string) bool {
	if harnessapi.IsKnownReasoningEffort(token) {
		return true
	}
	for _, hc := range cfg.Harnesses {
		if hc == nil || hc.Models == nil {
			continue
		}
		if _, ok := hc.Models[token]; ok {
			return true
		}
	}
	return false
}

func validateRouteEntry(cfg config.V2Config, routeName string, entry routing.ParsedEntry) error {
	if _, err := cfg.ResolveAgent(entry.Spec); err != nil {
		if alias, ok := config.RemovedGeminiAlias(err); ok {
			return &removedAliasRouteError{
				msg:     fmt.Sprintf("routes check: route %q entry %q: %s", routeName, entry.Raw, config.RemovedGeminiAliasWarning(routeName, entry.Raw, alias)),
				alias:   strings.ToLower(alias),
				warning: config.RemovedGeminiAliasWarning(routeName, entry.Raw, alias),
			}
		}
		return fmt.Errorf("routes check: route %q entry %q: %s", routeName, entry.Raw, decorateResolveError(cfg, entry.Spec, err))
	}
	return nil
}

func decorateResolveError(cfg config.V2Config, spec string, err error) string {
	errMsg := err.Error()
	if !strings.Contains(errMsg, "unknown agent alias") {
		return errMsg
	}

	alias := spec
	if idx := strings.Index(alias, ":"); idx >= 0 {
		alias = alias[:idx]
	}
	suggestions := topAliasSuggestions(alias, cfg)
	if len(suggestions) == 0 {
		return errMsg
	}
	return fmt.Sprintf("%s; did you mean %s?", errMsg, strings.Join(suggestions, ", "))
}

func topAliasSuggestions(target string, cfg config.V2Config) []string {
	candidates := aliasCandidates(cfg)
	if len(candidates) == 0 {
		return nil
	}

	type scored struct {
		name  string
		score int
	}

	ranked := make([]scored, 0, len(candidates))
	for _, candidate := range candidates {
		ranked = append(ranked, scored{name: candidate, score: levenshtein(target, candidate)})
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].name < ranked[j].name
		}
		return ranked[i].score < ranked[j].score
	})

	if len(ranked) > 3 {
		ranked = ranked[:3]
	}

	suggestions := make([]string, 0, len(ranked))
	for _, item := range ranked {
		suggestions = append(suggestions, item.name)
	}
	return suggestions
}

func aliasCandidates(cfg config.V2Config) []string {
	seen := map[string]bool{}
	candidates := []string{}

	for _, name := range []string{"ag", "agy", "antigravity", "cc", "claude", "cx", "codex", "op", "opencode"} {
		if seen[name] {
			continue
		}
		seen[name] = true
		candidates = append(candidates, name)
	}

	for name := range cfg.Harnesses {
		if seen[name] {
			continue
		}
		seen[name] = true
		candidates = append(candidates, name)
	}

	sort.Strings(candidates)
	return candidates
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
	best := math.MaxInt
	for _, value := range vals {
		if value < best {
			best = value
		}
	}
	return best
}
