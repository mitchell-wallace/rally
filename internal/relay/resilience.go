package relay

import (
	"fmt"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/store"
)

type AgentState string

const (
	StateActive    AgentState = "active"
	StatePaused    AgentState = "paused"
	StateFrozen    AgentState = "frozen"
	StateProbation AgentState = "probation"
)

// ResilienceKey identifies a specific harness+model pair for resilience tracking.
// Rate-limit and freeze state is tracked per harness-model combination.
type ResilienceKey struct {
	Harness string
	Model   string
}

// String returns the display form "harness:model" (or just "harness" when model is empty).
func (k ResilienceKey) String() string {
	if k.Model == "" {
		return k.Harness
	}
	return k.Harness + ":" + k.Model
}

// KeyFromAgent constructs a ResilienceKey from a ResolvedAgent.
func KeyFromAgent(a agent.ResolvedAgent) ResilienceKey {
	return ResilienceKey{Harness: a.Harness, Model: a.Model}
}

// Resilience tracks per-agent-type pause/freeze state via agent_status.jsonl.
type Resilience struct {
	Store                     *store.Store
	PauseDuration             time.Duration
	FreezeDuration            time.Duration
	HourlyRetriesBeforeFreeze int
	NowFunc                   func() time.Time
}

func NewResilience(s *store.Store) *Resilience {
	return &Resilience{
		Store:                     s,
		PauseDuration:             PauseDuration,
		FreezeDuration:            FreezeDuration,
		HourlyRetriesBeforeFreeze: HourlyRetriesBeforeFreeze,
		NowFunc:                   time.Now,
	}
}

func (r *Resilience) GetState(key ResilienceKey) (AgentState, time.Time) {
	events := r.Store.GetAgentStatus(key.Harness, key.Model)
	var state AgentState = StateActive
	var since time.Time
	for _, e := range events {
		t, err := time.Parse(time.RFC3339, e.Timestamp)
		if err != nil {
			continue
		}
		switch e.EventType {
		case "paused", "retry_failed":
			state = StatePaused
			since = t
		case "frozen":
			state = StateFrozen
			since = t
		case "probation":
			state = StateProbation
			since = t
		case "active", "unfrozen":
			state = StateActive
			since = t
		}
	}
	// Pure-read freeze decay: if the most recent state is frozen and it has
	// aged past FreezeDuration, surface it as probation. The on-disk event log
	// is untouched; syncRecoverySignals owns persisting the probation event.
	if state == StateFrozen && r.FreezeDuration > 0 && !since.IsZero() {
		if !r.NowFunc().Before(since.Add(r.FreezeDuration)) {
			state = StateProbation
		}
	}
	return state, since
}

// SelectActiveAgent returns the agent to use for the next run, the new runIndex
// to advance to, and whether the selected agent is undergoing an hourly retry.
func (r *Resilience) SelectActiveAgent(mix AgentMix, runIndex int) (agent.ResolvedAgent, int, bool, error) {
	cycleLen := len(mix.Cycle)
	if cycleLen == 0 {
		return agent.ResolvedAgent{Harness: "claude"}, runIndex + 1, false, nil
	}

	allFrozen := true
	anyActive := false
	anyProbation := false
	uniqueAgents := map[ResilienceKey]struct{}{}
	for _, a := range mix.Cycle {
		key := KeyFromAgent(a)
		if _, ok := uniqueAgents[key]; ok {
			continue
		}
		uniqueAgents[key] = struct{}{}
		st, _ := r.GetState(key)
		if st != StateFrozen {
			allFrozen = false
		}
		if st == StateActive {
			anyActive = true
		}
		if st == StateProbation {
			anyProbation = true
		}
	}
	if allFrozen {
		return agent.ResolvedAgent{}, runIndex, false, fmt.Errorf("all agents frozen")
	}

	// Look for an agent starting at runIndex
	for i := 0; i < cycleLen; i++ {
		idx := (runIndex + i) % cycleLen
		a := mix.Cycle[idx]
		key := KeyFromAgent(a)
		st, since := r.GetState(key)
		switch st {
		case StateActive:
			return a, runIndex + i + 1, false, nil
		case StateProbation:
			return a, runIndex + i + 1, false, nil
		case StatePaused:
			if !r.NowFunc().Before(since.Add(r.PauseDuration)) {
				return a, runIndex + i + 1, true, nil
			}
		}
	}

	if !anyActive && !anyProbation {
		return agent.ResolvedAgent{}, runIndex, false, fmt.Errorf("all agents paused")
	}
	return agent.ResolvedAgent{}, runIndex, false, fmt.Errorf("no active agent found")
}

func (r *Resilience) PauseAgent(key ResilienceKey, relayID int) error {
	st, _ := r.GetState(key)
	if st != StateActive {
		return nil
	}
	return r.Store.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: key.Harness,
		Model:     key.Model,
		EventType: "paused",
		Timestamp: r.NowFunc().UTC().Format(time.RFC3339),
		RelayID:   relayID,
		Reason:    "3 consecutive try failures",
	})
}

func (r *Resilience) UnpauseAgent(key ResilienceKey, relayID int) error {
	st, _ := r.GetState(key)
	if st == StateActive {
		return nil
	}
	return r.Store.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: key.Harness,
		Model:     key.Model,
		EventType: "active",
		Timestamp: r.NowFunc().UTC().Format(time.RFC3339),
		RelayID:   relayID,
		Reason:    "hourly retry succeeded",
	})
}

func (r *Resilience) RecordHourlyFailure(key ResilienceKey, relayID int) error {
	if err := r.Store.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: key.Harness,
		Model:     key.Model,
		EventType: "retry_failed",
		Timestamp: r.NowFunc().UTC().Format(time.RFC3339),
		RelayID:   relayID,
		Reason:    "hourly retry failed",
	}); err != nil {
		return err
	}

	events := r.Store.GetAgentStatus(key.Harness, key.Model)
	retryFailedCount := 0
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.EventType == "active" || e.EventType == "frozen" || e.EventType == "probation" {
			break
		}
		if e.EventType == "retry_failed" {
			retryFailedCount++
		}
	}
	if retryFailedCount >= r.HourlyRetriesBeforeFreeze {
		return r.FreezeAgent(key, relayID, "hourly retry threshold reached")
	}
	return nil
}

func (r *Resilience) FreezeAgent(key ResilienceKey, relayID int, reason string) error {
	st, _ := r.GetState(key)
	if st == StateFrozen {
		return nil
	}
	return r.Store.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: key.Harness,
		Model:     key.Model,
		EventType: "frozen",
		Timestamp: r.NowFunc().UTC().Format(time.RFC3339),
		RelayID:   relayID,
		Reason:    reason,
	})
}

// persistProbationEvent appends a probation event for the given key. Callers
// (currently syncRecoverySignals) are responsible for the once-per-cycle
// guard; this method does not check whether a probation event already exists.
func (r *Resilience) persistProbationEvent(key ResilienceKey) error {
	return r.Store.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: key.Harness,
		Model:     key.Model,
		EventType: "probation",
		Timestamp: r.NowFunc().UTC().Format(time.RFC3339),
		Reason:    "freeze decayed to probation",
	})
}
