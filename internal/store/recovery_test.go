package store

import (
	"testing"

	"github.com/mitchell-wallace/rally/internal/reliability"
)

func TestRecoveryPendingForLapUsesResolvingTryOfMostRecentRun(t *testing.T) {
	_, s := setupTempStore(t)

	mustAppendTry(t, s, TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeRunTimeout})
	mustAppendTry(t, s, TryRecord{ID: 2, RunID: 1, LapID: "lap-1", AttemptNumber: 2, HandoffOnly: true, Outcome: reliability.OutcomeHandoffTimeout})

	status := s.RecoveryPendingForLap("lap-1")
	if !status.Pending {
		t.Fatalf("Pending = false, want true: %+v", status)
	}
	if status.ResolvingTryID != 2 || status.ResolvingRunID != 1 {
		t.Fatalf("resolver = run %d try %d, want run 1 try 2", status.ResolvingRunID, status.ResolvingTryID)
	}
	if status.ResolvingOutcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("ResolvingOutcome = %q, want handoff_timeout", status.ResolvingOutcome)
	}
}

func TestRecoveryPendingForLapTriggers(t *testing.T) {
	tests := []struct {
		name string
		rec  TryRecord
		want bool
	}{
		{
			name: "dirty handoff",
			rec:  TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffRequested, DirtyHandoff: true},
			want: true,
		},
		{
			name: "handoff timeout",
			rec:  TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout},
			want: true,
		},
		{
			name: "ordinary failed",
			rec:  TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeFailed},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, s := setupTempStore(t)
			mustAppendTry(t, s, tt.rec)
			if got := s.RecoveryPendingForLap("lap-1").Pending; got != tt.want {
				t.Fatalf("Pending = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecoveryPendingForLapMatchesDirtyHandoffFollowupAfterReload(t *testing.T) {
	rallyDir, s := setupTempStore(t)
	mustAppendTry(t, s, TryRecord{
		ID:                   1,
		RunID:                1,
		LapID:                "original",
		AttemptNumber:        1,
		Outcome:              reliability.OutcomeHandoffRequested,
		DirtyHandoff:         true,
		HandoffCreatedLapIDs: []string{"followup"},
	})

	reloaded, err := NewStore(rallyDir)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}

	status := reloaded.RecoveryPendingForLap("followup")
	if !status.Pending {
		t.Fatalf("Pending = false, want true: %+v", status)
	}
	if status.TriggerLapID != "original" {
		t.Fatalf("TriggerLapID = %q, want original", status.TriggerLapID)
	}
	if !status.HandoffContinuationMatch {
		t.Fatal("HandoffContinuationMatch = false, want true")
	}
}

func TestRecoveryPendingForLapFollowupClearsAfterNewerCleanRun(t *testing.T) {
	_, s := setupTempStore(t)
	mustAppendTry(t, s, TryRecord{
		ID:                   1,
		RunID:                1,
		LapID:                "original",
		AttemptNumber:        1,
		Outcome:              reliability.OutcomeHandoffRequested,
		DirtyHandoff:         true,
		HandoffCreatedLapIDs: []string{"followup"},
	})
	mustAppendTry(t, s, TryRecord{
		ID:            2,
		RunID:         2,
		LapID:         "followup",
		AttemptNumber: 1,
		Outcome:       reliability.OutcomeCompleted,
		ResolvedRoute: "recovery",
	})

	status := s.RecoveryPendingForLap("followup")
	if status.Pending || status.CapHit {
		t.Fatalf("status = %+v, want recovery cleared by newer followup run", status)
	}
}

func TestRecoveryPendingForLapFollowupClearsAfterNewerOriginalRun(t *testing.T) {
	_, s := setupTempStore(t)
	mustAppendTry(t, s, TryRecord{
		ID:                   1,
		RunID:                1,
		LapID:                "original",
		AttemptNumber:        1,
		Outcome:              reliability.OutcomeHandoffRequested,
		DirtyHandoff:         true,
		HandoffCreatedLapIDs: []string{"followup"},
	})
	mustAppendTry(t, s, TryRecord{
		ID:            2,
		RunID:         2,
		LapID:         "original",
		AttemptNumber: 1,
		Outcome:       reliability.OutcomeCompleted,
		ResolvedRoute: "recovery",
	})

	status := s.RecoveryPendingForLap("followup")
	if status.Pending || status.CapHit {
		t.Fatalf("status = %+v, want recovery cleared by newer original-lap recovery run", status)
	}
}

func TestRecoveryPendingForLapCapCountsOriginalAndFollowupGroup(t *testing.T) {
	_, s := setupTempStore(t)
	mustAppendTry(t, s, TryRecord{
		ID:                   1,
		RunID:                1,
		LapID:                "original",
		AttemptNumber:        1,
		Outcome:              reliability.OutcomeHandoffRequested,
		DirtyHandoff:         true,
		HandoffCreatedLapIDs: []string{"followup"},
	})
	mustAppendTry(t, s, TryRecord{
		ID:            2,
		RunID:         2,
		LapID:         "original",
		AttemptNumber: 1,
		Outcome:       reliability.OutcomeHandoffTimeout,
		ResolvedRoute: "recovery",
	})
	mustAppendTry(t, s, TryRecord{
		ID:            3,
		RunID:         3,
		LapID:         "followup",
		AttemptNumber: 1,
		Outcome:       reliability.OutcomeHandoffTimeout,
		ResolvedRoute: "recovery",
	})

	status := s.RecoveryPendingForLap("original")
	if !status.CapHit {
		t.Fatalf("CapHit = false, want grouped recovery cap hit: %+v", status)
	}
	if status.ConsecutiveRecoveryRuns != RecoveryRouteConsecutiveCap {
		t.Fatalf("ConsecutiveRecoveryRuns = %d, want %d", status.ConsecutiveRecoveryRuns, RecoveryRouteConsecutiveCap)
	}
}

func TestRecoveryPendingForLapConsecutiveRecoveryCap(t *testing.T) {
	_, s := setupTempStore(t)
	mustAppendTry(t, s, TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "recovery"})
	mustAppendTry(t, s, TryRecord{ID: 2, RunID: 2, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "RECOVERY"})

	status := s.RecoveryPendingForLap("lap-1")
	if !status.CapHit {
		t.Fatalf("CapHit = false, want true: %+v", status)
	}
	if status.Pending {
		t.Fatal("Pending = true, want false on cap hit")
	}
	if status.ConsecutiveRecoveryRuns != RecoveryRouteConsecutiveCap {
		t.Fatalf("ConsecutiveRecoveryRuns = %d, want %d", status.ConsecutiveRecoveryRuns, RecoveryRouteConsecutiveCap)
	}
}

func TestRecoveryPendingForLapSelectsMostRecentRunAcrossRelays(t *testing.T) {
	_, s := setupTempStore(t)
	// Relay 1 reached run 2 and completed the lap cleanly before exiting.
	mustAppendTry(t, s, TryRecord{ID: 1, RelayID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeRunTimeout})
	mustAppendTry(t, s, TryRecord{ID: 2, RelayID: 1, RunID: 2, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeCompleted})
	// A later relay restarts run numbering at 1 (RunID collides with relay 1's
	// first run) and hands off. The newest run owns the routing decision.
	mustAppendTry(t, s, TryRecord{ID: 3, RelayID: 2, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout})

	status := s.RecoveryPendingForLap("lap-1")
	if !status.Pending {
		t.Fatalf("Pending = false, want true: newest run (relay 2 run 1) handed off: %+v", status)
	}
	if status.ResolvingTryID != 3 || status.ResolvingRunID != 1 {
		t.Fatalf("resolver = run %d try %d, want run 1 try 3 (relay 2)", status.ResolvingRunID, status.ResolvingTryID)
	}
}

func TestRecoveryPendingForLapCapCountsRunsAcrossRelayRestart(t *testing.T) {
	rallyDir, s := setupTempStore(t)
	// Relay 1, run 1: recovery-routed handoff.
	mustAppendTry(t, s, TryRecord{ID: 1, RelayID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "recovery"})
	// Relay restarts; run numbering resets so this run shares RunID 1 with the
	// relay-1 run above. It must count as a distinct consecutive recovery run.
	mustAppendTry(t, s, TryRecord{ID: 2, RelayID: 2, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "recovery"})

	reloaded, err := NewStore(rallyDir)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}

	status := reloaded.RecoveryPendingForLap("lap-1")
	if !status.CapHit {
		t.Fatalf("CapHit = false, want true: two recovery runs across a relay restart should hit the cap: %+v", status)
	}
	if status.Pending {
		t.Fatal("Pending = true, want false on cap hit")
	}
	if status.ConsecutiveRecoveryRuns != RecoveryRouteConsecutiveCap {
		t.Fatalf("ConsecutiveRecoveryRuns = %d, want %d (runs sharing RunID across relays must count separately)", status.ConsecutiveRecoveryRuns, RecoveryRouteConsecutiveCap)
	}
}
