package runner

import (
	"time"

	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/routing"
	"github.com/mitchell-wallace/rally/internal/store"
)

func (r *routeRuntime) syncRecoverySignals(scheduler *routing.Scheduler, resilience *relaycore.Resilience, effectiveAssignee string) {
	for _, state := range scheduler.EntryStates() {
		key, err := r.resilienceKeyForEntry(state.Entry, effectiveAssignee)
		if err != nil {
			continue
		}

		// An operator-disabled provider sidelines its members for the whole
		// relay. Sideline before consulting resilience state so the StateActive
		// arm below cannot un-bench a disabled entry on a later sync.
		if r.providers.Disabled(key.Harness, key.Model) {
			scheduler.OnAgentFailed(state, "provider disabled", true)
			continue
		}

		status, since := resilience.GetState(key)
		switch status {
		case relaycore.StateActive:
			if state.Benched {
				scheduler.OnAgentRecovered(state)
			}
		case relaycore.StatePaused:
			if !resilience.NowFunc().Before(since.Add(resilience.PauseDuration)) {
				scheduler.ResetEntry(state)
			} else if !(state.Benched && state.Exhausted) {
				scheduler.OnAgentFailed(state, "paused", true)
			}
		case relaycore.StateProbation:
			// Probation is the freeze-decay window: a frozen agent has aged
			// past FreezeDuration and is granted exactly one tentative
			// recovery attempt per probation cycle. The one-shot is split
			// across two sync calls. On the first sync (no probation event
			// yet) we persist the event and unbench the entry so Next() can
			// pick it for the probation run. On any subsequent sync where
			// the state is still probation (e.g. the prior run didn't
			// resolve cleanly via UnpauseAgent/FreezeAgent), the entry is
			// re-benched so it cannot be selected again until the state
			// transitions. runOne is responsible for writing the active or
			// frozen event that ends the probation cycle.
			if !r.hasProbationEventForCurrentFreeze(resilience, key) {
				_ = persistProbationEvent(resilience, key)
				scheduler.ResetEntry(state)
			} else if !(state.Benched && state.Exhausted) {
				scheduler.OnAgentFailed(state, "probation", true)
			}
		case relaycore.StateFrozen:
			if !(state.Benched && state.Exhausted) {
				scheduler.OnAgentFailed(state, "frozen", true)
			}
		case relaycore.StateBenched:
			// A benched key is sidelined until its usage-limit reset deadline.
			// No StateActive-scoped unbench guard is needed: GetState only
			// reports StateBenched while now < reset_at, so once the deadline
			// passes the key surfaces as StateActive and the StateActive arm
			// above unbenches the entry for its single re-probe.
			scheduler.OnAgentFailed(state, "quota", true)
		}
	}
}

// hasProbationEventForCurrentFreeze returns true when the agent's event log
// already contains a probation event newer than the latest frozen event for
// this key. Used by syncRecoverySignals so the probation event is persisted
// exactly once per freeze cycle.
func (r *routeRuntime) hasProbationEventForCurrentFreeze(resilience *relaycore.Resilience, key relaycore.ResilienceKey) bool {
	events, err := resilience.Store.GetAgentStatus(key.Harness, key.Model)
	if err != nil {
		return false
	}
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].EventType {
		case "probation":
			return true
		case "frozen":
			return false
		}
	}
	return false
}

func persistProbationEvent(resilience *relaycore.Resilience, key relaycore.ResilienceKey) error {
	return resilience.Store.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: key.Harness,
		Model:     key.Model,
		EventType: "probation",
		Timestamp: resilience.NowFunc().UTC().Format(time.RFC3339),
		Reason:    "freeze decayed to probation",
	})
}

// forceUnpauseAll moves every paused harness across the runtime's schedulers
// back to active state. Used when the user hits skip during a frozen-wait to
// retry immediately rather than serving out the pause window.
func (r *routeRuntime) forceUnpauseAll(resilience *relaycore.Resilience, relayID int, routeName string, effectiveAssignee string) (int, error) {
	seen := map[relaycore.ResilienceKey]struct{}{}
	unpaused := 0
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
			status, _ := resilience.GetState(key)
			// A skip during the wait clears both pause and bench: UnpauseAgent
			// writes an active event for any non-active state, ending the bench
			// early so the lane retries immediately rather than serving out the
			// usage-limit reset window.
			if status != relaycore.StatePaused && status != relaycore.StateBenched {
				continue
			}
			if err := resilience.UnpauseAgent(key, relayID); err != nil {
				return unpaused, err
			}
			unpaused++
		}
	}
	return unpaused, nil
}
