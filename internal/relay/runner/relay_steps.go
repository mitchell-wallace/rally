package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/style"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

func (r *Runner) startOrResumeRelay() (*store.RelayRecord, *routeRuntime, io.WriteCloser, error) {
	// Clear any stale run-state from a previous interrupted relay.
	_, _ = r.maybeWriteStubAndClearState("")

	relay, resumed, err := relaycore.ResumeRelay(r.store)
	if err != nil {
		return nil, nil, nil, err
	}

	routeRuntime := (*routeRuntime)(nil)
	selectionLabel := ""
	if resumed {
		if r.cfg.OverwriteMixOnResume {
			routeRuntime, selectionLabel, err = newRouteRuntimeFromConfig(r.cfg)
			if err != nil {
				return nil, nil, nil, err
			}
			relay.AgentMix = selectionLabel
			if err := r.store.UpdateRelay(*relay); err != nil {
				return nil, nil, nil, err
			}
		} else {
			routeRuntime, selectionLabel, err = newRouteRuntimeFromStoredLabel(r.cfg, relay.AgentMix)
			if err != nil {
				return nil, nil, nil, err
			}
		}
	} else {
		routeRuntime, selectionLabel, err = newRouteRuntimeFromConfig(r.cfg)
		if err != nil {
			return nil, nil, nil, err
		}
		relay, err = relaycore.CreateRelay(r.store, r.cfg.TargetIterations, selectionLabel)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	routeRuntime.store = r.store

	for _, w := range routeRuntime.Warnings() {
		fmt.Fprintln(os.Stderr, w)
	}

	log, err := openRelayLog(r.cfg.DataDir, r.cfg.WorkspaceDir, relay.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	r.log = log

	fmt.Fprintf(log, "relay %d started (target %d iterations, mix: %s)\n", relay.ID, relay.TargetIterations, relay.AgentMix)
	r.relayStart = time.Now()

	return relay, routeRuntime, log, nil
}

func (r *Runner) startRelaySpan(ctx context.Context, relay *store.RelayRecord) (context.Context, telemetry.Span, telemetry.RallyContext) {
	// Model the relay as a trace transaction; runs and tries are child spans.
	rc := r.rallyContext(relay)
	ctx, relaySpan := r.tel().StartSpan(ctx, "relay", fmt.Sprintf("relay-%d", relay.ID))
	relayTags := telemetry.Tags(telemetry.EventInfo{RelayID: relay.ID, Repo: rc.Repo, RepoName: rc.RepoName})
	applyRallyContext(relaySpan, relayTags, rc)
	return ctx, relaySpan, rc
}

func (r *Runner) consumeRelayScopedMessage(relay *store.RelayRecord) (*store.MessageRecord, error) {
	// Consume oldest eligible relay-scoped message at relay start
	var relayMsg *store.MessageRecord
	relayPending := r.store.EligibleRelayScopedMessages(relay.ID)
	if len(relayPending) > 0 {
		msg := relayPending[0]
		// Record consumption at consume time (Task 6)
		if msg.ConsumedByRelayID == nil {
			msg.ConsumedByRelayID = &relay.ID
			if err := r.store.UpdateMessage(msg); err != nil {
				return nil, err
			}
			// Append to ConsumedMessageIDs immediately
			relay.ConsumedMessageIDs = append(relay.ConsumedMessageIDs, msg.ID)
			if err := r.store.UpdateRelay(*relay); err != nil {
				return nil, err
			}
		}
		relayMsg = &msg
	}
	return relayMsg, nil
}

func (r *Runner) selectRouteOrWait(
	ctx context.Context,
	relay *store.RelayRecord,
	runIndex int,
	routeRuntime *routeRuntime,
	resilience *relaycore.Resilience,
	rc telemetry.RallyContext,
	log io.Writer,
) (runTask, routeSelection, bool, bool, error) {
	task, err := r.resolveRunTask(ctx)
	if err != nil {
		if errors.Is(err, errQueueEmpty) {
			fmt.Fprintf(log, "relay %d completed: laps queue empty\n", relay.ID)
			_ = relaycore.CompleteRelay(r.store, relay.ID)
			return runTask{}, routeSelection{}, false, true, nil
		}
		return runTask{}, routeSelection{}, false, false, err
	}

	selection, err := routeRuntime.next(task, resilience)
	if err != nil {
		var routeErr *routeSelectionError
		if errors.As(err, &routeErr) {
			if routeErr.AllFrozen {
				fmt.Fprintf(log, "relay %d failed: all agents frozen\n", relay.ID)
				// A relay ending with every agent type frozen is a lockout
				// that warrants operator attention — capture it as an Issue.
				// This is a relay-level state, not a single try: it carries only
				// agent_state=frozen and the relay/global context, with no
				// try_id, attempt, or reset evidence (those zero fields are
				// omitted by FailureStateTags).
				r.tel().CaptureFailure(ctx, fmt.Sprintf("relay %d stalled: all agents frozen", relay.ID),
					failureStateEvent(
						telemetry.Tags(telemetry.EventInfo{RelayID: relay.ID, Repo: rc.Repo, RepoName: rc.RepoName}),
						rc,
						telemetry.FailureState{AgentState: string(relaycore.StateFrozen)},
					))
				_ = relaycore.CompleteRelay(r.store, relay.ID)
				return runTask{}, routeSelection{}, false, false, fmt.Errorf("relay failed: all agents frozen")
			}
			if routeErr.Wait <= 0 {
				fmt.Fprintf(log, "relay %d failed: %s\n", relay.ID, routeErr.Error())
				_ = relaycore.CompleteRelay(r.store, relay.ID)
				return runTask{}, routeSelection{}, false, false, fmt.Errorf("relay failed: %s", routeErr.Error())
			}
			fmt.Fprintf(log, "relay %d all agents paused, waiting %v\n", relay.ID, routeErr.Wait)
			outcome, waitErr := waitWithCountdown(ctx, routeErr.Wait, "agents paused, waiting %s...")
			if waitErr != nil {
				return runTask{}, routeSelection{}, false, false, waitErr
			}
			switch outcome {
			case waitSkipped:
				unpaused, err := routeRuntime.forceUnpauseAll(resilience, relay.ID, routeErr.RouteName, routeErr.EffectiveAssignee)
				if err != nil {
					return runTask{}, routeSelection{}, false, false, err
				}
				fmt.Fprintf(log, "relay %d skip pressed during wait; force-unpaused %d agent(s)\n", relay.ID, unpaused)
			case waitStopped:
				fmt.Fprintf(log, "relay %d stop requested during wait\n", relay.ID)
				r.stopFlag.Store(true)
			}
			return runTask{}, routeSelection{}, false, false, nil
		}
		return runTask{}, routeSelection{}, false, false, err
	}
	if selection.Route.Warning != "" {
		fmt.Fprintln(os.Stderr, selection.Route.Warning)
		fmt.Fprintln(log, selection.Route.Warning)
	}
	task.ResolvedRoute = selection.Route.Name
	task.EffectiveAssignee = selection.EffectiveAssignee
	r.prepareExecutorForSelection(relay.ID, runIndex, selection, log)
	return task, selection, true, false, nil
}

func (r *Runner) startRunSpan(ctx context.Context, relay *store.RelayRecord, runID int, task runTask, selection routeSelection, rc telemetry.RallyContext) (context.Context, telemetry.Span) {
	runTags := telemetry.Tags(telemetry.EventInfo{
		RelayID:  relay.ID,
		RunID:    runID,
		Role:     task.promptAssignee(),
		Harness:  selection.Agent.Harness,
		Model:    selection.Agent.Model,
		Repo:     rc.Repo,
		RepoName: rc.RepoName,
		LapID:    task.LapID,
	})
	runCtx, runSpan := r.tel().StartSpan(ctx, "run", fmt.Sprintf("relay-%d-run-%d", relay.ID, runID))
	applyTags(runSpan, runTags)
	return runCtx, runSpan
}

func (r *Runner) emitFallbackEvents(
	ctx context.Context,
	runCtx context.Context,
	relay *store.RelayRecord,
	runID int,
	task runTask,
	selection routeSelection,
	fallbackCause *routeFallbackCause,
	rc telemetry.RallyContext,
	runSpan telemetry.Span,
	log io.Writer,
) *routeFallbackCause {
	// Rotating to a backup runner is a healthy recovery, not an alert. Record
	// it on the routing event stream, not as a try outcome.
	if selection.PreviousAgent != nil &&
		(selection.PreviousAgent.Harness != selection.Agent.Harness ||
			selection.PreviousAgent.Model != selection.Agent.Model) {
		from := telemetry.RunnerLabel(selection.PreviousAgent.Harness, selection.PreviousAgent.Model)
		to := telemetry.RunnerLabel(selection.Agent.Harness, selection.Agent.Model)
		fmt.Fprintf(log, "relay %d run %d route fallback: rotated %s -> %s\n", relay.ID, runID, from, to)
		runSpan.SetTag("route_fallback", "true")
		runSpan.SetData("route_fallback", true)
		runSpan.SetTag("from_runner", from)
		runSpan.SetTag("to_runner", to)
		fields := map[string]interface{}{
			"event":       "route_fallback",
			"relay_id":    relay.ID,
			"run_id":      runID,
			"from_runner": from,
			"to_runner":   to,
			"role":        task.promptAssignee(),
			"repo":        rc.Repo,
			"repo_name":   rc.RepoName,
			"lap_id":      task.LapID,
		}
		if fallbackCause != nil && fallbackCause.fromRunner == from {
			fallbackCause.addTo(fields, runSpan)
		}
		fallbackCause = nil
		r.tel().EmitRouteEvent(runCtx, fields)
	} else if fallbackCause != nil {
		fallbackCause = nil
	}
	if selection.RecoveryCapHit {
		to := telemetry.RunnerLabel(selection.Agent.Harness, selection.Agent.Model)
		from := to
		if selection.PreviousAgent != nil {
			from = telemetry.RunnerLabel(selection.PreviousAgent.Harness, selection.PreviousAgent.Model)
		}
		r.tel().EmitRouteEvent(runCtx, map[string]interface{}{
			"event":                        "route_fallback",
			"relay_id":                     relay.ID,
			"run_id":                       runID,
			"from_runner":                  from,
			"to_runner":                    to,
			"role":                         task.promptAssignee(),
			"repo":                         rc.Repo,
			"repo_name":                    rc.RepoName,
			"lap_id":                       task.LapID,
			"route_name":                   selection.Route.Name,
			"consecutive_recovery_runs":    selection.RecoveryStatus.ConsecutiveRecoveryRuns,
			"recovery_classification":      "needs_user",
			"route_entry_exhausted_reason": "recovery_cap_hit",
		})
		r.tel().CaptureFailure(ctx, fmt.Sprintf("relay %d lap %s recovery cap reached: needs_user", relay.ID, task.LapID),
			failureStateEvent(
				telemetry.Tags(telemetry.EventInfo{RelayID: relay.ID, RunID: runID, Role: task.promptAssignee(), Repo: rc.Repo, RepoName: rc.RepoName, LapID: task.LapID}),
				rc,
				telemetry.FailureState{RecoveryClassification: "needs_user"},
			))
	}
	return fallbackCause
}

func (r *Runner) consumeRunScopedMessage(runID int) (*store.MessageRecord, error) {
	// Consume run-scoped message at start of each run
	// First check if there's an already-consumed message from a failed run
	var consumedMsg *store.MessageRecord
	if existingMsg := r.store.ConsumedRunScopedMessageForRun(runID); existingMsg != nil {
		// Reuse the message from the failed run
		consumedMsg = existingMsg
	} else {
		// Consume a new message
		pending := r.store.PendingMessages()
		for _, p := range pending {
			if p.Scope != "relay" && p.ConsumedByRunID == nil {
				msg := p
				msg.ConsumedByRunID = &runID
				if err := r.store.UpdateMessage(msg); err != nil {
					return nil, err
				}
				consumedMsg = &msg
				break
			}
		}
	}
	return consumedMsg, nil
}

func (r *Runner) resolveFallbackCause(runID int, selection routeSelection, res runOutcome) *routeFallbackCause {
	if !res.Success {
		reason := "retry-budget-exhausted"
		switch {
		case res.Category != "":
			reason = "category:" + string(res.Category)
		case r.skipFlag.Load():
			reason = "skip"
		case res.Outcome != "":
			reason = "outcome:" + string(res.Outcome)
		}
		return &routeFallbackCause{
			fromRunner:           telemetry.RunnerLabel(selection.Agent.Harness, selection.Agent.Model),
			triggerRunID:         runID,
			triggerTryID:         r.store.NextTryID() - 1,
			triggerOutcome:       string(res.Outcome),
			triggerFailReason:    res.FailReason,
			triggerFailureClass:  string(res.FailureClass),
			triggerFailureCat:    string(res.Category),
			triggerLapID:         res.LapID,
			routeName:            selection.Route.Name,
			entryExhaustedReason: reason,
		}
	} else {
		return nil
	}
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

	// Surface the resolved failure category and any parsed reset deadline
	// from runOne, then act on it: a usage_limit benches the whole quota
	// scope until the reset (below); other categories (auth_or_proxy,
	// invalid_model) are left to the scheduler's normal exhaustion/route-away
	// path. The log line records the resolution for operator triage.
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

		// On a usage_limit, bench the entire exhausted quota scope across
		// every lane until the reset deadline so siblings sharing the same
		// account front leave rotation together, then wait it out rather
		// than thrashing the limit. Other terminal categories (auth_or_proxy,
		// invalid_model) are routed away by the scheduler's normal exhaustion
		// path and are not benched here.
		if res.Category == reliability.CategoryUsageLimit {
			resetAt := benchResetDeadline(res.ResetEvidence, time.Now())
			scope := routeRuntime.quotaScope(selection.Agent.Harness, selection.Agent.Model)
			benched, benchErr := routeRuntime.benchQuotaScope(resilience, scope, resetAt, relay.ID, selection.Route.Name, selection.EffectiveAssignee)
			if benchErr != nil {
				return benchErr
			}
			fmt.Fprintf(log, "relay %d run %d benched quota scope %q until %s (%d key(s))\n",
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

func (r *Runner) completeRelayIfTargetMet(relay *store.RelayRecord, log io.Writer) error {
	if relay.CompletedIterations >= relay.TargetIterations {
		if err := relaycore.CompleteRelay(r.store, relay.ID); err != nil {
			return err
		}
		fmt.Fprintf(log, "relay %d completed\n", relay.ID)
	}
	return nil
}

func (r *Runner) printRelaySummary(relay *store.RelayRecord) {
	// Print relay summary
	passCount, failCount, cancelledCount := tallyRuns(r.store.AllTries(), relay.ID)
	totalRuns := passCount + failCount + cancelledCount
	if totalRuns > 0 {
		totalDuration := time.Since(r.relayStart)
		summary := style.RenderSummary(totalRuns, passCount, failCount, totalDuration, cancelledCount)
		fmt.Println(summary)
	}
}

// tallyRuns aggregates try records into run-level pass/fail/cancelled counts
// for the given relay. Each run (identified by RunID) is counted exactly once:
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
		state, seen := byRun[tr.RunID]
		if !seen {
			order = append(order, tr.RunID)
		}
		if tr.Completed || tr.Outcome.IsSuccess() {
			state.completed = true
		}
		if tr.Outcome == reliability.OutcomeCancelled {
			state.cancelled = true
		}
		byRun[tr.RunID] = state
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
