package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ResetAgentStatus removes all agent status history to start fresh.
func (s *Store) ResetAgentStatus() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.stateDir, "agent_status.jsonl")
	s.cache.AgentStatus = nil
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// AppendAgentStatus appends an agent status event to JSONL and the cache.
func (s *Store) AppendAgentStatus(e AgentStatusEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.stateDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.stateDir, "agent_status.jsonl")
	if err := appendJSONL(path, e); err != nil {
		return err
	}
	s.cache.AgentStatus = append(s.cache.AgentStatus, e)
	if len(s.cache.AgentStatus) > agentStatusWindowSize {
		if err := s.truncateAgentStatus(path); err != nil {
			return fmt.Errorf("truncate agent_status: %w", err)
		}
		c, err := LoadCache(s.dir)
		if err != nil {
			return fmt.Errorf("reload cache after truncate: %w", err)
		}
		s.cache = c
	}
	return nil
}

// truncateAgentStatus truncates the agent status log while preserving summary
// events for any active frozen, probation, or benched entries that would
// otherwise be lost. This ensures that after truncation, the resilience state
// machine can still correctly identify agents that are frozen, in probation, or
// benched (a multi-day usage-limit reset must not be truncated away).
func (s *Store) truncateAgentStatus(path string) error {
	events := s.cache.AgentStatus
	if len(events) <= agentStatusWindowSize {
		return nil
	}

	dropped := events[:len(events)-agentStatusWindowSize]
	retained := events[len(events)-agentStatusWindowSize:]

	type agentKey struct {
		AgentType string
		Model     string
	}
	summaryEvents := make(map[agentKey]AgentStatusEvent)

	for _, e := range dropped {
		if e.EventType != "frozen" && e.EventType != "probation" && e.EventType != "benched" {
			continue
		}
		key := agentKey{AgentType: e.AgentType, Model: e.Model}
		if existing, ok := summaryEvents[key]; ok {
			if e.Timestamp > existing.Timestamp {
				summaryEvents[key] = e
			}
		} else {
			summaryEvents[key] = e
		}
	}

	var summaries []AgentStatusEvent
	for _, e := range summaryEvents {
		summaries = append(summaries, AgentStatusEvent{
			AgentType:  e.AgentType,
			Model:      e.Model,
			EventType:  e.EventType,
			Timestamp:  e.Timestamp,
			ResetAt:    e.ResetAt,
			QuotaScope: e.QuotaScope,
			Reason:     "truncation summary",
		})
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Timestamp < summaries[j].Timestamp
	})

	kept := append(summaries, retained...)

	if err := rewriteJSONL(path, kept); err != nil {
		return err
	}

	return nil
}

// GetAgentStatus returns all status events for a given agent type and model.
func (s *Store) GetAgentStatus(agentType string, model string) ([]AgentStatusEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if model == "" {
		return nil, fmt.Errorf("GetAgentStatus: model is required")
	}
	var out []AgentStatusEvent
	for _, e := range s.cache.AgentStatus {
		if e.AgentType != agentType {
			continue
		}
		if e.Model != model {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// AllAgentStatus returns all agent status events.
func (s *Store) AllAgentStatus() []AgentStatusEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AgentStatusEvent, len(s.cache.AgentStatus))
	copy(out, s.cache.AgentStatus)
	return out
}
