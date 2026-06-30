package config

import (
	"fmt"
	"strings"
)

func validateHarnesses(harnesses map[string]*HarnessConfig) error {
	for name, h := range harnesses {
		if isRemovedGeminiAlias(name) {
			continue
		}
		if !harnessNamePattern.MatchString(name) {
			return fmt.Errorf("config: invalid harness name %q: must match ^[A-Za-z][A-Za-z0-9_-]*$", name)
		}
		if builtInCanonical[name] {
			if err := validateBuiltInHarness(name, h); err != nil {
				return err
			}
		}
		if len(h.Command) > 0 {
			for _, elem := range h.Command {
				if strings.Contains(elem, "$MODEL") {
					return fmt.Errorf("config: harness %q command contains $MODEL; use model_flag instead of $MODEL placeholder", name)
				}
			}
		}
		if h.OutputStrategy != "" && h.OutputStrategy != "tail" {
			return fmt.Errorf("config: harness %q output_strategy %q is not supported; only \"tail\" is accepted in this version", name, h.OutputStrategy)
		}
		if h.TailStream != "" {
			switch h.TailStream {
			case "stdout", "stderr", "combined":
			default:
				return fmt.Errorf("config: harness %q tail_stream %q is invalid; must be one of stdout, stderr, combined", name, h.TailStream)
			}
		}
		for modelName, modelString := range h.Models {
			if !modelNamePattern.MatchString(modelName) || numericOnlyPattern.MatchString(modelName) {
				return fmt.Errorf("config: harness %q model name %q is invalid: must be a non-numeric identifier matching ^[A-Za-z][A-Za-z0-9_-]*$", name, modelName)
			}
			if modelString == "" {
				return fmt.Errorf("config: harness %q model name %q has an empty model string", name, modelName)
			}
		}
	}
	return nil
}

func validateBuiltInHarness(name string, h *HarnessConfig) error {
	if len(h.Command) > 0 {
		return fmt.Errorf("config: built-in harness %q cannot declare command", name)
	}
	if h.ModelFlag != nil {
		return fmt.Errorf("config: built-in harness %q cannot declare model_flag", name)
	}
	if h.OutputStrategy != "" {
		return fmt.Errorf("config: built-in harness %q cannot declare output_strategy", name)
	}
	if h.TailStream != "" {
		return fmt.Errorf("config: built-in harness %q cannot declare tail_stream", name)
	}
	return nil
}

func validateRoutes(routes map[string][]string) error {
	lowerSeen := map[string]string{}
	for key := range routes {
		lower := strings.ToLower(key)
		if prev, exists := lowerSeen[lower]; exists {
			return fmt.Errorf("config: duplicate route keys %q and %q differ only by case", prev, key)
		}
		lowerSeen[lower] = key
	}

	lowerRouteKeys := map[string]bool{}
	for key := range routes {
		lowerRouteKeys[strings.ToLower(key)] = true
	}

	for key, entries := range routes {
		for _, entry := range entries {
			parts := strings.Split(entry, ":")
			idPart := parts[0]
			if lowerRouteKeys[strings.ToLower(idPart)] {
				return fmt.Errorf("config: route %q references role name %q as an entry; role names are only valid in --agent, not in [routes]", key, idPart)
			}
		}
	}

	return nil
}

func normalizeReasoning(reasoning map[string]string) (map[string]string, error) {
	normalized := make(map[string]string, len(reasoning))
	seen := map[string]string{}
	for key, value := range reasoning {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return nil, fmt.Errorf("config: [reasoning] contains an empty role key")
		}
		lower := strings.ToLower(trimmedKey)
		if prev, exists := seen[lower]; exists {
			return nil, fmt.Errorf("config: duplicate reasoning keys %q and %q differ only by case", prev, key)
		}
		seen[lower] = key
		normalized[lower] = strings.TrimSpace(value)
	}
	return normalized, nil
}

func ValidateRoutesTable(routes map[string][]string) error {
	return validateRoutes(routes)
}
