// Package harness owns executor construction: it is the only harness-layer
// package that imports the adapter subpackages. BuildExecutors assembles the
// built-in adapters keyed by canonical name plus one generic adapter per
// configured custom harness, from a config-decoupled Config so the harness
// layer never imports internal/config.
package harness

import (
	"github.com/mitchell-wallace/rally/internal/harness/antigravity"
	"github.com/mitchell-wallace/rally/internal/harness/claude"
	"github.com/mitchell-wallace/rally/internal/harness/codex"
	"github.com/mitchell-wallace/rally/internal/harness/generic"
	"github.com/mitchell-wallace/rally/internal/harness/opencode"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
)

// Config is the narrow adapter-shaped input the registry consumes. It carries
// the built-in harness model defaults plus one GenericConfig per configured
// custom harness. It mirrors the adapter construction fields (not the config
// schema), keeping the harness layer independent of internal/config.
type Config struct {
	ClaudeModel      string
	CodexModel       string
	OpenCodeModel    string
	AntigravityModel string
	Custom           map[string]GenericConfig
}

// GenericConfig carries the generic adapter's construction fields exactly.
// ModelFlag stays *string so "model_flag absent" (nil) remains distinct from
// "model_flag = \"\"" (empty string); Model preserves the programmatic
// default-model path used by direct construction and tests.
type GenericConfig struct {
	Command        []string
	ModelFlag      *string
	OutputStrategy string
	OutputLines    int
	TailStream     string
	Model          string
}

// BuildExecutors constructs the executor registry: the four built-in adapters
// (antigravity, claude, codex, opencode) keyed by canonical name, plus one
// generic adapter per Custom entry that declares a command. A Custom entry
// without a command is not a runnable generic harness (it may only define model
// aliases) and is skipped, so the returned set and keys match the pre-change
// inline map exactly. Custom entries are registered after the built-ins, so a
// configured custom harness keyed by a built-in name overrides the built-in,
// matching the pre-change override semantics.
func BuildExecutors(cfg Config) map[string]harnessapi.Executor {
	executors := map[string]harnessapi.Executor{
		"antigravity": antigravity.New(cfg.AntigravityModel),
		"claude":      claude.New(cfg.ClaudeModel),
		"codex":       codex.New(cfg.CodexModel),
		"opencode":    opencode.New(cfg.OpenCodeModel),
	}

	for name, gc := range cfg.Custom {
		if len(gc.Command) == 0 {
			continue
		}
		executors[name] = generic.New(
			gc.Command,
			gc.ModelFlag,
			gc.OutputStrategy,
			gc.OutputLines,
			gc.TailStream,
			gc.Model,
		)
	}

	return executors
}
