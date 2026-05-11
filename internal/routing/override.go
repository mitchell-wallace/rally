package routing

import (
	"fmt"
	"strings"
	"sync"

	"github.com/mitchell-wallace/rally/internal/agent"
)

const roleRefSpecPrefix = "__route_ref__:"

type AgentResolver func(spec string) (agent.ResolvedAgent, error)

type OverrideRoute struct {
	Route

	mu       sync.Mutex
	roleRefs map[string]*roleReference
}

type roleReference struct {
	name    string
	entries []ParsedEntry
	cursor  int
	step    int
}

func BuildOverrideRoute(name string, rawEntries []string, routeSpecs map[string][]string, resolver AgentResolver) (*OverrideRoute, error) {
	entries := make([]ParsedEntry, 0, len(rawEntries))
	roleRefs := map[string]*roleReference{}

	for idx, raw := range rawEntries {
		parsed, err := ParseEntry(raw)
		if err != nil {
			return nil, fmt.Errorf("routing: override entry %q: %w", raw, err)
		}

		routeName, specs, ok := lookupRouteSpec(parsed.Spec, routeSpecs)
		if ok {
			routeEntries, err := ParseEntries(specs)
			if err != nil {
				return nil, fmt.Errorf("routing: override role %q: %w", routeName, err)
			}
			for i := range routeEntries {
				if routeEntries[i], err = resolveParsedEntry(routeEntries[i], resolver); err != nil {
					return nil, fmt.Errorf("routing: override role %q entry %q: %w", routeName, routeEntries[i].Raw, err)
				}
			}

			if !parsed.HasQuota {
				entries = append(entries, routeEntries...)
				continue
			}
			if !parsed.QuotaSingle() {
				return nil, fmt.Errorf("routing: override role reference %q must use a single numeric quota", raw)
			}

			synth := ParsedEntry{
				Raw:      raw,
				Spec:     fmt.Sprintf("%s%s:%d", roleRefSpecPrefix, strings.ToLower(routeName), idx),
				QuotaMin: 1,
				QuotaMax: 1,
				HasQuota: true,
			}
			entries = append(entries, synth)
			roleRefs[synth.Spec] = &roleReference{
				name:    routeName,
				entries: routeEntries,
				step:    parsed.QuotaMin,
			}
			continue
		}

		parsed, err = resolveParsedEntry(parsed, resolver)
		if err != nil {
			return nil, fmt.Errorf("routing: override entry %q: %w", raw, err)
		}
		// Inject quota=1 for bare direct entries so multi-entry overrides like
		// `--agent "cc ge op"` round-robin instead of sticking on the first one.
		// This matches the legacyMixRouteEntries path (which stamps :N counts)
		// and the user-visible expectation that `--agent "cc ge op" --iterations 3`
		// runs each harness once.
		if !parsed.HasQuota {
			parsed.HasQuota = true
			parsed.QuotaMin = 1
			parsed.QuotaMax = 1
		}
		entries = append(entries, parsed)
	}

	return &OverrideRoute{
		Route: Route{
			Name:    name,
			Entries: entries,
		},
		roleRefs: roleRefs,
	}, nil
}

func (o *OverrideRoute) ResolveSelection(entry ParsedEntry) (ParsedEntry, error) {
	if o == nil {
		return ParsedEntry{}, fmt.Errorf("routing: nil override route")
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	ref, ok := o.roleRefs[entry.Spec]
	if !ok {
		return entry, nil
	}
	if len(ref.entries) == 0 {
		return ParsedEntry{}, fmt.Errorf("routing: override role %q has no entries", ref.name)
	}

	selected := ref.entries[ref.cursor]
	ref.cursor = (ref.cursor + ref.step) % len(ref.entries)
	return selected, nil
}

func (o *OverrideRoute) HasDynamicRoleRefs() bool {
	if o == nil {
		return false
	}
	return len(o.roleRefs) > 0
}

func lookupRouteSpec(spec string, routeSpecs map[string][]string) (string, []string, bool) {
	if len(routeSpecs) == 0 {
		return "", nil, false
	}

	want := strings.ToLower(strings.TrimSpace(spec))
	for name, entries := range routeSpecs {
		if strings.ToLower(name) == want {
			return name, entries, true
		}
	}
	return "", nil, false
}

func resolveParsedEntry(entry ParsedEntry, resolver AgentResolver) (ParsedEntry, error) {
	if resolver == nil {
		return entry, nil
	}

	resolved, err := resolver(entry.Spec)
	if err != nil {
		return ParsedEntry{}, err
	}

	entry.Spec = resolved.Harness
	if resolved.Model != "" {
		entry.Spec += ":" + resolved.Model
	}
	return entry, nil
}
