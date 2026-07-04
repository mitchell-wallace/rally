package runner

import (
	"context"
	"errors"
	"fmt"
	"io"

	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

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
			outcome, waitErr := waitWithCountdown(ctx, r.eventSink(), r.cfg.Controls, routeErr.Wait, "agents paused, waiting %s...")
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
		fmt.Fprintln(log, selection.Route.Warning)
		r.eventSink().Emit(ctx, runtimeevent.RouteWarning{Message: selection.Route.Warning})
	}
	task.ResolvedRoute = selection.Route.Name
	task.EffectiveAssignee = selection.EffectiveAssignee
	r.prepareExecutorForSelection(relay.ID, runIndex, selection, log)
	return task, selection, true, false, nil
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
			triggerOutingID:      runID,
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
