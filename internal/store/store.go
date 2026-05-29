package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const (
	triesWindowSize       = 200
	relaysWindowSize      = 50
	agentStatusWindowSize = 500
	messagesWindowSize    = 200
)

// Store provides unified read/write access to Rally's JSONL-backed storage.
type Store struct {
	dir   string
	cache *Cache
}

// NewStore creates a Store and loads all JSONL data into memory.
func NewStore(dir string) (*Store, error) {
	cache, err := LoadCache(dir)
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, cache: cache}, nil
}

// AppendTry appends a try record to JSONL and the cache.
func (s *Store) AppendTry(t TryRecord) error {
	path := filepath.Join(s.dir, "tries.jsonl")
	if err := appendJSONL(path, t); err != nil {
		return err
	}
	s.cache.Tries = append(s.cache.Tries, t)
	s.cache.TryIndex[t.ID] = len(s.cache.Tries) - 1
	if len(s.cache.Tries) > triesWindowSize {
		if err := commitThenTruncate(path, triesWindowSize); err != nil {
			return fmt.Errorf("truncate tries: %w", err)
		}
		// Reload cache after truncation
		c, err := LoadCache(s.dir)
		if err != nil {
			return fmt.Errorf("reload cache after truncate: %w", err)
		}
		s.cache = c
	}
	return nil
}

// AppendRelay appends a relay record to JSONL and the cache.
func (s *Store) AppendRelay(r RelayRecord) error {
	path := filepath.Join(s.dir, "relays.jsonl")
	if err := appendJSONL(path, r); err != nil {
		return err
	}
	s.cache.Relays = append(s.cache.Relays, r)
	s.cache.RelayIndex[r.ID] = len(s.cache.Relays) - 1
	if len(s.cache.Relays) > relaysWindowSize {
		if err := commitThenTruncate(path, relaysWindowSize); err != nil {
			return fmt.Errorf("truncate relays: %w", err)
		}
		c, err := LoadCache(s.dir)
		if err != nil {
			return fmt.Errorf("reload cache after truncate: %w", err)
		}
		s.cache = c
	}
	return nil
}

// ResetAgentStatus removes all agent status history to start fresh.
func (s *Store) ResetAgentStatus() error {
	path := filepath.Join(s.dir, "agent_status.jsonl")
	s.cache.AgentStatus = nil
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// AppendAgentStatus appends an agent status event to JSONL and the cache.
func (s *Store) AppendAgentStatus(e AgentStatusEvent) error {
	path := filepath.Join(s.dir, "agent_status.jsonl")
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
// events for any active frozen or probation entries that would otherwise be
// lost. This ensures that after truncation, the resilience state machine can
// still correctly identify agents that are frozen or in probation.
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
		if e.EventType != "frozen" && e.EventType != "probation" {
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
			AgentType: e.AgentType,
			Model:     e.Model,
			EventType: e.EventType,
			Timestamp: e.Timestamp,
			Reason:    "truncation summary",
		})
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Timestamp < summaries[j].Timestamp
	})

	kept := append(summaries, retained...)

	if err := commitThenTruncateWithContent(path, kept); err != nil {
		return err
	}

	return nil
}

// AddMessage appends a new message. It assigns the next position automatically
// if the caller left Position at 0.
func (s *Store) AddMessage(m MessageRecord) error {
	if m.Position == 0 {
		maxPos := 0
		for _, msg := range s.cache.Messages {
			if msg.Position > maxPos {
				maxPos = msg.Position
			}
		}
		m.Position = maxPos + 1
	}

	path := filepath.Join(s.dir, "messages.jsonl")
	if err := appendJSONL(path, m); err != nil {
		return err
	}
	s.cache.Messages = append(s.cache.Messages, m)
	s.cache.MessageIndex[m.ID] = len(s.cache.Messages) - 1
	// Pending messages are exempt from windowing.
	return nil
}

// UpdateMessage rewrites messages.jsonl with the updated record.
func (s *Store) UpdateMessage(m MessageRecord) error {
	idx, ok := s.cache.MessageIndex[m.ID]
	if !ok {
		return fmt.Errorf("message %d not found", m.ID)
	}
	s.cache.Messages[idx] = m
	s.cache.MessageIndex[m.ID] = idx

	path := filepath.Join(s.dir, "messages.jsonl")
	if err := rewriteJSONL(path, s.cache.Messages); err != nil {
		return err
	}

	// Window messages only when resolved/cancelled; pending exempt.
	if m.Status == "addressed" || m.Status == "cancelled" {
		if err := s.maybeTruncateMessages(); err != nil {
			return fmt.Errorf("truncate messages: %w", err)
		}
	}
	return nil
}

// maybeTruncateMessages removes oldest resolved/cancelled messages if they
// exceed the window size, while preserving all pending messages.
func (s *Store) maybeTruncateMessages() error {
	resolvedCount := 0
	for _, m := range s.cache.Messages {
		if m.Status != "pending" {
			resolvedCount++
		}
	}
	if resolvedCount <= messagesWindowSize {
		return nil
	}

	path := filepath.Join(s.dir, "messages.jsonl")

	// Determine which non-pending messages to keep (the most recent ones).
	keepCount := 0
	for i := len(s.cache.Messages) - 1; i >= 0; i-- {
		if s.cache.Messages[i].Status != "pending" {
			keepCount++
			if keepCount >= messagesWindowSize {
				// All messages before this index that are non-pending should be dropped.
				break
			}
		}
	}

	// Build new slice preserving order: pending + recent non-pending.
	var kept []MessageRecord
	dropThreshold := -1
	if keepCount >= messagesWindowSize {
		found := 0
		for i := len(s.cache.Messages) - 1; i >= 0; i-- {
			if s.cache.Messages[i].Status != "pending" {
				found++
				if found >= messagesWindowSize {
					dropThreshold = i
					break
				}
			}
		}
	}

	for i, m := range s.cache.Messages {
		if m.Status == "pending" {
			kept = append(kept, m)
		} else if dropThreshold >= 0 && i >= dropThreshold {
			kept = append(kept, m)
		} else if dropThreshold < 0 {
			// Not enough non-pending to trigger drop, keep all (shouldn't happen here).
			kept = append(kept, m)
		}
	}

	// Archive the full file, write the exact kept set, then commit the result.
	if err := commitThenTruncateWithContent(path, kept); err != nil {
		return err
	}

	// Rebuild cache
	c, err := LoadCache(s.dir)
	if err != nil {
		return fmt.Errorf("reload cache after message truncate: %w", err)
	}
	s.cache = c
	return nil
}

// GetTry returns a try by ID or nil if not found.
func (s *Store) GetTry(id int) *TryRecord {
	if idx, ok := s.cache.TryIndex[id]; ok {
		return &s.cache.Tries[idx]
	}
	return nil
}

// GetRelay returns a relay by ID or nil if not found.
func (s *Store) GetRelay(id int) *RelayRecord {
	if idx, ok := s.cache.RelayIndex[id]; ok {
		return &s.cache.Relays[idx]
	}
	return nil
}

// GetMessages returns all messages.
func (s *Store) GetMessages() []MessageRecord {
	out := make([]MessageRecord, len(s.cache.Messages))
	copy(out, s.cache.Messages)
	return out
}

// PendingMessages returns pending messages sorted by position (FIFO).
func (s *Store) PendingMessages() []MessageRecord {
	var out []MessageRecord
	for _, m := range s.cache.Messages {
		if m.Status == "pending" {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Position < out[j].Position
	})
	return out
}

// RelayScopedMessages returns pending messages with Scope == "relay" sorted by position.
func (s *Store) RelayScopedMessages() []MessageRecord {
	var out []MessageRecord
	for _, m := range s.cache.Messages {
		if m.Status == "pending" && m.Scope == "relay" {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Position < out[j].Position
	})
	return out
}

// EligibleRelayScopedMessages returns pending relay-scoped messages that have
// not been consumed by a different relay. Messages already consumed by the
// given relayID are included (for resume).
func (s *Store) EligibleRelayScopedMessages(relayID int) []MessageRecord {
	var out []MessageRecord
	for _, m := range s.cache.Messages {
		if m.Status == "pending" && m.Scope == "relay" {
			// Include if not consumed, or consumed by this relay
			if m.ConsumedByRelayID == nil || *m.ConsumedByRelayID == relayID {
				out = append(out, m)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Position < out[j].Position
	})
	return out
}

// ConsumedRunScopedMessageForRun returns a run-scoped message that was already
// consumed by the given runID but not addressed (i.e., from a failed run).
func (s *Store) ConsumedRunScopedMessageForRun(runID int) *MessageRecord {
	for _, m := range s.cache.Messages {
		if m.Status == "pending" && m.Scope != "relay" && m.ConsumedByRunID != nil && *m.ConsumedByRunID == runID {
			cp := m
			return &cp
		}
	}
	return nil
}

// GetAgentStatus returns all status events for a given agent type and model.
// When model is non-empty, events are filtered on both agent_type and model.
// When model is empty, only agent_type is matched (backward compatible).
func (s *Store) GetAgentStatus(agentType string, model string) []AgentStatusEvent {
	var out []AgentStatusEvent
	for _, e := range s.cache.AgentStatus {
		if e.AgentType != agentType {
			continue
		}
		if model != "" && e.Model != model {
			continue
		}
		out = append(out, e)
	}
	return out
}

// RecentTries returns the last n tries. If a relayID is provided and > 0, only tries
// matching that relay are returned.
func (s *Store) RecentTries(n int, relayID ...int) []TryRecord {
	if n <= 0 {
		return nil
	}

	tries := s.cache.Tries
	if len(relayID) > 0 && relayID[0] > 0 {
		rid := relayID[0]
		var filtered []TryRecord
		for _, t := range tries {
			if t.RelayID == rid {
				filtered = append(filtered, t)
			}
		}
		tries = filtered
	}

	start := len(tries) - n
	if start < 0 {
		start = 0
	}
	out := make([]TryRecord, len(tries)-start)
	copy(out, tries[start:])
	return out
}

// RecentRelays returns the last n relays.
func (s *Store) RecentRelays(n int) []RelayRecord {
	if n <= 0 {
		return nil
	}
	start := len(s.cache.Relays) - n
	if start < 0 {
		start = 0
	}
	out := make([]RelayRecord, len(s.cache.Relays)-start)
	copy(out, s.cache.Relays[start:])
	return out
}

// AllRelays returns all relays.
func (s *Store) AllRelays() []RelayRecord {
	out := make([]RelayRecord, len(s.cache.Relays))
	copy(out, s.cache.Relays)
	return out
}

// AllTries returns all tries.
func (s *Store) AllTries() []TryRecord {
	out := make([]TryRecord, len(s.cache.Tries))
	copy(out, s.cache.Tries)
	return out
}

// AllAgentStatus returns all agent status events.
func (s *Store) AllAgentStatus() []AgentStatusEvent {
	out := make([]AgentStatusEvent, len(s.cache.AgentStatus))
	copy(out, s.cache.AgentStatus)
	return out
}

// UpdateRelay rewrites relays.jsonl with the updated record.
func (s *Store) UpdateRelay(r RelayRecord) error {
	idx, ok := s.cache.RelayIndex[r.ID]
	if !ok {
		return fmt.Errorf("relay %d not found", r.ID)
	}
	s.cache.Relays[idx] = r

	path := filepath.Join(s.dir, "relays.jsonl")
	if err := rewriteJSONL(path, s.cache.Relays); err != nil {
		return err
	}

	if len(s.cache.Relays) > relaysWindowSize {
		if err := commitThenTruncate(path, relaysWindowSize); err != nil {
			return fmt.Errorf("truncate relays: %w", err)
		}
		c, err := LoadCache(s.dir)
		if err != nil {
			return fmt.Errorf("reload cache after truncate: %w", err)
		}
		s.cache = c
	}
	return nil
}

// NextRelayID returns the next available relay ID.
func (s *Store) NextRelayID() int {
	max := 0
	for _, r := range s.cache.Relays {
		if r.ID > max {
			max = r.ID
		}
	}
	return max + 1
}

// NextTryID returns the next available try ID.
func (s *Store) NextTryID() int {
	max := 0
	for _, t := range s.cache.Tries {
		if t.ID > max {
			max = t.ID
		}
	}
	return max + 1
}

// NextMessageID returns the next available message ID.
func (s *Store) NextMessageID() int {
	max := 0
	for _, m := range s.cache.Messages {
		if m.ID > max {
			max = m.ID
		}
	}
	return max + 1
}
