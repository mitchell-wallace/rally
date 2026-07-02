package runner

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/routing"
	"github.com/mitchell-wallace/rally/internal/store"
)

func (r *routeRuntime) next(task runTask, resilience *relaycore.Resilience) (routeSelection, error) {
	effectiveAssignee := task.Assignee
	recoveryStatus := store.RecoveryPendingStatus{}
	recoveryForced := false
	recoveryCapHit := false

	route, err := r.selector.ActiveRoute(routing.Lap{Assignee: task.Assignee}, r.overrideRoute())
	if err != nil {
		return routeSelection{}, err
	}

	if r.store != nil && task.LapID != "" {
		recoveryStatus = r.store.RecoveryPendingForLap(task.LapID)
		switch {
		case recoveryStatus.CapHit:
			recoveryCapHit = true
			route.Warning = joinRouteWarnings(route.Warning, fmt.Sprintf(
				"routing: recovery cap reached for lap %q after %d consecutive recovery run(s); falling back to normal route and raising needs_user",
				task.LapID, recoveryStatus.ConsecutiveRecoveryRuns,
			))
		case recoveryStatus.Pending:
			recoveryRoute, recoveryErr := r.selector.ActiveRoute(routing.Lap{Assignee: store.RecoveryRouteName}, r.overrideRoute())
			if recoveryErr != nil {
				route.Warning = joinRouteWarnings(route.Warning, fmt.Sprintf(
					"routing: recovery pending for lap %q but recovery route could not be resolved (%v); falling back to normal route",
					task.LapID, recoveryErr,
				))
				break
			}
			if r.override == nil && recoveryRoute.Source != routing.RouteSourceAssignee {
				route.Warning = joinRouteWarnings(route.Warning, fmt.Sprintf(
					"routing: recovery pending for lap %q but no recovery route is configured; falling back to normal route",
					task.LapID,
				))
				break
			}
			route = recoveryRoute
			effectiveAssignee = store.RecoveryRouteName
			recoveryForced = true
		}
	}

	scheduler := r.schedulers[strings.ToLower(route.Name)]
	if scheduler == nil {
		return routeSelection{}, fmt.Errorf("routing: no scheduler for route %q", route.Name)
	}

	r.syncRecoverySignals(scheduler, resilience, effectiveAssignee)

	scheduled, err := scheduler.Next()
	if err != nil {
		if strings.Contains(err.Error(), "all entries exhausted") {
			return routeSelection{}, r.selectionWaitError(scheduler, resilience, route.Name, effectiveAssignee)
		}
		return routeSelection{}, err
	}
	entry := scheduled.Current

	selectedEntry := entry.Entry
	if r.override != nil && strings.EqualFold(route.Name, r.override.Name) {
		selectedEntry, err = r.override.ResolveSelection(entry.Entry)
		if err != nil {
			return routeSelection{}, err
		}
	}

	picked, err := r.resolvedEntryAgent(selectedEntry, effectiveAssignee)
	if err != nil {
		return routeSelection{}, fmt.Errorf("routing: route %q entry %q: %w", route.Name, selectedEntry.Raw, err)
	}

	st, since := resilience.GetState(relaycore.KeyFromAgent(picked))
	hourlyRetry := st == relaycore.StatePaused && !resilience.NowFunc().Before(since.Add(resilience.PauseDuration))
	probation := st == relaycore.StateProbation

	var previousAgent *harnessapi.ResolvedAgent
	routeKey := strings.ToLower(route.Name)
	if scheduled.Prev != nil && scheduled.Prev.Position != entry.Position {
		if last, ok := r.lastAgent[routeKey]; ok {
			lastCopy := last
			previousAgent = &lastCopy
		}
	}
	r.lastAgent[routeKey] = picked

	return routeSelection{
		Agent:             picked,
		PreviousAgent:     previousAgent,
		Route:             route,
		Entry:             entry,
		Scheduler:         scheduler,
		HourlyRetry:       hourlyRetry,
		Probation:         probation,
		EffectiveAssignee: effectiveAssignee,
		RecoveryForced:    recoveryForced,
		RecoveryCapHit:    recoveryCapHit,
		RecoveryStatus:    recoveryStatus,
	}, nil
}

func joinRouteWarnings(existing, next string) string {
	existing = strings.TrimSpace(existing)
	next = strings.TrimSpace(next)
	switch {
	case existing == "":
		return next
	case next == "":
		return existing
	default:
		return existing + "\n" + next
	}
}

func (r *routeRuntime) overrideRoute() *routing.Route {
	if r.override == nil {
		return nil
	}
	route := routing.Route{
		Name:    r.override.Name,
		Entries: cloneParsedEntries(r.override.Entries),
	}
	return &route
}

func (r *routeRuntime) selectionWaitError(scheduler *routing.Scheduler, resilience *relaycore.Resilience, routeName string, effectiveAssignee string) error {
	var minWait time.Duration
	waitSet := false
	seenKeys := map[relaycore.ResilienceKey]struct{}{}
	totalKeys := 0
	frozenKeys := 0
	disabledKeys := 0

	for _, state := range scheduler.EntryStates() {
		key, err := r.resilienceKeyForEntry(state.Entry, effectiveAssignee)
		if err != nil {
			continue
		}
		if _, ok := seenKeys[key]; ok {
			continue
		}
		seenKeys[key] = struct{}{}
		totalKeys++

		// Disabled providers have no reset deadline — they are off until the
		// operator re-enables them — so they contribute no wait, only a distinct
		// terminal message when an entire lane is disabled.
		if r.providers.Disabled(key.Harness, key.Model) {
			disabledKeys++
			continue
		}

		status, since := resilience.GetState(key)
		var wait time.Duration
		switch status {
		case relaycore.StatePaused:
			wait = since.Add(resilience.PauseDuration).Sub(resilience.NowFunc())
		case relaycore.StateBenched:
			// Benched keys wait out their usage-limit reset deadline, not the
			// fixed PauseDuration. GetState reports StateBenched only while
			// now < reset_at, so a positive wait is expected here.
			resetAt, ok := r.benchResetAt(resilience, key)
			if !ok {
				continue
			}
			wait = resetAt.Sub(resilience.NowFunc())
		case relaycore.StateFrozen:
			frozenKeys++
			continue
		default:
			continue
		}

		if wait < 0 {
			wait = 0
		}
		if !waitSet || wait < minWait {
			minWait = wait
			waitSet = true
		}
	}

	if totalKeys > 0 && frozenKeys == totalKeys {
		return &routeSelectionError{
			AllFrozen:         true,
			RouteName:         routeName,
			EffectiveAssignee: effectiveAssignee,
			message:           "all agents frozen",
		}
	}

	if waitSet {
		return &routeSelectionError{
			Wait:              minWait,
			RouteName:         routeName,
			EffectiveAssignee: effectiveAssignee,
			message:           "all agents paused or benched",
		}
	}

	if totalKeys > 0 && disabledKeys == totalKeys {
		return &routeSelectionError{
			RouteName:         routeName,
			EffectiveAssignee: effectiveAssignee,
			message:           "all agents disabled (check [providers] disabled flags)",
		}
	}

	return &routeSelectionError{
		RouteName:         routeName,
		EffectiveAssignee: effectiveAssignee,
		message:           "all agents unavailable",
	}
}

func (r *Runner) prepareExecutorForSelection(relayID, runIndex int, selection routeSelection, log io.Writer) {
	if selection.PreviousAgent == nil {
		return
	}
	if selection.PreviousAgent.Harness != selection.Agent.Harness {
		return
	}

	exec := r.executors[selection.Agent.Harness]
	if exec == nil || !exec.RotateSupported() {
		return
	}

	// Each Execute starts a fresh CLI process, so doing nothing here naturally
	// preserves the existing teardown/respawn fallback path. Rotation is only an
	// optimization when the adapter opts in and the swap succeeds.
	if err := exec.RotateModel(selection.Agent.Model); err != nil {
		fmt.Fprintf(log, "relay %d run %d rotate fallback for %s: %v\n", relayID, runIndex+1, selection.Agent.Harness, err)
	}
}
