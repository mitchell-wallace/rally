package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/agent_prompt"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/routing"
	"github.com/mitchell-wallace/rally/internal/user_prompt/roleloader"
	"github.com/spf13/cobra"
)

const defaultRouteKey = "default"

var resolveWorkspaceDir = defaultResolveWorkspaceDir
var loadConfig = config.LoadV2

type RouteCheckResult struct {
	Summaries       []RouteSummary
	ProviderSummary []ProviderSummary
	RoleDiagnostics []RoleDiagnostic
	Overlaps        []RoleOverlap
	Warnings        []string
	Infos           []string
}

// ProviderSummary describes one [providers] quota group for `rally routes check`.
type ProviderSummary struct {
	Name        string
	MemberCount int
	Disabled    bool
}

type RoleDiagnostic struct {
	Role       string
	TokenCount int
	IsCustom   bool
}

type RoleOverlap struct {
	Role       string
	MatchTerm  string
	IsHeadless bool
}

type RouteSummary struct {
	Name       string
	EntryCount int
}

func NewRoutesCmd() *cobra.Command {
	routesCmd := &cobra.Command{
		Use:   "routes",
		Short: "Inspect route configuration",
	}

	checkCmd := &cobra.Command{
		Use:          "check",
		Short:        "Validate [routes] configuration",
		SilenceUsage: true,
		RunE:         runRoutesCheck,
	}

	routesCmd.AddCommand(checkCmd)
	return routesCmd
}

func runRoutesCheck(cmd *cobra.Command, args []string) error {
	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}

	cfg, err := loadConfig(workspaceDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	result, err := CheckRoutes(workspaceDir, cfg)
	renderRouteCheckResult(cmd.OutOrStdout(), result)
	return err
}

func CheckRoutes(workspaceDir string, cfg config.V2Config) (RouteCheckResult, error) {
	result := RouteCheckResult{}

	names := sortedRouteNames(cfg.Routes)
	for _, name := range names {
		route, err := routing.ParseRoute(name, cfg.Routes[name])
		if err != nil {
			return result, fmt.Errorf("routes check: %w", err)
		}
		for _, entry := range route.Entries {
			if err := validateRouteEntry(cfg, name, entry); err != nil {
				return result, err
			}
		}
		result.Summaries = append(result.Summaries, RouteSummary{
			Name:       name,
			EntryCount: len(route.Entries),
		})
	}

	providerCounts, err := cfg.ProviderMemberCounts()
	if err != nil {
		return result, fmt.Errorf("routes check: %w", err)
	}

	providerNames := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	for _, name := range providerNames {
		pc := cfg.Providers[name]
		result.ProviderSummary = append(result.ProviderSummary, ProviderSummary{
			Name:        name,
			MemberCount: providerCounts[name],
			Disabled:    pc.Disabled,
		})
		if pc.Disabled {
			result.Infos = append(result.Infos,
				fmt.Sprintf("info: provider %q is disabled; its runners are sidelined until you re-enable it", name))
		}
	}

	reasoningWarnings, err := validateReasoning(cfg)
	if err != nil {
		return result, err
	}
	result.Warnings = append(result.Warnings, reasoningWarnings...)

	for _, note := range cfg.DeprecationNotes {
		result.Warnings = append(result.Warnings, "warning: "+note)
	}
	if cfg.SchemaWarning != "" {
		result.Warnings = append(result.Warnings, "warning: "+cfg.SchemaWarning)
	}

	if len(cfg.Routes) > 0 && !hasDefaultRoute(cfg.Routes) {
		result.Warnings = append(result.Warnings, "warning: no default route is configured; laps without a matching assignee will fail at run-time")
	}

	activeAssignees, warning, err := collectActiveAssignees(workspaceDir)
	if err != nil {
		return result, fmt.Errorf("routes check: inspect active assignees: %w", err)
	}
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}

	for _, name := range names {
		if strings.EqualFold(name, defaultRouteKey) {
			continue
		}
		if _, ok := activeAssignees[strings.ToLower(name)]; ok {
			continue
		}
		result.Infos = append(result.Infos,
			fmt.Sprintf("info: route %q is declared but not referenced by any current lap assignee", name))
	}

	diags, overlaps, err := checkRoles(workspaceDir)
	if err != nil {
		return result, fmt.Errorf("routes check: %w", err)
	}
	result.RoleDiagnostics = diags
	result.Overlaps = overlaps

	return result, nil
}

func checkRoles(workspaceDir string) ([]RoleDiagnostic, []RoleOverlap, error) {
	rolesMap := map[string]struct{}{}
	for _, r := range agent_prompt.Roles() {
		rolesMap[strings.ToLower(r)] = struct{}{}
	}

	agentsDir := filepath.Join(workspaceDir, ".rally", "agents")
	entries, err := os.ReadDir(agentsDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(name, ".md") {
				base := strings.TrimSuffix(name, ".md")
				rolesMap[strings.ToLower(base)] = struct{}{}
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("read agents dir: %w", err)
	}

	names := make([]string, 0, len(rolesMap))
	for name := range rolesMap {
		names = append(names, name)
	}
	sort.Strings(names)

	var diags []RoleDiagnostic
	var overlaps []RoleOverlap

	for _, name := range names {
		var content string
		isCustom := false

		customPath := filepath.Join(agentsDir, name+".md")
		if data, err := os.ReadFile(customPath); err == nil {
			content = string(data)
			isCustom = true
		} else {
			// case-variant fallback (like roleloader does, but simplified here; we can use roleloader.Loader)
			loaded, err := roleloader.Loader{WorkspaceDir: workspaceDir}.Load(name)
			if err != nil {
				return nil, nil, fmt.Errorf("load role %q: %w", name, err)
			}
			if loaded != "" {
				content = loaded
				isCustom = true
			} else {
				content, _ = agent_prompt.Role(name)
			}
		}

		tokCount := len(content) / 4
		diags = append(diags, RoleDiagnostic{
			Role:       name,
			TokenCount: tokCount,
			IsCustom:   isCustom,
		})

		if isCustom {
			lowerContent := strings.ToLower(content)
			if strings.Contains(lowerContent, "laps done") {
				overlaps = append(overlaps, RoleOverlap{Role: name, MatchTerm: "laps done", IsHeadless: false})
			} else if strings.Contains(lowerContent, "laps handoff") {
				overlaps = append(overlaps, RoleOverlap{Role: name, MatchTerm: "laps handoff", IsHeadless: false})
			} else if strings.Contains(lowerContent, "laps wrapup") {
				overlaps = append(overlaps, RoleOverlap{Role: name, MatchTerm: "laps wrapup", IsHeadless: false})
			} else if strings.Contains(lowerContent, "headless") {
				overlaps = append(overlaps, RoleOverlap{Role: name, MatchTerm: "headless", IsHeadless: true})
			}
		}
	}

	return diags, overlaps, nil
}

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

// validateReasoning checks the `[reasoning]` table. A harness-scoped model
// alias (e.g. `cc:opus-high`) names its harness, so a missing alias is almost
// certainly an operator typo and is reported as a hard error. A bare token is
// resolved against the route-selected harness only at runtime — it may be a
// model alias or a passthrough effort value — so it never hard-fails; it only
// warns when it matches neither a configured model alias nor a documented
// reasoning effort.
func validateReasoning(cfg config.V2Config) ([]string, error) {
	if len(cfg.Reasoning) == 0 {
		return nil, nil
	}

	roles := make([]string, 0, len(cfg.Reasoning))
	for role := range cfg.Reasoning {
		roles = append(roles, role)
	}
	sort.Strings(roles)

	var warnings []string
	for _, role := range roles {
		preference := strings.TrimSpace(cfg.Reasoning[role])
		if preference == "" {
			continue
		}

		if scopedHarness, _, scoped := strings.Cut(preference, ":"); scoped {
			if _, _, err := cfg.ResolveRoleReasoning(role, strings.TrimSpace(scopedHarness), preference); err != nil {
				return nil, fmt.Errorf("routes check: %w", err)
			}
			continue
		}

		if reasoningTokenRecognised(cfg, preference) {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"warning: [reasoning].%s value %q is not a known model alias or documented reasoning effort; it will be passed through to the selected harness as-is",
			role, preference))
	}

	return warnings, nil
}

func reasoningTokenRecognised(cfg config.V2Config, token string) bool {
	if agent.IsKnownReasoningEffort(token) {
		return true
	}
	for _, hc := range cfg.Harnesses {
		if hc == nil || hc.Models == nil {
			continue
		}
		if _, ok := hc.Models[token]; ok {
			return true
		}
	}
	return false
}

func validateRouteEntry(cfg config.V2Config, routeName string, entry routing.ParsedEntry) error {
	if _, err := cfg.ResolveAgent(entry.Spec); err != nil {
		return fmt.Errorf("routes check: route %q entry %q: %s", routeName, entry.Raw, decorateResolveError(cfg, entry.Spec, err))
	}
	return nil
}

func decorateResolveError(cfg config.V2Config, spec string, err error) string {
	errMsg := err.Error()
	if !strings.Contains(errMsg, "unknown agent alias") {
		return errMsg
	}

	alias := spec
	if idx := strings.Index(alias, ":"); idx >= 0 {
		alias = alias[:idx]
	}
	suggestions := topAliasSuggestions(alias, cfg)
	if len(suggestions) == 0 {
		return errMsg
	}
	return fmt.Sprintf("%s; did you mean %s?", errMsg, strings.Join(suggestions, ", "))
}

func topAliasSuggestions(target string, cfg config.V2Config) []string {
	candidates := aliasCandidates(cfg)
	if len(candidates) == 0 {
		return nil
	}

	type scored struct {
		name  string
		score int
	}

	ranked := make([]scored, 0, len(candidates))
	for _, candidate := range candidates {
		ranked = append(ranked, scored{name: candidate, score: levenshtein(target, candidate)})
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].name < ranked[j].name
		}
		return ranked[i].score < ranked[j].score
	})

	if len(ranked) > 3 {
		ranked = ranked[:3]
	}

	suggestions := make([]string, 0, len(ranked))
	for _, item := range ranked {
		suggestions = append(suggestions, item.name)
	}
	return suggestions
}

func aliasCandidates(cfg config.V2Config) []string {
	seen := map[string]bool{}
	candidates := []string{}

	for _, name := range []string{"ag", "agy", "antigravity", "cc", "claude", "cx", "codex", "ge", "gemini", "op", "opencode"} {
		if seen[name] {
			continue
		}
		seen[name] = true
		candidates = append(candidates, name)
	}

	for name := range cfg.Harnesses {
		if seen[name] {
			continue
		}
		seen[name] = true
		candidates = append(candidates, name)
	}

	sort.Strings(candidates)
	return candidates
}

func collectActiveAssignees(workspaceDir string) (map[string]struct{}, string, error) {
	assignees := map[string]struct{}{}

	lapsPath := filepath.Join(workspaceDir, ".laps", "laps.json")
	if _, err := os.Stat(lapsPath); err == nil {
		found, err := collectJSONAssignees(lapsPath)
		if err != nil {
			return nil, "", err
		}
		mergeAssignees(assignees, found)
	}

	return assignees, "", nil
}

func collectJSONAssignees(path string) (map[string]struct{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	assignees := map[string]struct{}{}
	collectNestedAssignees(payload, assignees)
	return assignees, nil
}

func collectNestedAssignees(value any, assignees map[string]struct{}) {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			if strings.EqualFold(key, "assignee") {
				if assignee, ok := child.(string); ok {
					addAssignee(assignees, assignee)
				}
			}
			collectNestedAssignees(child, assignees)
		}
	case []any:
		for _, child := range node {
			collectNestedAssignees(child, assignees)
		}
	}
}

func addAssignee(assignees map[string]struct{}, assignee string) {
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return
	}
	assignees[strings.ToLower(assignee)] = struct{}{}
}

func mergeAssignees(dst, src map[string]struct{}) {
	for assignee := range src {
		dst[assignee] = struct{}{}
	}
}

func hasDefaultRoute(routes map[string][]string) bool {
	for name := range routes {
		if strings.EqualFold(name, defaultRouteKey) {
			return true
		}
	}
	return false
}

func sortedRouteNames(routes map[string][]string) []string {
	names := make([]string, 0, len(routes))
	for name := range routes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func defaultResolveWorkspaceDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if root, ok, _ := gitx.GitRepoRoot(wd); ok {
		return root, nil
	}
	return wd, nil
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev, curr = curr, prev
	}

	return prev[lb]
}

func min(vals ...int) int {
	best := math.MaxInt
	for _, value := range vals {
		if value < best {
			best = value
		}
	}
	return best
}
