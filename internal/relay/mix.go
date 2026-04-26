package relay

import (
	"fmt"
	"strconv"
	"strings"
)

type AgentMix struct {
	Weights map[string]int
	Order   []string
	Cycle   []string
	Label   string
}

func ParseAgentMix(specs []string) (AgentMix, error) {
	weights := map[string]int{"claude": 0, "codex": 0, "gemini": 0, "opencode": 0}
	order := []string{}
	addWeight := func(agent string, amount int) error {
		if amount < 1 {
			return fmt.Errorf("agent weight must be >= 1")
		}
		if weights[agent] == 0 {
			order = append(order, agent)
		}
		weights[agent] += amount
		return nil
	}

	if len(specs) == 0 {
		_ = addWeight("claude", 1)
		_ = addWeight("codex", 2)
	} else {
		aliases := map[string]string{
			"cc": "claude", "claude": "claude",
			"cx": "codex", "codex": "codex",
			"ge": "gemini", "gemini": "gemini",
			"op": "opencode", "opencode": "opencode",
		}
		for _, spec := range specs {
			parts := strings.SplitN(spec, ":", 2)
			agent, ok := aliases[parts[0]]
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
			if err := addWeight(agent, weight); err != nil {
				return AgentMix{}, err
			}
		}
	}

	cycle := []string{}
	labelParts := []string{}
	for _, agent := range order {
		for i := 0; i < weights[agent]; i++ {
			cycle = append(cycle, agent)
		}
		labelParts = append(labelParts, fmt.Sprintf("%s:%d", agent, weights[agent]))
	}
	return AgentMix{
		Weights: weights,
		Order:   order,
		Cycle:   cycle,
		Label:   strings.Join(labelParts, " "),
	}, nil
}

func AgentForRun(runIndex int, mix AgentMix) string {
	if len(mix.Cycle) == 0 {
		return "claude"
	}
	return mix.Cycle[runIndex%len(mix.Cycle)]
}
