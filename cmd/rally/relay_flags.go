package main

import (
	"fmt"
	"strings"
)

func expandRelayFlag(values []string, flagName string) ([]string, error) {
	var expanded []string
	for _, spec := range values {
		for _, commaPart := range strings.Split(spec, ",") {
			fields := strings.Fields(strings.TrimSpace(commaPart))
			if len(fields) == 0 {
				return nil, fmt.Errorf("empty value for %s", flagName)
			}
			expanded = append(expanded, fields...)
		}
	}
	return expanded, nil
}

func chooseRelayAgentSpecs(agentValues, mixValues []string, defaultMix string) (specs []string, usedOverride bool, warning string, err error) {
	expandedAgents, err := expandRelayFlag(agentValues, "--agent")
	if err != nil {
		return nil, false, "", err
	}

	expandedMix, err := expandRelayFlag(mixValues, "--mix")
	if err != nil {
		return nil, false, "", err
	}

	switch {
	case len(expandedAgents) > 0 && len(expandedMix) > 0:
		return expandedAgents, true, "warning: both --agent and --mix were supplied; ignoring --mix", nil
	case len(expandedAgents) > 0:
		return expandedAgents, true, "", nil
	case len(expandedMix) > 0:
		return expandedMix, true, "", nil
	}

	if strings.TrimSpace(defaultMix) == "" {
		return nil, false, "", nil
	}

	specs, err = expandRelayFlag([]string{defaultMix}, "[defaults].mix")
	if err != nil {
		return nil, false, "", err
	}
	return specs, false, "", nil
}
