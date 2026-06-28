package routing

import "strings"

var antigravityFamilies = []string{"claude", "flash", "pro"}

// QuotaScope resolves the harness-aware quota bucket a runner draws from, used
// as the bench key when a usage limit is hit so every runner sharing the
// exhausted quota leaves rotation together. Antigravity quotas are per model
// family (matched case-insensitively against free-form display labels),
// opencode quotas are per provider (the segment before '/' in provider/model),
// and direct harnesses (claude, codex) are per harness — the model is
// ignored so a stray '/' cannot mis-split the scope.
func QuotaScope(harness, model string) string {
	switch strings.ToLower(harness) {
	case "antigravity":
		lower := strings.ToLower(model)
		for _, family := range antigravityFamilies {
			if strings.Contains(lower, family) {
				return harness + ":" + family
			}
		}
		return harness + ":" + lower
	case "opencode":
		if idx := strings.Index(model, "/"); idx >= 0 {
			return harness + ":" + model[:idx]
		}
		return harness + ":" + model
	default:
		return harness
	}
}
