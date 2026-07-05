package runner

import (
	"fmt"
	"io"
	"time"

	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

const (
	// Auth does not self-heal; this bench decay provides a re-probe cadence so
	// an operator logging in mid-relay is picked up within the hour.
	authBenchRetryInterval = time.Hour
	authBenchReason        = "not authenticated - operator login required"
)

func (r *Runner) updateRunProgress(
	relay *store.RelayRecord,
	relayMsg *store.MessageRecord,
	consumedMsg *store.MessageRecord,
	res runOutcome,
	runIndex int,
) (int, error) {
	if res.Success {
		relay.CompletedIterations++
		runIndex++
		if consumedMsg != nil && res.Addressed {
			consumedMsg.Status = "addressed"
			now := time.Now().UTC().Format(time.RFC3339)
			consumedMsg.UpdatedAt = now
			if err := r.store.UpdateMessage(*consumedMsg); err != nil {
				return runIndex, err
			}
			// Add to ConsumedMessageIDs if not already present
			if !containsInt(relay.ConsumedMessageIDs, consumedMsg.ID) {
				relay.ConsumedMessageIDs = append(relay.ConsumedMessageIDs, consumedMsg.ID)
			}
		}
		if relayMsg != nil && res.Addressed && relayMsg.Status == "pending" {
			relayMsg.Status = "addressed"
			now := time.Now().UTC().Format(time.RFC3339)
			relayMsg.UpdatedAt = now
			if err := r.store.UpdateMessage(*relayMsg); err != nil {
				return runIndex, err
			}
			// Already added at consume time, but ensure no duplicates
			if !containsInt(relay.ConsumedMessageIDs, relayMsg.ID) {
				relay.ConsumedMessageIDs = append(relay.ConsumedMessageIDs, relayMsg.ID)
			}
		}
	} else {
		runIndex++
	}

	relay.LastTryID = r.store.NextTryID() - 1
	if relay.FirstTryID == 0 {
		relay.FirstTryID = relay.LastTryID
	}
	if err := r.store.UpdateRelay(*relay); err != nil {
		return runIndex, err
	}
	return runIndex, nil
}

func (r *Runner) updateSkippedRunProgress(relay *store.RelayRecord, selection routeSelection, runIndex int) (int, error) {
	// If skipped, don't pause the agent — just advance rotation
	r.skipFlag.Store(false)
	selection.Entry.Exhausted = true
	selection.Entry.Benched = false
	runIndex++
	relay.LastTryID = r.store.NextTryID() - 1
	if relay.FirstTryID == 0 {
		relay.FirstTryID = relay.LastTryID
	}
	if err := r.store.UpdateRelay(*relay); err != nil {
		return runIndex, err
	}
	return runIndex, nil
}

func (r *Runner) applyRunOutcomeToResilience(
	relay *store.RelayRecord,
	runIndex int,
	selection routeSelection,
	res runOutcome,
	routeRuntime *routeRuntime,
	resilience *relaycore.Resilience,
	log io.Writer,
) error {
	if !res.Success {
		selection.Scheduler.OnAgentFailed(selection.Entry, "retry-budget-exhausted", false)
	}

	// Surface the resolved failure category and any parsed reset deadline from
	// runOne, then act on it: usage_limit benches the whole quota scope until
	// reset, and auth_or_proxy benches it for a short re-probe interval. Other
	// categories (invalid_model, etc.) follow scheduler exhaustion/route-away.
	// The log line records the resolution for operator triage.
	if !res.Success && res.Category != "" {
		resetNote := "none"
		if res.ResetEvidence != nil {
			if res.ResetEvidence.ResetAt != nil {
				resetNote = "reset_at=" + res.ResetEvidence.ResetAt.UTC().Format(time.RFC3339)
			} else if res.ResetEvidence.ResetAfter > 0 {
				resetNote = "reset_after=" + res.ResetEvidence.ResetAfter.String()
			}
		}
		fmt.Fprintf(log, "relay %d run %d resolved failure category=%s reset=%s\n",
			relay.ID, runIndex+1, res.Category, resetNote)

		// Bench provider/account-scoped failures across every lane so siblings
		// sharing the same quota or auth front leave rotation together.
		if res.Category == reliability.CategoryUsageLimit {
			resetAt := benchResetDeadline(res.ResetEvidence, time.Now())
			scope := routeRuntime.quotaScope(selection.Agent.Harness, selection.Agent.Model)
			benched, benchErr := routeRuntime.benchQuotaScope(resilience, scope, resetAt, relay.ID, selection.Route.Name, selection.EffectiveAssignee)
			if benchErr != nil {
				return benchErr
			}
			fmt.Fprintf(log, "relay %d run %d benched quota scope %q until %s (%d key(s))\n",
				relay.ID, runIndex+1, scope, resetAt.UTC().Format(time.RFC3339), benched)
		} else if res.Category == reliability.CategoryAuthOrProxy {
			resetAt := time.Now().Add(authBenchRetryInterval)
			scope := routeRuntime.authScope(selection.Agent.Harness, selection.Agent.Model)
			benched, benchErr := routeRuntime.benchAuthScope(resilience, scope, resetAt, relay.ID, selection.Route.Name, selection.EffectiveAssignee, authBenchReason)
			if benchErr != nil {
				return benchErr
			}
			fmt.Fprintf(log, "relay %d run %d benched quota scope %q until %s (%d key(s)): harness not authenticated\n",
				relay.ID, runIndex+1, scope, resetAt.UTC().Format(time.RFC3339), benched)
		}
	}

	if selection.Probation {
		if res.Success || res.FailureClass == reliability.FailureIncomplete {
			if err := resilience.UnpauseAgent(relaycore.KeyFromAgent(selection.Agent), relay.ID); err != nil {
				return err
			}
		} else {
			if err := resilience.FreezeAgent(relaycore.KeyFromAgent(selection.Agent), relay.ID, "probation run failed"); err != nil {
				return err
			}
		}
	} else if selection.HourlyRetry {
		if res.Success {
			if err := resilience.UnpauseAgent(relaycore.KeyFromAgent(selection.Agent), relay.ID); err != nil {
				return err
			}
		} else if res.FailureClass == reliability.FailureInfra && res.InfraFailures > 1 {
			if err := resilience.RecordHourlyFailure(relaycore.KeyFromAgent(selection.Agent), relay.ID); err != nil {
				return err
			}
		}
	} else {
		if !res.Success && res.FailureClass == reliability.FailureInfra && res.InfraFailures > 1 {
			if err := resilience.PauseAgent(relaycore.KeyFromAgent(selection.Agent), relay.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runner) completeRelayIfTargetMet(relay *store.RelayRecord, log io.Writer) error {
	if relay.CompletedIterations >= relay.TargetIterations {
		if err := relaycore.CompleteRelay(r.store, relay.ID); err != nil {
			return err
		}
		fmt.Fprintf(log, "relay %d completed\n", relay.ID)
	}
	return nil
}

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}
