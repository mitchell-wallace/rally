package routing

import (
	"strings"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
)

// RoleReasoningResolver resolves a role-level reasoning preference once the
// selected route entry's harness is known.
type RoleReasoningResolver func(role, selectedHarness, preference string) (model, reasoningEffort string, err error)

// ApplyRoleReasoningFallback applies a role reasoning preference only after
// route selection, and only for route entries that did not explicitly name a
// model. Explicit route models remain the highest-precedence model selection.
func ApplyRoleReasoningFallback(
	picked harnessapi.ResolvedAgent,
	entry ParsedEntry,
	role string,
	preferences map[string]string,
	resolver RoleReasoningResolver,
) (harnessapi.ResolvedAgent, error) {
	if entry.ExplicitModel || picked.Harness == "" || resolver == nil {
		return picked, nil
	}

	preference, ok := lookupRoleReasoning(preferences, role)
	if !ok || strings.TrimSpace(preference) == "" {
		return picked, nil
	}

	model, effort, err := resolver(role, picked.Harness, preference)
	if err != nil {
		return harnessapi.ResolvedAgent{}, err
	}
	if model != "" {
		picked.Model = model
	}
	if effort != "" {
		picked.ReasoningEffort = effort
	}
	return picked, nil
}

func lookupRoleReasoning(preferences map[string]string, role string) (string, bool) {
	if len(preferences) == 0 {
		return "", false
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return "", false
	}
	preference, ok := preferences[role]
	if ok {
		return preference, true
	}
	for key, value := range preferences {
		if strings.EqualFold(strings.TrimSpace(key), role) {
			return value, true
		}
	}
	return "", false
}
