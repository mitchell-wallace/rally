package relay

import (
	"fmt"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/routing"
)

const (
	relaySelectionModeRoutes         = "__routes__"
	relaySelectionModeOverridePrefix = "__override__:"
)

// FormatMixLabel renders a stored agent-mix label as user-friendly text.
// Stored labels may include internal markers (__routes__, __override__:…)
// that should never appear in CLI prompts.
func FormatMixLabel(stored string) string {
	stored = strings.TrimSpace(stored)
	switch {
	case stored == "":
		return "(empty)"
	case stored == relaySelectionModeRoutes:
		return "configured routes"
	case strings.HasPrefix(stored, relaySelectionModeOverridePrefix):
		specs := strings.TrimSpace(strings.TrimPrefix(stored, relaySelectionModeOverridePrefix))
		if specs == "" {
			return "(override)"
		}
		return specs
	default:
		return stored
	}
}

type routeRuntime struct {
	selector   *routing.Selector
	override   *routing.OverrideRoute
	schedulers map[string]*routing.Scheduler
	resolver   Resolver
	lastAgent  map[string]agent.ResolvedAgent
}

type routeSelection struct {
	Agent         agent.ResolvedAgent
	PreviousAgent *agent.ResolvedAgent
	Route         routing.Route
	Entry         *routing.EntryState
	Scheduler     *routing.Scheduler
	HourlyRetry   bool
	Probation     bool
}

type routeSelectionError struct {
	Wait      time.Duration
	AllFrozen bool
	message   string
}

func (e *routeSelectionError) Error() string {
	return e.message
}

func newRouteRuntimeFromConfig(cfg Config) (*routeRuntime, string, error) {
	switch {
	case cfg.UseOverrideRoute:
		return newOverrideRouteRuntime(cfg.AgentMixSpecs, cfg.RouteSpecs, cfg.Resolver, !cfg.LapsEnabled)
	case len(cfg.RouteSpecs) > 0:
		rt, err := newResolvedRouteRuntime(cfg.RouteSpecs, cfg.Resolver, !cfg.LapsEnabled, nil)
		return rt, relaySelectionModeRoutes, err
	default:
		return newLegacyMixRouteRuntime(cfg.AgentMixSpecs, cfg.Resolver, !cfg.LapsEnabled)
	}
}

func newRouteRuntimeFromStoredLabel(cfg Config, stored string) (*routeRuntime, string, error) {
	switch {
	case stored == relaySelectionModeRoutes:
		if len(cfg.RouteSpecs) == 0 {
			return nil, "", fmt.Errorf("resume relay: stored route-based relay requires configured routes")
		}
		rt, err := newResolvedRouteRuntime(cfg.RouteSpecs, cfg.Resolver, !cfg.LapsEnabled, nil)
		return rt, relaySelectionModeRoutes, err
	case strings.HasPrefix(stored, relaySelectionModeOverridePrefix):
		specs := strings.Fields(strings.TrimSpace(strings.TrimPrefix(stored, relaySelectionModeOverridePrefix)))
		return newOverrideRouteRuntime(specs, cfg.RouteSpecs, cfg.Resolver, !cfg.LapsEnabled)
	default:
		return newLegacyMixRouteRuntime(strings.Fields(stored), cfg.Resolver, !cfg.LapsEnabled)
	}
}

func newLegacyMixRouteRuntime(specs []string, resolver Resolver, noBackend bool) (*routeRuntime, string, error) {
	routeEntries, label, err := legacyMixRouteEntries(specs, resolver)
	if err != nil {
		return nil, "", err
	}

	rt, err := newResolvedRouteRuntime(map[string][]string{
		"default": routeEntries,
	}, nil, noBackend, nil)
	if err != nil {
		return nil, "", err
	}

	return rt, label, nil
}

func newOverrideRouteRuntime(specs []string, routeSpecs map[string][]string, resolver Resolver, noBackend bool) (*routeRuntime, string, error) {
	override, err := routing.BuildOverrideRoute("override", specs, routeSpecs, routing.AgentResolver(resolver))
	if err != nil {
		return nil, "", err
	}

	rt, err := newResolvedRouteRuntime(routeSpecs, resolver, noBackend, override)
	if err != nil {
		return nil, "", err
	}

	return rt, relaySelectionModeOverridePrefix + strings.Join(specs, " "), nil
}

func newResolvedRouteRuntime(routeSpecs map[string][]string, resolver Resolver, noBackend bool, override *routing.OverrideRoute) (*routeRuntime, error) {
	selector, err := routing.NewSelector(routeSpecs, noBackend)
	if err != nil {
		return nil, err
	}

	schedulers := make(map[string]*routing.Scheduler, len(routeSpecs)+1)
	for name, rawEntries := range routeSpecs {
		route, err := routing.ParseRoute(name, rawEntries)
		if err != nil {
			return nil, err
		}

		resolvedEntries, err := resolveRouteEntries(route.Entries, resolver)
		if err != nil {
			return nil, fmt.Errorf("routing: route %q: %w", name, err)
		}
		schedulers[strings.ToLower(name)] = routing.NewScheduler(resolvedEntries)
	}

	if override != nil {
		schedulers[strings.ToLower(override.Name)] = routing.NewScheduler(cloneParsedEntries(override.Entries))
	}

	return &routeRuntime{
		selector:   selector,
		override:   override,
		schedulers: schedulers,
		resolver:   resolver,
		lastAgent:  make(map[string]agent.ResolvedAgent, len(schedulers)),
	}, nil
}

func (r *routeRuntime) next(task runTask, resilience *Resilience) (routeSelection, error) {
	route, err := r.selector.ActiveRoute(routing.Lap{Assignee: task.Assignee}, r.overrideRoute())
	if err != nil {
		return routeSelection{}, err
	}

	scheduler := r.schedulers[strings.ToLower(route.Name)]
	if scheduler == nil {
		return routeSelection{}, fmt.Errorf("routing: no scheduler for route %q", route.Name)
	}

	r.syncRecoverySignals(scheduler, resilience)

	scheduled, err := scheduler.Next()
	if err != nil {
		if strings.Contains(err.Error(), "all entries exhausted") {
			return routeSelection{}, r.selectionWaitError(scheduler, resilience)
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

	picked, err := resolveAgentSpec(selectedEntry.Spec, nil)
	if err != nil {
		return routeSelection{}, err
	}

	st, since := resilience.GetState(KeyFromAgent(picked))
	hourlyRetry := st == StatePaused && !resilience.NowFunc().Before(since.Add(resilience.PauseDuration))
	probation := st == StateProbation

	var previousAgent *agent.ResolvedAgent
	routeKey := strings.ToLower(route.Name)
	if scheduled.Prev != nil && scheduled.Prev.Position != entry.Position {
		if last, ok := r.lastAgent[routeKey]; ok {
			lastCopy := last
			previousAgent = &lastCopy
		}
	}
	r.lastAgent[routeKey] = picked

	return routeSelection{
		Agent:         picked,
		PreviousAgent: previousAgent,
		Route:         route,
		Entry:         entry,
		Scheduler:     scheduler,
		HourlyRetry:   hourlyRetry,
		Probation:     probation,
	}, nil
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

func (r *routeRuntime) syncRecoverySignals(scheduler *routing.Scheduler, resilience *Resilience) {
	for _, state := range scheduler.EntryStates() {
		resolved, err := resolveAgentSpec(state.Entry.Spec, nil)
		if err != nil {
			continue
		}

		key := KeyFromAgent(resolved)
		status, since := resilience.GetState(key)
		switch status {
		case StateActive:
			if state.Benched {
				scheduler.OnAgentRecovered(state)
			}
		case StatePaused:
			if !resilience.NowFunc().Before(since.Add(resilience.PauseDuration)) && (state.Benched || state.Exhausted) {
				scheduler.ResetEntry(state)
			} else if !(state.Benched && state.Exhausted) {
				scheduler.OnAgentFailed(state, "paused", true)
			}
		case StateProbation:
			// Probation is the freeze-decay window: a frozen agent has aged
			// past FreezeDuration and is granted exactly one tentative
			// recovery attempt per probation cycle. The one-shot is split
			// across two sync calls. On the first sync (no probation event
			// yet) we persist the event and unbench the entry so Next() can
			// pick it for the probation run. On any subsequent sync where
			// the state is still probation (e.g. the prior run didn't
			// resolve cleanly via UnpauseAgent/FreezeAgent), the entry is
			// re-benched so it cannot be selected again until the state
			// transitions. runOne is responsible for writing the active or
			// frozen event that ends the probation cycle.
			if !r.hasProbationEventForCurrentFreeze(resilience, key) {
				_ = resilience.persistProbationEvent(key)
				scheduler.ResetEntry(state)
			} else if !(state.Benched && state.Exhausted) {
				scheduler.OnAgentFailed(state, "probation", true)
			}
		case StateFrozen:
			if !(state.Benched && state.Exhausted) {
				scheduler.OnAgentFailed(state, "frozen", true)
			}
		}
	}
}

// hasProbationEventForCurrentFreeze returns true when the agent's event log
// already contains a probation event newer than the latest frozen event for
// this key. Used by syncRecoverySignals so the probation event is persisted
// exactly once per freeze cycle.
func (r *routeRuntime) hasProbationEventForCurrentFreeze(resilience *Resilience, key ResilienceKey) bool {
	events := resilience.Store.GetAgentStatus(key.Harness, key.Model)
	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].EventType {
		case "probation":
			return true
		case "frozen":
			return false
		}
	}
	return false
}

func (r *routeRuntime) selectionWaitError(scheduler *routing.Scheduler, resilience *Resilience) error {
	var minWait time.Duration
	waitSet := false
	seenKeys := map[ResilienceKey]struct{}{}

	for _, state := range scheduler.EntryStates() {
		resolved, err := resolveAgentSpec(state.Entry.Spec, nil)
		if err != nil {
			continue
		}
		key := KeyFromAgent(resolved)
		if _, ok := seenKeys[key]; ok {
			continue
		}
		seenKeys[key] = struct{}{}

		status, since := resilience.GetState(key)
		if status != StatePaused {
			continue
		}

		wait := since.Add(resilience.PauseDuration).Sub(resilience.NowFunc())
		if wait < 0 {
			wait = 0
		}
		if !waitSet || wait < minWait {
			minWait = wait
			waitSet = true
		}
	}

	if waitSet {
		return &routeSelectionError{
			Wait:    minWait,
			message: "all agents paused",
		}
	}

	return &routeSelectionError{
		AllFrozen: true,
		message:   "all agents frozen",
	}
}

// forceUnpauseAll moves every paused harness across the runtime's schedulers
// back to active state. Used when the user hits skip during a frozen-wait to
// retry immediately rather than serving out the pause window.
func (r *routeRuntime) forceUnpauseAll(resilience *Resilience, relayID int) (int, error) {
	seen := map[ResilienceKey]struct{}{}
	unpaused := 0
	for _, scheduler := range r.schedulers {
		for _, state := range scheduler.EntryStates() {
			resolved, err := resolveAgentSpec(state.Entry.Spec, nil)
			if err != nil {
				continue
			}
			key := KeyFromAgent(resolved)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			status, _ := resilience.GetState(key)
			if status != StatePaused {
				continue
			}
			if err := resilience.UnpauseAgent(key, relayID); err != nil {
				return unpaused, err
			}
			unpaused++
		}
	}
	return unpaused, nil
}

func legacyMixRouteEntries(specs []string, resolver Resolver) ([]string, string, error) {
	mix, err := ParseAgentMix(specs, resolver)
	if err != nil {
		return nil, "", err
	}

	if len(mix.Cycle) == 0 {
		return nil, mix.Label, fmt.Errorf("routing: legacy mix produced no entries")
	}

	entries := make([]string, 0, len(mix.Cycle))
	for i := 0; i < len(mix.Cycle); {
		current := mix.Cycle[i]
		count := 1
		for j := i + 1; j < len(mix.Cycle); j++ {
			if mix.Cycle[j] != current {
				break
			}
			count++
		}
		entries = append(entries, fmt.Sprintf("%s:%d", agentRouteSpec(current), count))
		i += count
	}

	return entries, mix.Label, nil
}

func resolveRouteEntries(entries []routing.ParsedEntry, resolver Resolver) ([]routing.ParsedEntry, error) {
	resolved := make([]routing.ParsedEntry, len(entries))
	for i, entry := range entries {
		picked, err := resolveAgentSpec(entry.Spec, resolver)
		if err != nil {
			return nil, err
		}
		entry.Spec = agentRouteSpec(picked)
		resolved[i] = entry
	}
	return resolved, nil
}

func resolveAgentSpec(spec string, resolver Resolver) (agent.ResolvedAgent, error) {
	if resolver != nil {
		return resolver(spec)
	}

	parts := strings.SplitN(spec, ":", 2)
	aliases := map[string]string{
		"ag": "antigravity", "agy": "antigravity", "antigravity": "antigravity",
		"cc": "claude", "claude": "claude",
		"cx": "codex", "codex": "codex",
		"ge": "gemini", "gemini": "gemini",
		"op": "opencode", "opencode": "opencode",
	}

	harness := parts[0]
	if mapped, ok := aliases[harness]; ok {
		harness = mapped
	}

	resolved := agent.ResolvedAgent{Harness: harness}
	if len(parts) == 2 {
		resolved.Model = parts[1]
	}
	return resolved, nil
}

func agentRouteSpec(resolved agent.ResolvedAgent) string {
	spec := resolved.Harness
	if resolved.Model != "" {
		spec += ":" + resolved.Model
	}
	return spec
}

func cloneParsedEntries(entries []routing.ParsedEntry) []routing.ParsedEntry {
	return append([]routing.ParsedEntry(nil), entries...)
}
