package reliability

import (
	"context"
	"time"
)

const (
	DefaultProbeTimeout         = 30 * time.Second
	DefaultAmbiguousProbeWindow = 60 * time.Second
)

type LivenessProbe struct {
	timeout time.Duration
	run     func(context.Context) (bool, error)
}

func NewLivenessProbe(timeout time.Duration, run func(context.Context) (bool, error)) *LivenessProbe {
	if run == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	return &LivenessProbe{
		timeout: timeout,
		run:     run,
	}
}

func (p *LivenessProbe) Check(ctx context.Context) bool {
	if p == nil || p.run == nil {
		return false
	}

	probeCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	ok, err := p.run(probeCtx)
	if err != nil {
		return false
	}
	return ok
}
