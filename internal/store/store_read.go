package store

// GetTry returns a try by ID or nil if not found.
func (s *Store) GetTry(id int) *TryRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx, ok := s.cache.TryIndex[id]; ok {
		return &s.cache.Tries[idx]
	}
	return nil
}

// GetRelay returns a relay by ID or nil if not found.
func (s *Store) GetRelay(id int) *RelayRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx, ok := s.cache.RelayIndex[id]; ok {
		return &s.cache.Relays[idx]
	}
	return nil
}

// RecentTries returns the last n tries. If a relayID is provided and > 0, only tries
// matching that relay are returned.
func (s *Store) RecentTries(n int, relayID ...int) []TryRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RelayRecord, len(s.cache.Relays))
	copy(out, s.cache.Relays)
	return out
}

// AllTries returns all tries.
func (s *Store) AllTries() []TryRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TryRecord, len(s.cache.Tries))
	copy(out, s.cache.Tries)
	return out
}
