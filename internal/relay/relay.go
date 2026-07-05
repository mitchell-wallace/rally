package relay

import (
	"fmt"
	"time"

	"github.com/mitchell-wallace/rally/internal/store"
)

// End reasons recorded on RelayRecord.EndReason when a relay is closed.
// Cancellation/interrupt deliberately has no reason: a stopped relay keeps
// EndedAt empty so it stays resumable, and is only closed later by target
// completion or an explicit discard.
const (
	EndReasonCompleted   = "completed"
	EndReasonQueueEmpty  = "queue_empty"
	EndReasonConfigError = "config_error"
	EndReasonDiscarded   = "discarded"
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
	return EndRelay(s, relayID, EndReasonCompleted)
}

func EndRelay(s *store.Store, relayID int, reason string) error {
	r := s.GetRelay(relayID)
	if r == nil {
		return fmt.Errorf("relay %d not found", relayID)
	}
	r.EndedAt = time.Now().UTC().Format(time.RFC3339)
	r.EndReason = reason
	return s.UpdateRelay(*r)
}
