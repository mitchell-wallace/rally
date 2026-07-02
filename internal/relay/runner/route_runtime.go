package runner

import (
	"fmt"
	"sort"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/routing"
	"github.com/mitchell-wallace/rally/internal/store"
)

type routeRuntime struct {
	selector          *routing.Selector
	override          *routing.OverrideRoute
	schedulers        map[string]*routing.Scheduler
	resolver          relaycore.Resolver
	reasoning         map[string]string
	reasoningResolver routing.RoleReasoningResolver
	providers         *routing.ProviderIndex
	store             *store.Store
	lastAgent         map[string]harnessapi.ResolvedAgent
	warnings          []string
}

// quotaScope resolves the provider-aware quota bucket for a runner. A nil
// provider index falls back to the harness-default routing.QuotaScope.
func (r *routeRuntime) quotaScope(harness, model string) string {
	return r.providers.QuotaScope(harness, model)
}

// applyProviders attaches the resolved provider index and warns once per
// disabled provider that has entries in the configured routes, so operators see
// at relay start that a lane has been intentionally narrowed.
func (r *routeRuntime) applyProviders(idx *routing.ProviderIndex) {
	r.providers = idx
	if idx == nil {
		return
	}

	schedulerNames := make([]string, 0, len(r.schedulers))
	for name := range r.schedulers {
		schedulerNames = append(schedulerNames, name)
	}
	sort.Strings(schedulerNames)

	warned := map[string]bool{}
	for _, schedulerName := range schedulerNames {
		scheduler := r.schedulers[schedulerName]
		for _, state := range scheduler.EntryStates() {
			resolved, err := r.resolvedEntryAgent(state.Entry, schedulerName)
			if err != nil {
				continue
			}
			name, ok := idx.ProviderFor(resolved.Harness, resolved.Model)
			if !ok || !idx.Disabled(resolved.Harness, resolved.Model) || warned[name] {
				continue
			}
			warned[name] = true
			r.warnings = append(r.warnings, fmt.Sprintf("warning: provider %q is disabled; its runners are sidelined for this relay", name))
		}
	}
}

func (r *routeRuntime) Warnings() []string {
	if r.warnings == nil {
		return nil
	}
	out := make([]string, len(r.warnings))
	copy(out, r.warnings)
	return out
}

type routeSelection struct {
	Agent             harnessapi.ResolvedAgent
	PreviousAgent     *harnessapi.ResolvedAgent
	Route             routing.Route
	Entry             *routing.EntryState
	Scheduler         *routing.Scheduler
	HourlyRetry       bool
	Probation         bool
	EffectiveAssignee string
	RecoveryForced    bool
	RecoveryCapHit    bool
	RecoveryStatus    store.RecoveryPendingStatus
}

type routeSelectionError struct {
	Wait              time.Duration
	AllFrozen         bool
	RouteName         string
	EffectiveAssignee string
	message           string
}

func (e *routeSelectionError) Error() string {
	return e.message
}
