package app

import (
	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
)

// BuildExecutors assembles the executor registry for a relay run: the built-in
// harnesses (antigravity/claude/codex/opencode) keyed by their canonical names,
// plus a GenericExecutor for each generic harness configured with a command.
func BuildExecutors(cfg config.V2Config) map[string]harnessapi.Executor {
	executors := map[string]harnessapi.Executor{
		"antigravity": &agent.AntigravityExecutor{Model: cfg.AntigravityModel},
		"claude":      &agent.ClaudeExecutor{Model: cfg.ClaudeModel},
		"codex":       &agent.CodexExecutor{Model: cfg.CodexModel},
		"opencode":    &agent.OpenCodeExecutor{Model: cfg.OpenCodeModel},
	}

	for name, hc := range cfg.Harnesses {
		if len(hc.Command) > 0 {
			executors[name] = &agent.GenericExecutor{
				Command:        hc.Command,
				ModelFlag:      hc.ModelFlag,
				OutputStrategy: hc.OutputStrategy,
				OutputLines:    hc.OutputLines,
				TailStream:     hc.TailStream,
			}
		}
	}

	return executors
}
