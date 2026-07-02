package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// AppendTry appends a try record to JSONL and the cache.
func (s *Store) AppendTry(t TryRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(t.CommitHistory) > 0 {
		t.CommitHash = t.CommitHistory[len(t.CommitHistory)-1]
	} else if t.CommitHash != "" {
		t.CommitHistory = []string{t.CommitHash}
	}
	t.Summary = TruncateFinalSnippet(t.Summary)
	t.RemainingWork = TruncateFinalSnippet(t.RemainingWork)

	if err := os.MkdirAll(s.stateDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.stateDir, "tries.jsonl")
	if err := appendJSONL(path, t); err != nil {
		return err
	}
	s.cache.Tries = append(s.cache.Tries, t)
	s.cache.TryIndex[t.ID] = len(s.cache.Tries) - 1
	return nil
}

// AppendRelay appends a relay record to JSONL and the cache.
func (s *Store) AppendRelay(r RelayRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.stateDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.stateDir, "relays.jsonl")
	if err := appendJSONL(path, r); err != nil {
		return err
	}
	s.cache.Relays = append(s.cache.Relays, r)
	s.cache.RelayIndex[r.ID] = len(s.cache.Relays) - 1
	return nil
}

// UpdateRelay rewrites relays.jsonl with the updated record.
func (s *Store) UpdateRelay(r RelayRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, ok := s.cache.RelayIndex[r.ID]
	if !ok {
		return fmt.Errorf("relay %d not found", r.ID)
	}
	s.cache.Relays[idx] = r

	path := filepath.Join(s.stateDir, "relays.jsonl")
	if err := rewriteJSONL(path, s.cache.Relays); err != nil {
		return err
	}

	return nil
}

// NextRelayID returns the next available relay ID.
func (s *Store) NextRelayID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	max := 0
	for _, t := range s.cache.Tries {
		if t.ID > max {
			max = t.ID
		}
	}
	return max + 1
}
