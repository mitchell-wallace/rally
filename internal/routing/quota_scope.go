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
// AuthScope resolves the account bucket an auth failure poisons. It matches
// QuotaScope except for antigravity: quota is per model family there, but the
// CLI login is one account shared by every family, so an auth failure
// sidelines the whole harness. Opencode credentials are per provider and
// direct-harness logins are per harness, which QuotaScope already captures.
func AuthScope(harness, model string) string {
	if strings.ToLower(harness) == "antigravity" {
		return harness
	}
	return QuotaScope(harness, model)
}

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
