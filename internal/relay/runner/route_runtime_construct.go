package runner

import (
	"fmt"
	"strings"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/routing"
)

const (
	relaySelectionModeRoutes         = "__routes__"
	relaySelectionModeOverridePrefix = "__override__:"
)

func newRouteRuntimeFromConfig(cfg Config) (*routeRuntime, string, error) {
	var (
		rt    *routeRuntime
		label string
		err   error
	)
	switch {
	case cfg.UseOverrideRoute:
		rt, label, err = newOverrideRouteRuntimeWithReasoning(cfg.AgentMixSpecs, cfg.RouteSpecs, cfg.Resolver, cfg.Reasoning, cfg.ReasoningResolver, !cfg.LapsEnabled)
	case len(cfg.RouteSpecs) > 0:
		rt, err = newResolvedRouteRuntimeWithReasoning(cfg.RouteSpecs, cfg.Resolver, cfg.Reasoning, cfg.ReasoningResolver, !cfg.LapsEnabled, nil)
		label = relaySelectionModeRoutes
	default:
		rt, label, err = newLegacyMixRouteRuntime(cfg.AgentMixSpecs, cfg.Resolver, !cfg.LapsEnabled)
	}
	if err == nil && rt != nil {
		rt.applyProviders(cfg.Providers)
	}
	return rt, label, err
}

func newRouteRuntimeFromStoredLabel(cfg Config, stored string) (*routeRuntime, string, error) {
	var (
		rt    *routeRuntime
		label string
		err   error
	)
	switch {
	case stored == relaySelectionModeRoutes:
		if len(cfg.RouteSpecs) == 0 {
			return nil, "", fmt.Errorf("resume relay: stored route-based relay requires configured routes")
		}
		rt, err = newResolvedRouteRuntimeWithReasoning(cfg.RouteSpecs, cfg.Resolver, cfg.Reasoning, cfg.ReasoningResolver, !cfg.LapsEnabled, nil)
		label = relaySelectionModeRoutes
	case strings.HasPrefix(stored, relaySelectionModeOverridePrefix):
		specs := strings.Fields(strings.TrimSpace(strings.TrimPrefix(stored, relaySelectionModeOverridePrefix)))
		rt, label, err = newOverrideRouteRuntimeWithReasoning(specs, cfg.RouteSpecs, cfg.Resolver, cfg.Reasoning, cfg.ReasoningResolver, !cfg.LapsEnabled)
	default:
		rt, label, err = newLegacyMixRouteRuntime(strings.Fields(stored), cfg.Resolver, !cfg.LapsEnabled)
	}
	if err == nil && rt != nil {
		rt.applyProviders(cfg.Providers)
	}
	return rt, label, err
}

func newLegacyMixRouteRuntime(specs []string, resolver relaycore.Resolver, noBackend bool) (*routeRuntime, string, error) {
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

func newOverrideRouteRuntime(specs []string, routeSpecs map[string][]string, resolver relaycore.Resolver, noBackend bool) (*routeRuntime, string, error) {
	return newOverrideRouteRuntimeWithReasoning(specs, routeSpecs, resolver, nil, nil, noBackend)
}

func newOverrideRouteRuntimeWithReasoning(specs []string, routeSpecs map[string][]string, resolver relaycore.Resolver, reasoning map[string]string, reasoningResolver routing.RoleReasoningResolver, noBackend bool) (*routeRuntime, string, error) {
	override, err := routing.BuildOverrideRoute("override", specs, routeSpecs, routing.AgentResolver(resolver))
	if err != nil {
		return nil, "", err
	}

	rt, err := newResolvedRouteRuntimeWithReasoning(routeSpecs, resolver, reasoning, reasoningResolver, noBackend, override)
	if err != nil {
		return nil, "", err
	}

	return rt, relaySelectionModeOverridePrefix + strings.Join(specs, " "), nil
}

func newResolvedRouteRuntime(routeSpecs map[string][]string, resolver relaycore.Resolver, noBackend bool, override *routing.OverrideRoute) (*routeRuntime, error) {
	return newResolvedRouteRuntimeWithReasoning(routeSpecs, resolver, nil, nil, noBackend, override)
}

func newResolvedRouteRuntimeWithReasoning(routeSpecs map[string][]string, resolver relaycore.Resolver, reasoning map[string]string, reasoningResolver routing.RoleReasoningResolver, noBackend bool, override *routing.OverrideRoute) (*routeRuntime, error) {
	selector, err := routing.NewSelector(routeSpecs, noBackend)
	if err != nil {
		return nil, err
	}

	var warnings []string
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
		if len(resolvedEntries) == 1 {
			warnings = append(warnings, fmt.Sprintf("warning: lane %q has a single runner (%s) — if it fails, the lane stalls with no fallback", name, resolvedEntries[0].Spec))
		}
	}

	if override != nil {
		overrideEntries := cloneParsedEntries(override.Entries)
		schedulers[strings.ToLower(override.Name)] = routing.NewScheduler(overrideEntries)
		if len(overrideEntries) == 1 {
			warnings = append(warnings, fmt.Sprintf("warning: lane %q has a single runner (%s) — if it fails, the lane stalls with no fallback", override.Name, overrideEntries[0].Spec))
		}
	}

	return &routeRuntime{
		selector:          selector,
		override:          override,
		schedulers:        schedulers,
		resolver:          resolver,
		reasoning:         reasoning,
		reasoningResolver: reasoningResolver,
		lastAgent:         make(map[string]harnessapi.ResolvedAgent, len(schedulers)),
		warnings:          warnings,
	}, nil
}

func legacyMixRouteEntries(specs []string, resolver relaycore.Resolver) ([]string, string, error) {
	mix, err := relaycore.ParseAgentMix(specs, resolver)
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

func resolveRouteEntries(entries []routing.ParsedEntry, resolver relaycore.Resolver) ([]routing.ParsedEntry, error) {
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

func resolveAgentSpec(spec string, resolver relaycore.Resolver) (harnessapi.ResolvedAgent, error) {
	if resolver != nil {
		return resolver(spec)
	}

	parts := strings.SplitN(spec, ":", 2)
	aliases := map[string]string{
		"ag": "antigravity", "agy": "antigravity", "antigravity": "antigravity",
		"cc": "claude", "claude": "claude",
		"cx": "codex", "codex": "codex",
		"op": "opencode", "opencode": "opencode",
	}

	harness := parts[0]
	if mapped, ok := aliases[harness]; ok {
		harness = mapped
	}

	resolved := harnessapi.ResolvedAgent{Harness: harness}
	if len(parts) == 2 {
		resolved.Model = parts[1]
	}
	return resolved, nil
}

func agentRouteSpec(resolved harnessapi.ResolvedAgent) string {
	spec := resolved.Harness
	if resolved.Model != "" {
		spec += ":" + resolved.Model
	}
	return spec
}

func cloneParsedEntries(entries []routing.ParsedEntry) []routing.ParsedEntry {
	return append([]routing.ParsedEntry(nil), entries...)
}
