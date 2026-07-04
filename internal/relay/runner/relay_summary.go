package runner

import (
	"context"
	"time"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func (r *Runner) printRelaySummary(relay *store.RelayRecord) {
	// Print relay summary
	passCount, failCount, cancelledCount := tallyRuns(r.store.AllTries(), relay.ID)
	totalRuns := passCount + failCount + cancelledCount
	totalDuration := time.Since(r.relayStart)
	if totalRuns > 0 {
		r.eventSink().Emit(context.Background(), runtimeevent.RelaySummaryReady{
			TotalRuns:     totalRuns,
			Passed:        passCount,
			Failed:        failCount,
			Cancelled:     cancelledCount,
			TotalDuration: totalDuration,
		})
	}
	// Data-only lifecycle marker: no operator-facing print (a terminal sink
	// no-ops it); it pairs with RelayStarted for alternate presentations.
	r.eventSink().Emit(context.Background(), runtimeevent.RelayCompleted{
		RelayID:       relay.ID,
		TotalRuns:     totalRuns,
		Passed:        passCount,
		Failed:        failCount,
		Cancelled:     cancelledCount,
		TotalDuration: totalDuration,
	})
}

// tallyRuns aggregates try records into run-level pass/fail/cancelled counts
// for the given relay. Each run (identified by OutingID) is counted exactly once:
// it passes if any attempt ultimately completed, is cancelled if no attempt
// completed and an operator-cancelled attempt resolved the run, and fails only
// when every attempt exhausted without completion or cancellation.
func tallyRuns(tries []store.TryRecord, relayID int) (passCount, failCount, cancelledCount int) {
	type runState struct {
		completed bool
		cancelled bool
	}
	byRun := make(map[int]runState)
	order := make([]int, 0)
	for _, tr := range tries {
		if tr.RelayID != relayID {
			continue
		}
		state, seen := byRun[tr.OutingID]
		if !seen {
			order = append(order, tr.OutingID)
		}
		if tr.Completed || tr.Outcome.IsSuccess() {
			state.completed = true
		}
		if tr.Outcome == reliability.OutcomeCancelled {
			state.cancelled = true
		}
		byRun[tr.OutingID] = state
	}
	for _, runID := range order {
		state := byRun[runID]
		switch {
		case state.completed:
			passCount++
		case state.cancelled:
			cancelledCount++
		default:
			failCount++
		}
	}
	return passCount, failCount, cancelledCount
}
