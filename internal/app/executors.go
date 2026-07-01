package app

import (
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/harness"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
)

// BuildExecutors assembles the executor registry for a relay run. It is a thin
// mapper: it translates the config schema (the built-in model fields and the
// cfg.Harnesses command specs) into the config-decoupled harness.Config and
// delegates to harness.BuildExecutors. The returned map still feeds
// runner.NewRunner, keeping concrete adapter types out of both cmd/rally and
// internal/relay/runner.
//
// ModelFlag is passed through as *string so "model_flag absent" (nil) stays
// distinct from "model_flag = \"\"" (empty string). GenericConfig.Model is left
// empty because the current config shape has no generic-harness default-model
// field; the registry skips Custom entries without a command, matching the
// pre-change executor set and keys.
func BuildExecutors(cfg config.V2Config) map[string]harnessapi.Executor {
	custom := make(map[string]harness.GenericConfig, len(cfg.Harnesses))
	for name, hc := range cfg.Harnesses {
		if hc == nil {
			continue
		}
		custom[name] = harness.GenericConfig{
			Command:        hc.Command,
			ModelFlag:      hc.ModelFlag,
			OutputStrategy: hc.OutputStrategy,
			OutputLines:    hc.OutputLines,
			TailStream:     hc.TailStream,
		}
	}

	return harness.BuildExecutors(harness.Config{
		ClaudeModel:      cfg.ClaudeModel,
		CodexModel:       cfg.CodexModel,
		OpenCodeModel:    cfg.OpenCodeModel,
		AntigravityModel: cfg.AntigravityModel,
		Custom:           custom,
	})
}
