package relay

import (
	"fmt"
	"time"

	"github.com/mitchell-wallace/rally/internal/store"
)

func CreateRelay(s *store.Store, targetIterations int, agentMix string) (*store.RelayRecord, error) {
	id := s.NextRelayID()
	r := store.RelayRecord{
		ID:               id,
		TargetIterations: targetIterations,
		AgentMix:         agentMix,
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.AppendRelay(r); err != nil {
		return nil, err
	}
	return &r, nil
}

func ResumeRelay(s *store.Store) (*store.RelayRecord, bool, error) {
	relays := s.AllRelays()
	for i := len(relays) - 1; i >= 0; i-- {
		r := relays[i]
		if r.EndedAt == "" && r.CompletedIterations < r.TargetIterations {
			cp := r
			return &cp, true, nil
		}
	}
	return nil, false, nil
}

func CompleteRelay(s *store.Store, relayID int) error {
	r := s.GetRelay(relayID)
	if r == nil {
		return fmt.Errorf("relay %d not found", relayID)
	}
	r.EndedAt = time.Now().UTC().Format(time.RFC3339)
	return s.UpdateRelay(*r)
}
