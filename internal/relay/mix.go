package relay

import (
	"fmt"
	"strconv"
	"strings"
)

// ResolvedAgent is a typed (harness, model) record. Harness is always the
// canonical harness name (e.g. "claude", "opencode"). Model is empty for a bare
// alias (the harness uses its default model) or a non-empty model string when
// resolved via a named-model entry or raw harness:model-string form.
type ResolvedAgent struct {
	Harness string
	Model   string
}

// Resolver converts a mix token (e.g. "cc", "op:z", "opencode:provider/model")
// into a ResolvedAgent. The config layer provides the concrete implementation.
type Resolver func(spec string) (ResolvedAgent, error)

// Weights and Order are keyed by harness alias (not by (harness, model) tuple).
// Weighting is per-harness: if claude is weighted 2, all models under claude
// share that weight. This matches v0.2.x semantics and simplifies the
// resilience layer, where freezing applies per-harness, not per-model.
type AgentMix struct {
	Weights map[string]int
	Order   []string
	Cycle   []ResolvedAgent
	Label   string
}

func ParseAgentMix(specs []string, resolver Resolver) (AgentMix, error) {
	weights := map[string]int{}
	order := []string{}
	seen := map[string]bool{}

	addWeight := func(harness string, amount int) error {
		if amount < 1 {
			return fmt.Errorf("agent weight must be >= 1")
		}
		if !seen[harness] {
			order = append(order, harness)
			seen[harness] = true
		}
		weights[harness] += amount
		return nil
	}

	// When no specs provided, use default mix: 1 claude, 2 codex.
	if len(specs) == 0 {
		_ = addWeight("claude", 1)
		_ = addWeight("codex", 2)
	} else {
		for _, spec := range specs {
			// Reject third colon segment (reserved for v0.6.0 weight-on-named-model).
			if strings.Count(spec, ":") > 1 {
				return AgentMix{}, fmt.Errorf("invalid agent spec %q: weight-on-named-model (e.g. cc:opus:2) is not supported", spec)
			}

			// If a resolver is provided, delegate resolution to it.
			if resolver != nil {
				ra, err := resolver(spec)
				if err != nil {
					return AgentMix{}, err
				}
				if !seen[ra.Harness] {
					order = append(order, ra.Harness)
					seen[ra.Harness] = true
				}
				// Weighted entries: detect digits-after-colon (e.g. "cc:2").
				// The resolver returns Model="" for weight-only entries.
				if ra.Model == "" {
					parts := strings.SplitN(spec, ":", 2)
					if len(parts) == 2 {
						if n, err := strconv.Atoi(parts[1]); err == nil && n >= 1 {
							weights[ra.Harness] += n
							continue
						}
					}
				}
				weights[ra.Harness]++
				continue
			}

			// Fallback path: no resolver provided (legacy/test path).
			aliases := map[string]string{
				"cc": "claude", "claude": "claude",
				"cx": "codex", "codex": "codex",
				"ge": "gemini", "gemini": "gemini",
				"op": "opencode", "opencode": "opencode",
			}
			parts := strings.SplitN(spec, ":", 2)
			harness, ok := aliases[parts[0]]
			if !ok {
				return AgentMix{}, fmt.Errorf("unknown agent alias %q", parts[0])
			}
			weight := 1
			if len(parts) == 2 {
				n, err := strconv.Atoi(parts[1])
				if err != nil || n < 1 {
					return AgentMix{}, fmt.Errorf("invalid agent weight %q", spec)
				}
				weight = n
			}
			if err := addWeight(harness, weight); err != nil {
				return AgentMix{}, err
			}
		}
	}

	// Build the typed cycle and label.
	// Label format: one space-separated token per cycle entry.
	//   Bare alias:   "claude"
	//   Named model:  "opencode:z"
	//   Raw model:    "opencode:provider/model"
	// This expanded form round-trips through ParseAgentMix with the same resolver.
	// When a harness has weight > 1 and no model, the alias is repeated.
	cycle := []ResolvedAgent{}
	labelParts := []string{}
	for _, harness := range order {
		// Collect all named-model entries for this harness from the specs,
		// but only when resolver was provided. Otherwise use bare entries.
		namedEntries := []ResolvedAgent{}
		if resolver != nil {
			for _, spec := range specs {
				ra, _ := resolver(spec)
				if ra.Harness == harness && ra.Model != "" {
					namedEntries = append(namedEntries, ra)
				}
			}
		}

		if len(namedEntries) > 0 {
			for _, entry := range namedEntries {
				cycle = append(cycle, entry)
				labelParts = append(labelParts, harness+":"+entry.Model)
			}
		} else {
			for i := 0; i < weights[harness]; i++ {
				cycle = append(cycle, ResolvedAgent{Harness: harness})
				labelParts = append(labelParts, harness)
			}
		}
	}

	return AgentMix{
		Weights: weights,
		Order:   order,
		Cycle:   cycle,
		Label:   strings.Join(labelParts, " "),
	}, nil
}
