package runner

import (
	"testing"

	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestTallyOutingsRetryThenSuccess(t *testing.T) {
	// One outing, two attempts: the first fails, the second completes. The run
	// should count as a single pass and zero failures.
	tries := []store.TryRecord{
		{ID: 1, RelayID: 1, OutingID: 1, AttemptNumber: 1, Completed: false},
		{ID: 2, RelayID: 1, OutingID: 1, AttemptNumber: 2, Completed: true},
	}

	pass, fail, cancelled := tallyOutings(tries, 1)
	if pass != 1 || fail != 0 || cancelled != 0 {
		t.Fatalf("retry-then-success: want 1 pass / 0 fail / 0 cancelled, got %d pass / %d fail / %d cancelled", pass, fail, cancelled)
	}
}

func TestTallyOutingsAllExhausted(t *testing.T) {
	// One outing, all attempts exhausted without completion: one failure.
	tries := []store.TryRecord{
		{ID: 1, RelayID: 1, OutingID: 1, AttemptNumber: 1, Completed: false},
		{ID: 2, RelayID: 1, OutingID: 1, AttemptNumber: 2, Completed: false},
		{ID: 3, RelayID: 1, OutingID: 1, AttemptNumber: 3, Completed: false},
	}

	pass, fail, cancelled := tallyOutings(tries, 1)
	if pass != 0 || fail != 1 || cancelled != 0 {
		t.Fatalf("all-exhausted: want 0 pass / 1 fail / 0 cancelled, got %d pass / %d fail / %d cancelled", pass, fail, cancelled)
	}
}

func TestTallyOutingsAggregatesMultipleRuns(t *testing.T) {
	// Two outings in the relay: outing 1 succeeds after a retry, outing 2 exhausts its
	// budget. Tries from a different relay must be ignored entirely.
	tries := []store.TryRecord{
		{ID: 1, RelayID: 1, OutingID: 1, AttemptNumber: 1, Completed: false},
		{ID: 2, RelayID: 1, OutingID: 1, AttemptNumber: 2, Completed: true},
		{ID: 3, RelayID: 1, OutingID: 2, AttemptNumber: 1, Completed: false},
		{ID: 4, RelayID: 1, OutingID: 2, AttemptNumber: 2, Completed: false},
		// Different relay — should not be counted.
		{ID: 5, RelayID: 2, OutingID: 3, AttemptNumber: 1, Completed: false},
	}

	pass, fail, cancelled := tallyOutings(tries, 1)
	if pass != 1 || fail != 1 || cancelled != 0 {
		t.Fatalf("multi-outing: want 1 pass / 1 fail / 0 cancelled, got %d pass / %d fail / %d cancelled", pass, fail, cancelled)
	}
}

func TestTallyOutingsCancelledNotFailed(t *testing.T) {
	tries := []store.TryRecord{
		{ID: 1, RelayID: 1, OutingID: 1, AttemptNumber: 1, Completed: false, Outcome: reliability.OutcomeCancelled, CancellationSource: "skip"},
		{ID: 2, RelayID: 1, OutingID: 2, AttemptNumber: 1, Completed: false, Outcome: reliability.OutcomeFailed},
		{ID: 3, RelayID: 1, OutingID: 3, AttemptNumber: 1, Completed: true, Outcome: reliability.OutcomeCompleted},
	}

	pass, fail, cancelled := tallyOutings(tries, 1)
	if pass != 1 || fail != 1 || cancelled != 1 {
		t.Fatalf("cancelled tally: want 1 pass / 1 fail / 1 cancelled, got %d pass / %d fail / %d cancelled", pass, fail, cancelled)
	}
}

func TestTallyOutingsAllCancellationSourcesNotFailed(t *testing.T) {
	tries := []store.TryRecord{
		{ID: 1, RelayID: 1, OutingID: 1, AttemptNumber: 1, Completed: false, Outcome: reliability.OutcomeCancelled, CancellationSource: "skip"},
		{ID: 2, RelayID: 1, OutingID: 2, AttemptNumber: 1, Completed: false, Outcome: reliability.OutcomeCancelled, CancellationSource: "graceful_stop"},
		{ID: 3, RelayID: 1, OutingID: 3, AttemptNumber: 1, Completed: false, Outcome: reliability.OutcomeCancelled, CancellationSource: "quit_now"},
		{ID: 4, RelayID: 1, OutingID: 4, AttemptNumber: 1, Completed: false, Outcome: reliability.OutcomeFailed},
	}

	pass, fail, cancelled := tallyOutings(tries, 1)
	if pass != 0 || fail != 1 || cancelled != 3 {
		t.Fatalf("all cancellation sources: want 0 pass / 1 fail / 3 cancelled, got %d pass / %d fail / %d cancelled", pass, fail, cancelled)
	}
}

func TestTallyOutingsCompletionWinsOverEarlierCancellation(t *testing.T) {
	tries := []store.TryRecord{
		{ID: 1, RelayID: 1, OutingID: 1, AttemptNumber: 1, Completed: false, Outcome: reliability.OutcomeCancelled, CancellationSource: "skip"},
		{ID: 2, RelayID: 1, OutingID: 1, AttemptNumber: 2, Completed: true, Outcome: reliability.OutcomeCompleted},
	}

	pass, fail, cancelled := tallyOutings(tries, 1)
	if pass != 1 || fail != 0 || cancelled != 0 {
		t.Fatalf("completion-wins tally: want 1 pass / 0 fail / 0 cancelled, got %d pass / %d fail / %d cancelled", pass, fail, cancelled)
	}
}

func TestTallyOutingsEmpty(t *testing.T) {
	pass, fail, cancelled := tallyOutings(nil, 1)
	if pass != 0 || fail != 0 || cancelled != 0 {
		t.Fatalf("empty: want 0/0/0, got %d/%d/%d", pass, fail, cancelled)
	}
}
