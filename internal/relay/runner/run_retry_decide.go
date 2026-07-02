package runner

import (
	"context"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

type routeFallbackCause struct {
	fromRunner           string
	triggerRunID         int
	triggerTryID         int
	triggerOutcome       string
	triggerFailReason    string
	triggerFailureClass  string
	triggerFailureCat    string
	triggerLapID         string
	routeName            string
	entryExhaustedReason string
}

func (c *routeFallbackCause) addTo(fields map[string]interface{}, span telemetry.Span) {
	if c == nil {
		return
	}
	values := map[string]interface{}{
		"trigger_run_id":               c.triggerRunID,
		"trigger_try_id":               c.triggerTryID,
		"trigger_outcome":              c.triggerOutcome,
		"trigger_fail_reason":          c.triggerFailReason,
		"trigger_failure_class":        c.triggerFailureClass,
		"trigger_failure_category":     c.triggerFailureCat,
		"trigger_lap_id":               c.triggerLapID,
		"route_name":                   c.routeName,
		"route_entry_exhausted_reason": c.entryExhaustedReason,
	}
	for k, v := range values {
		if v == "" || v == 0 {
			continue
		}
		fields[k] = v
		if span != nil {
			switch x := v.(type) {
			case string:
				span.SetTag(k, x)
			default:
				span.SetData(k, x)
			}
		}
	}
}

func (r *Runner) decideRetryOrComplete(task runTask, state *runOneState, attempt *runAttemptState, onStallRecovered func()) runOneAttemptDecision {
	if attempt.actionTaken {
		if r.stopFlag.Load() {
			return runOneAttemptDecision{action: runOneAttemptReturn, outcome: state.outcome(task, false, false, true)}
		}
		if r.skipFlag.Load() {
			return runOneAttemptDecision{action: runOneAttemptReturn, outcome: state.outcome(task, false, false, false)}
		}
		pausePrompt := "Paused — press Enter to resume"
		r.eventSink().Emit(context.Background(), runtimeevent.PausePromptShown{Message: pausePrompt})
		if r.cfg.Controls != nil {
			_ = r.cfg.Controls.WaitResume(context.Background())
		}
		if attempt.result != nil {
			state.previousSummary = attempt.result.Summary
			state.lastResult = attempt.result
			if attempt.result.SessionID != "" {
				state.sessionID = attempt.result.SessionID
			}
		} else {
			state.previousSummary = ""
			state.lastResult = &harnessapi.TryResult{Completed: false}
		}
		return runOneAttemptDecision{action: runOneAttemptContinue}
	}

	if !attempt.failed {
		if state.stallMarked && onStallRecovered != nil {
			onStallRecovered()
			attempt.mon.SetStalled(false)
			attempt.mon.SetRecovered()
			state.stallMarked = false
		}
		state.success = true
		state.lastResult = attempt.result
		return runOneAttemptDecision{action: runOneAttemptBreak}
	}

	if r.skipFlag.Load() {
		return runOneAttemptDecision{action: runOneAttemptBreak}
	}
	if attempt.lapPinMismatch {
		return runOneAttemptDecision{action: runOneAttemptBreak}
	}
	// usage_limit / auth_or_proxy are bounded to a single attempt: break here
	// so the routing dispatch loop can bench the quota scope or route away
	// rather than burning the retry budget on a quota/auth failure that a
	// retry cannot clear.
	if attempt.terminalForRun {
		state.lastResult = attempt.result
		return runOneAttemptDecision{action: runOneAttemptBreak}
	}
	// Run-budget exhaustion is terminal for the normal implementation loop,
	// even though OutcomeRunTimeout is deliberately NOT terminal in
	// IsTerminalForRun (a per-try cap with budget remaining must still be able
	// to retry). Unlike that retryable per-try cap, an exhausted run budget
	// stops retries here and the run proceeds to the bounded handoff-only
	// continuation. That continuation is owned by the next lap (task 4); until
	// it lands, breaking here resolves the run on the persisted run_timeout
	// attempt rather than burning more of an already-exhausted budget.
	if attempt.runBudgetExhausted {
		state.lastResult = attempt.result
		// When the harness can resume the captured session, defer to the
		// bounded handoff-only continuation below: the run_timeout try just
		// recorded is observability only, and the continuation (a separate
		// HandoffOnly try) becomes the run resolver. Without a resumable
		// session the run_timeout try was already relabelled handoff_timeout
		// above and is itself the resolving try.
		if attempt.canHandoffResume {
			state.handoffResumePending = true
			state.handoffResumeSessionID = state.sessionID
			state.handoffResumeBaseAttempt = attempt.attempt
		}
		return runOneAttemptDecision{action: runOneAttemptBreak}
	}

	if attempt.result != nil {
		state.previousSummary = attempt.result.Summary
		state.lastResult = attempt.result
		if attempt.result.SessionID != "" {
			state.sessionID = attempt.result.SessionID
		}
	} else {
		state.previousSummary = ""
		state.lastResult = &harnessapi.TryResult{Completed: false}
	}
	return runOneAttemptDecision{action: runOneAttemptContinue}
}
