package relay

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mitchell-wallace/rally/internal/agent"
)

// Resolver converts a mix token (e.g. "cc", "op:z", "opencode:provider/model")
// into an agent.ResolvedAgent. The config layer provides the concrete implementation.
type Resolver func(spec string) (agent.ResolvedAgent, error)

// Weights and Order are keyed by harness alias (not by (harness, model) tuple).
// Weighting is per-harness: if claude is weighted 2, all models under claude
// share that weight. This matches v0.2.x semantics and simplifies the
// resilience layer, where freezing applies per-harness, not per-model.
type AgentMix struct {
	Weights map[string]int
	Order   []string
	Cycle   []agent.ResolvedAgent
	Label   string
}

func ParseAgentMix(specs []string, resolver Resolver) (AgentMix, error) {
	weights := map[string]int{}
	order := []string{}
	seen := map[string]bool{}
	// harnessDefaultModel tracks the resolver's model for bare/weight slots so
	// that opts.Model is populated even when the user doesn't specify a named model.
	harnessDefaultModel := map[string]string{}

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
				// Detect weight entries and bare aliases by spec pattern, not by
				// resolver output. The resolver may return a default model for cc:2
				// which would otherwise suppress weight accounting.
				parts := strings.SplitN(spec, ":", 2)
				isBareOrWeight := len(parts) < 2
				specWeight := 1
				if len(parts) == 2 {
					if n, err := strconv.Atoi(parts[1]); err == nil && n >= 1 {
						isBareOrWeight = true
						specWeight = n
					}
				}

				ra, err := resolver(spec)
				if err != nil {
					return AgentMix{}, err
				}
				if !seen[ra.Harness] {
					order = append(order, ra.Harness)
					seen[ra.Harness] = true
				}
				if isBareOrWeight {
					weights[ra.Harness] += specWeight
					// Capture the default model from the resolver for bare/weight slots.
					if _, ok := harnessDefaultModel[ra.Harness]; !ok {
						harnessDefaultModel[ra.Harness] = ra.Model
					}
				} else {
					weights[ra.Harness]++
				}
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
	//   Bare/weight alias: "claude" (canonical harness name, repeated for weight > 1)
	//   Named model:       "cc:opus" (original spec — NOT the resolved model string)
	//   Raw model:         "opencode:provider/model"
	// Using the original spec for named-model entries is critical for round-trip
	// correctness: a resolved string like "claude-opus-4-7" looks like a named-model
	// key to the resolver, so the label must carry the short alias ("cc:opus").
	cycle := []agent.ResolvedAgent{}
	labelParts := []string{}
	for _, harness := range order {
		// Collect named-model entries for this harness: specs with a non-numeric
		// right side (not a bare alias or weight entry).
		namedSpecs := []string{}
		namedResolved := []agent.ResolvedAgent{}
		if resolver != nil {
			for _, spec := range specs {
				parts := strings.SplitN(spec, ":", 2)
				if len(parts) < 2 {
					continue // bare alias — not a named model
				}
				if _, err := strconv.Atoi(parts[1]); err == nil {
					continue // weight entry — not a named model
				}
				ra, _ := resolver(spec)
				if ra.Harness == harness && ra.Model != "" {
					namedSpecs = append(namedSpecs, spec)
					namedResolved = append(namedResolved, ra)
				}
			}
		}

		if len(namedResolved) > 0 {
			for i, entry := range namedResolved {
				cycle = append(cycle, entry)
				labelParts = append(labelParts, namedSpecs[i])
			}
		} else {
			defaultModel := harnessDefaultModel[harness]
			for i := 0; i < weights[harness]; i++ {
				cycle = append(cycle, agent.ResolvedAgent{Harness: harness, Model: defaultModel})
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
