package cli

import (
	"fmt"
	"io"

	"github.com/mitchell-wallace/rally/internal/agent_prompt"
)

func renderRouteCheckResult(w io.Writer, result RouteCheckResult) {
	fmt.Fprintln(w, "routes check summary:")
	if len(result.Summaries) == 0 {
		fmt.Fprintln(w, "- no routes declared")
	} else {
		for _, summary := range result.Summaries {
			fmt.Fprintf(w, "- %s: %d %s\n", summary.Name, summary.EntryCount, pluralize(summary.EntryCount, "entry", "entries"))
		}
	}

	if len(result.ProviderSummary) > 0 {
		fmt.Fprintln(w, "\nproviders (shared-quota groups):")
		for _, p := range result.ProviderSummary {
			status := ""
			if p.Disabled {
				status = " [disabled]"
			}
			fmt.Fprintf(w, "- %s: %d %s%s\n", p.Name, p.MemberCount, pluralize(p.MemberCount, "model", "models"), status)
		}
	}

	if len(result.RoleDiagnostics) > 0 {
		fmt.Fprintln(w, "\nrole prompt diagnostics:")
		for _, diag := range result.RoleDiagnostics {
			src := "embedded"
			if diag.IsCustom {
				src = fmt.Sprintf("custom, .rally/agents/%s.md", diag.Role)
			}
			fmt.Fprintf(w, "- %s: ~%d tokens (%s)\n", diag.Role, diag.TokenCount, src)
		}
	}

	if len(result.Warnings) > 0 || len(result.Infos) > 0 {
		fmt.Fprintln(w)
	}

	for _, warning := range result.Warnings {
		fmt.Fprintln(w, warning)
	}
	for _, info := range result.Infos {
		fmt.Fprintln(w, info)
	}

	for _, overlap := range result.Overlaps {
		fmt.Fprintf(w, "\nadvisory: custom role prompt .rally/agents/%s.md references %q.\n", overlap.Role, overlap.MatchTerm)
		fmt.Fprintln(w, "This may overlap with the shared guidance which is automatically injected.")

		snippetName := "finalize.md"
		snippetText := agent_prompt.Finalize()
		if overlap.IsHeadless {
			snippetName = "headless.md"
			snippetText = agent_prompt.Headless()
		}

		fmt.Fprintf(w, "For comparison, the embedded general/%s snippet is:\n", snippetName)
		fmt.Fprintln(w, "---")
		fmt.Fprintln(w, snippetText)
		fmt.Fprintln(w, "---")
	}
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
