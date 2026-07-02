package runner

import (
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/routing"
)

// benchQuotaScope sidelines every runner across the runtime's schedulers whose
// QuotaScope matches scope, writing a benched event (via BenchAgent) for each
// distinct {Harness,Model} key until resetAt. Called on a usage_limit so the
// whole exhausted quota bucket — not just the failing entry — leaves rotation.
// All quota-scope fan-out is contained here; GetState, syncRecoverySignals, and
// selectionWaitError stay per-key. Mirrors the iterate-all-schedulers pattern in
// forceUnpauseAll. Returns the number of distinct keys benched.
func (r *routeRuntime) benchQuotaScope(resilience *relaycore.Resilience, scope string, resetAt time.Time, relayID int, routeName string, effectiveAssignee string) (int, error) {
	seen := map[relaycore.ResilienceKey]struct{}{}
	benched := 0
	for schedulerName, scheduler := range r.schedulers {
		role := r.roleForScheduler(schedulerName, routeName, effectiveAssignee)
		for _, state := range scheduler.EntryStates() {
			key, err := r.resilienceKeyForEntry(state.Entry, role)
			if err != nil {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			if r.quotaScope(key.Harness, key.Model) != scope {
				continue
			}
			if err := resilience.BenchAgent(key, resetAt, scope, relayID); err != nil {
				return benched, err
			}
			benched++
		}
	}
	return benched, nil
}

func (r *routeRuntime) resilienceKeyForEntry(entry routing.ParsedEntry, role string) (relaycore.ResilienceKey, error) {
	resolved, err := r.resolvedEntryAgent(entry, role)
	if err != nil {
		return relaycore.ResilienceKey{}, err
	}
	return relaycore.KeyFromAgent(resolved), nil
}

func (r *routeRuntime) resolvedEntryAgent(entry routing.ParsedEntry, role string) (harnessapi.ResolvedAgent, error) {
	picked, err := resolveAgentSpec(entry.Spec, nil)
	if err != nil {
		return harnessapi.ResolvedAgent{}, err
	}
	return routing.ApplyRoleReasoningFallback(picked, entry, role, r.reasoning, r.reasoningResolver)
}

func (r *routeRuntime) roleForScheduler(schedulerName string, selectedRouteName string, effectiveAssignee string) string {
	if selectedRouteName != "" && strings.EqualFold(schedulerName, selectedRouteName) {
		if role := strings.TrimSpace(effectiveAssignee); role != "" {
			return role
		}
	}
	return schedulerName
}

// benchResetAt returns the reset deadline of the key's in-force bench. It is
// only meaningful when GetState reports StateBenched: it reads back to the
// latest benched event, returning false if a later recovery/failure event has
// since superseded it.
func (r *routeRuntime) benchResetAt(resilience *relaycore.Resilience, key relaycore.ResilienceKey) (time.Time, bool) {
	events, err := resilience.Store.GetAgentStatus(key.Harness, key.Model)
	if err != nil {
		return time.Time{}, false
	}
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].EventType {
		case "benched":
			resetAt, err := time.Parse(time.RFC3339, events[i].ResetAt)
			if err != nil {
				return time.Time{}, false
			}
			return resetAt, true
		case "active", "unfrozen", "frozen", "paused", "probation", "retry_failed":
			return time.Time{}, false
		}
	}
	return time.Time{}, false
}
