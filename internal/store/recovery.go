package store

import (
	"sort"
	"strings"

	"github.com/mitchell-wallace/rally/internal/reliability"
)

const RecoveryRouteName = "recovery"

const RecoveryRouteConsecutiveCap = 2

// RecoveryPendingStatus is derived only from persisted try records. It is used
// by relay routing to force the next run through the recovery route without
// mutating the laps queue.
type RecoveryPendingStatus struct {
	Pending                  bool
	CapHit                   bool
	ConsecutiveRecoveryRuns  int
	TriggerLapID             string
	ResolvingRunID           int
	ResolvingTryID           int
	ResolvingOutcome         reliability.TryOutcome
	ResolvingDirtyHandoff    bool
	HandoffContinuationMatch bool
}

// RecoveryPendingForLap evaluates whether the claimed, not-done lap should be
// routed through recovery. A lap can match either its own most recent resolving
// try, or a dirty handoff try from an original lap that created this lap as a
// head followup.
func (s *Store) RecoveryPendingForLap(lapID string) RecoveryPendingStatus {
	lapID = strings.TrimSpace(lapID)
	if lapID == "" {
		return RecoveryPendingStatus{}
	}

	candidates := s.recoveryCandidatesForLap(lapID)
	if len(candidates) == 0 {
		return RecoveryPendingStatus{}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].latestTryID() > candidates[j].latestTryID()
	})

	candidate := candidates[0]
	return recoveryStatusFromRunResolvers(candidate.lapID, candidate.resolvers, candidate.continuationMatch)
}

type recoveryCandidate struct {
	lapID             string
	resolvers         []TryRecord
	continuationMatch bool
}

// latestTryID returns the global try ID of the most recent resolver. Try IDs
// are monotonic across the whole store (including relay restarts), so they give
// a stable recency ordering where relay-local RunIDs would collide or misorder.
func (c recoveryCandidate) latestTryID() int {
	if len(c.resolvers) == 0 {
		return 0
	}
	return c.resolvers[len(c.resolvers)-1].ID
}

func (s *Store) recoveryCandidatesForLap(lapID string) []recoveryCandidate {
	byLap := make(map[string][]TryRecord)
	continuation := make(map[string]bool)
	for _, tr := range s.cache.Tries {
		if strings.TrimSpace(tr.LapID) == "" {
			continue
		}
		if tr.LapID == lapID {
			byLap[tr.LapID] = append(byLap[tr.LapID], tr)
			continue
		}
		if tr.DirtyHandoff && containsString(tr.HandoffCreatedLapIDs, lapID) {
			byLap[tr.LapID] = append(byLap[tr.LapID], tr)
			continuation[tr.LapID] = true
		}
	}

	out := make([]recoveryCandidate, 0, len(byLap))
	for candidateLapID, tries := range byLap {
		resolvers := resolvingTriesByRun(tries)
		if len(resolvers) == 0 {
			continue
		}
		out = append(out, recoveryCandidate{
			lapID:             candidateLapID,
			resolvers:         resolvers,
			continuationMatch: continuation[candidateLapID],
		})
	}
	return out
}

// runIdentity uniquely identifies a run across relay restarts. RunID is
// relay-local (each relay numbers its runs from 1), so two distinct runs in
// different relays can share a RunID; pairing it with RelayID keeps them apart.
type runIdentity struct {
	relayID int
	runID   int
}

func resolvingTriesByRun(tries []TryRecord) []TryRecord {
	latestByRun := make(map[runIdentity]TryRecord)
	for _, tr := range tries {
		if tr.RunID <= 0 {
			continue
		}
		key := runIdentity{relayID: tr.RelayID, runID: tr.RunID}
		existing, ok := latestByRun[key]
		if !ok || tr.AttemptNumber > existing.AttemptNumber || (tr.AttemptNumber == existing.AttemptNumber && tr.ID > existing.ID) {
			latestByRun[key] = tr
		}
	}

	out := make([]TryRecord, 0, len(latestByRun))
	for _, tr := range latestByRun {
		out = append(out, tr)
	}
	// Order by global try ID so runs are chronological across relay restarts.
	// Relay-local RunID ordering would interleave or collapse runs from
	// different relays, breaking most-recent selection and consecutive
	// recovery-cap counting.
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func recoveryStatusFromRunResolvers(lapID string, resolvers []TryRecord, continuationMatch bool) RecoveryPendingStatus {
	if len(resolvers) == 0 {
		return RecoveryPendingStatus{}
	}

	latest := resolvers[len(resolvers)-1]
	triggered := latest.Outcome == reliability.OutcomeHandoffTimeout || latest.DirtyHandoff
	if !triggered {
		return RecoveryPendingStatus{}
	}

	consecutiveRecoveryRuns := 0
	for i := len(resolvers) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(resolvers[i].ResolvedRoute), RecoveryRouteName) {
			break
		}
		consecutiveRecoveryRuns++
	}

	status := RecoveryPendingStatus{
		TriggerLapID:             lapID,
		ResolvingRunID:           latest.RunID,
		ResolvingTryID:           latest.ID,
		ResolvingOutcome:         latest.Outcome,
		ResolvingDirtyHandoff:    latest.DirtyHandoff,
		ConsecutiveRecoveryRuns:  consecutiveRecoveryRuns,
		HandoffContinuationMatch: continuationMatch,
	}
	if consecutiveRecoveryRuns >= RecoveryRouteConsecutiveCap {
		status.CapHit = true
		return status
	}
	status.Pending = true
	return status
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
