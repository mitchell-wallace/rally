package runner

import (
	"strings"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/monitor"
	"github.com/mitchell-wallace/rally/internal/reliability"
)

var stallCheckInterval = monitor.TickInterval

func (r *Runner) newStallController(tryLogPath string, exec agent.Executor) reliability.StallController {
	if r.stallControllerFactory != nil {
		return r.stallControllerFactory(tryLogPath)
	}
	threshold := r.cfg.StallThreshold
	if threshold <= 0 {
		threshold = reliability.DefaultStallThreshold
	}
	netStatsPath := strings.TrimSuffix(tryLogPath, ".log") + ".netstat.jsonl"
	return reliability.NewStallControllerFull(tryLogPath, threshold, r.buildLivenessProbe(exec), netStatsPath)
}

func (r *Runner) buildLivenessProbe(exec agent.Executor) *reliability.LivenessProbe {
	if !r.cfg.LivenessProbe || exec == nil || !exec.LivenessProbeSupported() {
		return nil
	}
	return reliability.NewLivenessProbe(reliability.DefaultProbeTimeout, exec.ProbeLiveness)
}
