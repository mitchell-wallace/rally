package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// AddMessage appends a new message. It assigns the next position automatically
// if the caller left Position at 0.
func (s *Store) AddMessage(m MessageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.Position == 0 {
		maxPos := 0
		for _, msg := range s.cache.Messages {
			if msg.Position > maxPos {
				maxPos = msg.Position
			}
		}
		m.Position = maxPos + 1
	}

	if err := os.MkdirAll(s.stateDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.stateDir, "messages.jsonl")
	if err := appendJSONL(path, m); err != nil {
		return err
	}
	s.cache.Messages = append(s.cache.Messages, m)
	s.cache.MessageIndex[m.ID] = len(s.cache.Messages) - 1
	return nil
}

// UpdateMessage rewrites messages.jsonl with the updated record.
func (s *Store) UpdateMessage(m MessageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, ok := s.cache.MessageIndex[m.ID]
	if !ok {
		return fmt.Errorf("message %d not found", m.ID)
	}
	s.cache.Messages[idx] = m
	s.cache.MessageIndex[m.ID] = idx

	path := filepath.Join(s.stateDir, "messages.jsonl")
	if err := rewriteJSONL(path, s.cache.Messages); err != nil {
		return err
	}

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

	path := filepath.Join(s.stateDir, "messages.jsonl")

	keepCount := 0
	for i := len(s.cache.Messages) - 1; i >= 0; i-- {
		if s.cache.Messages[i].Status != "pending" {
			keepCount++
			if keepCount >= messagesWindowSize {
				break
			}
		}
	}

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
			kept = append(kept, m)
		}
	}

	if err := rewriteJSONL(path, kept); err != nil {
		return err
	}

	c, err := LoadCache(s.dir)
	if err != nil {
		return fmt.Errorf("reload cache after message truncate: %w", err)
	}
	s.cache = c
	return nil
}

// GetMessages returns all messages.
func (s *Store) GetMessages() []MessageRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]MessageRecord, len(s.cache.Messages))
	copy(out, s.cache.Messages)
	return out
}

// PendingMessages returns pending messages sorted by position (FIFO).
func (s *Store) PendingMessages() []MessageRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
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

// ConsumedOutingScopedMessageForOuting returns an outing-scoped message that was
// already consumed by the given outingID but not addressed. The persisted scope
// value is still "run" for compatibility with existing state.
func (s *Store) ConsumedOutingScopedMessageForOuting(outingID int) *MessageRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.cache.Messages {
		if m.Status == "pending" && m.Scope != "relay" && m.ConsumedByOutingID != nil && *m.ConsumedByOutingID == outingID {
			cp := m
			return &cp
		}
	}
	return nil
}

// NextMessageID returns the next available message ID.
func (s *Store) NextMessageID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	max := 0
	for _, m := range s.cache.Messages {
		if m.ID > max {
			max = m.ID
		}
	}
	return max + 1
}
