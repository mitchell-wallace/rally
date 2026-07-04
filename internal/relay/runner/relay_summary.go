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
	passCount, failCount, cancelledCount := tallyOutings(r.store.AllTries(), relay.ID)
	totalOutings := passCount + failCount + cancelledCount
	totalDuration := time.Since(r.relayStart)
	if totalOutings > 0 {
		r.eventSink().Emit(context.Background(), runtimeevent.RelaySummaryReady{
			TotalOutings:  totalOutings,
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
		TotalOutings:  totalOutings,
		Passed:        passCount,
		Failed:        failCount,
		Cancelled:     cancelledCount,
		TotalDuration: totalDuration,
	})
}

// tallyOutings aggregates try records into outing-level pass/fail/cancelled counts
// for the given relay. Each outing (identified by OutingID) is counted exactly once:
// it passes if any attempt ultimately completed, is cancelled if no attempt
// completed and an operator-cancelled attempt resolved the outing, and fails only
// when every attempt exhausted without completion or cancellation.
func tallyOutings(tries []store.TryRecord, relayID int) (passCount, failCount, cancelledCount int) {
	type outingState struct {
		completed bool
		cancelled bool
	}
	byOuting := make(map[int]outingState)
	order := make([]int, 0)
	for _, tr := range tries {
		if tr.RelayID != relayID {
			continue
		}
		state, seen := byOuting[tr.OutingID]
		if !seen {
			order = append(order, tr.OutingID)
		}
		if tr.Completed || tr.Outcome.IsSuccess() {
			state.completed = true
		}
		if tr.Outcome == reliability.OutcomeCancelled {
			state.cancelled = true
		}
		byOuting[tr.OutingID] = state
	}
	for _, outingID := range order {
		state := byOuting[outingID]
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
