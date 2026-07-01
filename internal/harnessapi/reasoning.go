package harnessapi

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// knownReasoningEfforts records the reasoning-effort values each harness's CLI
// documents. The sets come from spike-1 (release-0-10-0): codex and claude
// publish a fixed enum, while opencode treats the value as a provider-specific
// "variant" with no fixed set (so it is intentionally absent here). Antigravity
// has no effort flag and is handled separately in ApplyReasoningEffort.
var knownReasoningEfforts = map[string]map[string]bool{
	"codex":  {"none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true},
	"claude": {"low": true, "medium": true, "high": true, "xhigh": true, "max": true},
}

// ApplyReasoningEffort appends the harness-specific flag(s) that inject a
// resolved reasoning/variant effort onto args, returning the updated args and
// an optional human-readable warning.
//
//   - codex:       -c model_reasoning_effort=<value>
//   - claude:      --effort <value>
//   - opencode:    --variant <value>
//   - antigravity: unsupported as a flag — warn and skip (reasoning is encoded
//     in the model alias/name instead)
//
// Unknown effort values for a supported harness are passed through with a
// warning rather than a hard failure: the spike confirmed claude/opencode
// silently ignore unsupported values and codex defers rejection to the API, so
// rally must not pre-emptively fail a run on an effort token it does not
// recognise. An empty effort is a no-op.
func ApplyReasoningEffort(args []string, harness, effort string) ([]string, string) {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return args, ""
	}

	switch harness {
	case "codex":
		return append(args, "-c", "model_reasoning_effort="+effort), unknownEffortWarning(harness, effort)
	case "claude":
		return append(args, "--effort", effort), unknownEffortWarning(harness, effort)
	case "opencode":
		// opencode variants are provider-specific with no enumerable set, so
		// there is nothing to validate; an unknown variant is silently ignored
		// by the CLI.
		return append(args, "--variant", effort), ""
	case "antigravity":
		return args, fmt.Sprintf("rally: antigravity has no reasoning-effort flag; ignoring reasoning effort %q (reasoning is selected via the model alias/name)", effort)
	default:
		return args, fmt.Sprintf("rally: harness %q does not support reasoning-effort injection; ignoring reasoning effort %q", harness, effort)
	}
}

// unknownEffortWarning returns a pass-through warning when effort is outside the
// harness's documented set, or "" when the harness has no enumerable set or the
// value is recognised.
func unknownEffortWarning(harness, effort string) string {
	known := knownReasoningEfforts[harness]
	if known == nil || known[strings.ToLower(effort)] {
		return ""
	}
	return fmt.Sprintf("rally: unknown %s reasoning effort %q; passing it through (documented values: %s)",
		harness, effort, strings.Join(sortedEffortValues(known), ", "))
}

// IsKnownReasoningEffort reports whether effort matches a documented
// reasoning-effort value for any harness. Used by `rally routes check` to
// decide whether a bare `[reasoning]` token is a recognised effort (no warning)
// or an unrecognised value that will merely be passed through at runtime.
func IsKnownReasoningEffort(effort string) bool {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return false
	}
	for _, set := range knownReasoningEfforts {
		if set[effort] {
			return true
		}
	}
	return false
}

func sortedEffortValues(set map[string]bool) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

// EmitReasoningWarning surfaces a non-fatal reasoning-effort warning to the
// operator (stderr) and, when a try log path is set, records it in the log for
// later inspection.
func EmitReasoningWarning(logPath, warning string) {
	if warning == "" {
		return
	}
	fmt.Fprintln(os.Stderr, warning)
	if logPath == "" {
		return
	}
	if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintf(f, "\n--- rally reasoning ---\n%s\n", warning)
		_ = f.Close()
	}
}
