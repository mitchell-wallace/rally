package relay

import (
	"fmt"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/store"
)

type AgentState string

const (
	StateActive AgentState = "active"
	StatePaused AgentState = "paused"
	StateFrozen AgentState = "frozen"
)

// Resilience tracks per-agent-type pause/freeze state via agent_status.jsonl.
type Resilience struct {
	Store                     *store.Store
	PauseDuration             time.Duration
	HourlyRetriesBeforeFreeze int
	NowFunc                   func() time.Time
}

func NewResilience(s *store.Store) *Resilience {
	return &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		HourlyRetriesBeforeFreeze: 5,
		NowFunc:                   time.Now,
	}
}

func (r *Resilience) getState(agentType string) (AgentState, time.Time) {
	events := r.Store.GetAgentStatus(agentType)
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
		case "active", "unfrozen":
			state = StateActive
			since = t
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
	uniqueAgents := map[string]struct{}{}
	for _, a := range mix.Cycle {
		if _, ok := uniqueAgents[a.Harness]; ok {
			continue
		}
		uniqueAgents[a.Harness] = struct{}{}
		st, _ := r.getState(a.Harness)
		if st != StateFrozen {
			allFrozen = false
		}
		if st == StateActive {
			anyActive = true
		}
	}
	if allFrozen {
		return agent.ResolvedAgent{}, runIndex, false, fmt.Errorf("all agents frozen")
	}

	// Look for an agent starting at runIndex
	for i := 0; i < cycleLen; i++ {
		idx := (runIndex + i) % cycleLen
		a := mix.Cycle[idx]
		st, since := r.getState(a.Harness)
		switch st {
		case StateActive:
			return a, runIndex + i + 1, false, nil
		case StatePaused:
			if !r.NowFunc().Before(since.Add(r.PauseDuration)) {
				return a, runIndex + i + 1, true, nil
			}
		}
	}

	if !anyActive {
		return agent.ResolvedAgent{}, runIndex, false, fmt.Errorf("all agents paused")
	}
	return agent.ResolvedAgent{}, runIndex, false, fmt.Errorf("no active agent found")
}

func (r *Resilience) PauseAgent(agentType string, relayID int) error {
	st, _ := r.getState(agentType)
	if st != StateActive {
		return nil
	}
	return r.Store.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: agentType,
		EventType: "paused",
		Timestamp: r.NowFunc().UTC().Format(time.RFC3339),
		RelayID:   relayID,
		Reason:    "3 consecutive try failures",
	})
}

func (r *Resilience) UnpauseAgent(agentType string, relayID int) error {
	st, _ := r.getState(agentType)
	if st == StateActive {
		return nil
	}
	return r.Store.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: agentType,
		EventType: "active",
		Timestamp: r.NowFunc().UTC().Format(time.RFC3339),
		RelayID:   relayID,
		Reason:    "hourly retry succeeded",
	})
}

func (r *Resilience) RecordHourlyFailure(agentType string, relayID int) error {
	if err := r.Store.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: agentType,
		EventType: "retry_failed",
		Timestamp: r.NowFunc().UTC().Format(time.RFC3339),
		RelayID:   relayID,
		Reason:    "hourly retry failed",
	}); err != nil {
		return err
	}

	events := r.Store.GetAgentStatus(agentType)
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
		return r.FreezeAgent(agentType, relayID)
	}
	return nil
}

func (r *Resilience) FreezeAgent(agentType string, relayID int) error {
	st, _ := r.getState(agentType)
	if st == StateFrozen {
		return nil
	}
	return r.Store.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: agentType,
		EventType: "frozen",
		Timestamp: r.NowFunc().UTC().Format(time.RFC3339),
		RelayID:   relayID,
		Reason:    "5 hourly retries failed",
	})
}
