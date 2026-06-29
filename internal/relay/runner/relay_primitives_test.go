package runner

import (
	"github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/store"
)

type AgentMix = relay.AgentMix
type AgentState = relay.AgentState
type Resilience = relay.Resilience
type ResilienceKey = relay.ResilienceKey
type Resolver = relay.Resolver

const (
	StateActive    = relay.StateActive
	StatePaused    = relay.StatePaused
	StateFrozen    = relay.StateFrozen
	StateProbation = relay.StateProbation
	StateBenched   = relay.StateBenched

	FreezeDuration            = relay.FreezeDuration
	PauseDuration             = relay.PauseDuration
	HourlyRetriesBeforeFreeze = relay.HourlyRetriesBeforeFreeze
	HourlyRetryMaxAttempts    = relay.HourlyRetryMaxAttempts
	BenchDefaultDuration      = relay.BenchDefaultDuration
)

func NewResilience(s *store.Store) *Resilience {
	return relay.NewResilience(s)
}

func ParseAgentMix(specs []string, resolver Resolver) (AgentMix, error) {
	return relay.ParseAgentMix(specs, resolver)
}
