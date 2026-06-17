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

	group := s.recoveryLapGroup(lapID)
	tries := s.recoveryTriesForLapGroup(group)
	resolvers := resolvingTriesByRun(tries)
	if len(resolvers) == 0 {
		return RecoveryPendingStatus{}
	}

	return recoveryStatusFromRunResolvers(lapID, resolvers, s.lapHasDirtyHandoffParent(lapID, group))
}

func (s *Store) recoveryLapGroup(lapID string) map[string]bool {
	group := map[string]bool{lapID: true}
	changed := true
	for changed {
		changed = false
		for _, tr := range s.cache.Tries {
			if !tr.DirtyHandoff || strings.TrimSpace(tr.LapID) == "" {
				continue
			}
			connected := group[tr.LapID]
			for _, created := range tr.HandoffCreatedLapIDs {
				if group[created] {
					connected = true
					break
				}
			}
			if !connected {
				continue
			}
			if !group[tr.LapID] {
				group[tr.LapID] = true
				changed = true
			}
			for _, created := range tr.HandoffCreatedLapIDs {
				created = strings.TrimSpace(created)
				if created == "" || group[created] {
					continue
				}
				group[created] = true
				changed = true
			}
		}
	}
	return group
}

func (s *Store) recoveryTriesForLapGroup(group map[string]bool) []TryRecord {
	var out []TryRecord
	for _, tr := range s.cache.Tries {
		if strings.TrimSpace(tr.LapID) == "" || !group[tr.LapID] {
			continue
		}
		out = append(out, tr)
	}
	return out
}

func (s *Store) lapHasDirtyHandoffParent(lapID string, group map[string]bool) bool {
	for _, tr := range s.cache.Tries {
		if !tr.DirtyHandoff || !group[tr.LapID] {
			continue
		}
		if tr.LapID != lapID && containsString(tr.HandoffCreatedLapIDs, lapID) {
			return true
		}
	}
	return false
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

func recoveryStatusFromRunResolvers(requestedLapID string, resolvers []TryRecord, continuationMatch bool) RecoveryPendingStatus {
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

	triggerLapID := latest.LapID
	if strings.TrimSpace(triggerLapID) == "" {
		triggerLapID = requestedLapID
	}
	status := RecoveryPendingStatus{
		TriggerLapID:             triggerLapID,
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
