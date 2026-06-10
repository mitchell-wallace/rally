package routing

import "strings"

var antigravityFamilies = []string{"claude", "flash", "pro"}

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
