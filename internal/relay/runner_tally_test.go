package relay

import (
	"testing"

	"github.com/mitchell-wallace/rally/internal/store"
)

func TestTallyRunsRetryThenSuccess(t *testing.T) {
	// One run, two attempts: the first fails, the second completes. The run
	// should count as a single pass and zero failures.
	tries := []store.TryRecord{
		{ID: 1, RelayID: 1, RunID: 1, AttemptNumber: 1, Completed: false},
		{ID: 2, RelayID: 1, RunID: 1, AttemptNumber: 2, Completed: true},
	}

	pass, fail := tallyRuns(tries, 1)
	if pass != 1 || fail != 0 {
		t.Fatalf("retry-then-success: want 1 pass / 0 fail, got %d pass / %d fail", pass, fail)
	}
}

func TestTallyRunsAllExhausted(t *testing.T) {
	// One run, all attempts exhausted without completion: one failure.
	tries := []store.TryRecord{
		{ID: 1, RelayID: 1, RunID: 1, AttemptNumber: 1, Completed: false},
		{ID: 2, RelayID: 1, RunID: 1, AttemptNumber: 2, Completed: false},
		{ID: 3, RelayID: 1, RunID: 1, AttemptNumber: 3, Completed: false},
	}

	pass, fail := tallyRuns(tries, 1)
	if pass != 0 || fail != 1 {
		t.Fatalf("all-exhausted: want 0 pass / 1 fail, got %d pass / %d fail", pass, fail)
	}
}

func TestTallyRunsAggregatesMultipleRuns(t *testing.T) {
	// Two runs in the relay: run 1 succeeds after a retry, run 2 exhausts its
	// budget. Tries from a different relay must be ignored entirely.
	tries := []store.TryRecord{
		{ID: 1, RelayID: 1, RunID: 1, AttemptNumber: 1, Completed: false},
		{ID: 2, RelayID: 1, RunID: 1, AttemptNumber: 2, Completed: true},
		{ID: 3, RelayID: 1, RunID: 2, AttemptNumber: 1, Completed: false},
		{ID: 4, RelayID: 1, RunID: 2, AttemptNumber: 2, Completed: false},
		// Different relay — should not be counted.
		{ID: 5, RelayID: 2, RunID: 3, AttemptNumber: 1, Completed: false},
	}

	pass, fail := tallyRuns(tries, 1)
	if pass != 1 || fail != 1 {
		t.Fatalf("multi-run: want 1 pass / 1 fail, got %d pass / %d fail", pass, fail)
	}
}

func TestTallyRunsEmpty(t *testing.T) {
	pass, fail := tallyRuns(nil, 1)
	if pass != 0 || fail != 0 {
		t.Fatalf("empty: want 0/0, got %d/%d", pass, fail)
	}
}
