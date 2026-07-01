package app

import (
	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/harness/antigravity"
	"github.com/mitchell-wallace/rally/internal/harness/claude"
	"github.com/mitchell-wallace/rally/internal/harness/codex"
	"github.com/mitchell-wallace/rally/internal/harness/generic"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
)

// BuildExecutors assembles the executor registry for a relay run: the built-in
// harnesses (antigravity/claude/codex/opencode) keyed by their canonical names,
// plus a GenericExecutor for each generic harness configured with a command.
func BuildExecutors(cfg config.V2Config) map[string]harnessapi.Executor {
	executors := map[string]harnessapi.Executor{
		"antigravity": antigravity.New(cfg.AntigravityModel),
		"claude":      claude.New(cfg.ClaudeModel),
		"codex":       codex.New(cfg.CodexModel),
		"opencode":    &agent.OpenCodeExecutor{Model: cfg.OpenCodeModel},
	}

	for name, hc := range cfg.Harnesses {
		if len(hc.Command) > 0 {
			executors[name] = generic.New(
				hc.Command,
				hc.ModelFlag,
				hc.OutputStrategy,
				hc.OutputLines,
				hc.TailStream,
				"", // no programmatic default model from config today
			)
		}
	}

	return executors
}
