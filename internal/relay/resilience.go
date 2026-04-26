package relay

import (
	"fmt"
	"time"

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
func (r *Resilience) SelectActiveAgent(mix AgentMix, runIndex int) (string, int, bool, error) {
	cycleLen := len(mix.Cycle)
	if cycleLen == 0 {
		return "claude", runIndex + 1, false, nil
	}

	allFrozen := true
	anyActive := false
	var pausedAgents []string
	uniqueAgents := map[string]struct{}{}
	for _, a := range mix.Cycle {
		if _, ok := uniqueAgents[a]; ok {
			continue
		}
		uniqueAgents[a] = struct{}{}
		st, _ := r.getState(a)
		if st != StateFrozen {
			allFrozen = false
		}
		if st == StateActive {
			anyActive = true
		} else if st == StatePaused {
			pausedAgents = append(pausedAgents, a)
		}
	}
	if allFrozen {
		return "", runIndex, false, fmt.Errorf("all agents frozen")
	}

	// Look for active agent starting at runIndex
	for i := 0; i < cycleLen; i++ {
		idx := (runIndex + i) % cycleLen
		a := mix.Cycle[idx]
		st, _ := r.getState(a)
		if st == StateActive {
			return a, runIndex + i + 1, false, nil
		}
	}

	// No active agents. If any paused agent is due for hourly retry, use it.
	if !anyActive {
		var selected string
		var selectedDue time.Time
		for _, a := range pausedAgents {
			st, pausedAt := r.getState(a)
			if st != StatePaused {
				continue
			}
			due := pausedAt.Add(r.PauseDuration)
			if selected == "" || due.Before(selectedDue) {
				selected = a
				selectedDue = due
			}
		}
		if selected != "" && !r.NowFunc().Before(selectedDue) {
			return selected, runIndex + 1, true, nil
		}
		return "", runIndex, false, fmt.Errorf("all agents paused")
	}

	return "", runIndex, false, fmt.Errorf("no active agent found")
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
		if e.EventType == "active" {
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
