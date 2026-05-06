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

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/routing"
	"github.com/spf13/cobra"
)

const defaultRouteKey = "default"

var resolveWorkspaceDir = defaultResolveWorkspaceDir
var loadConfig = config.LoadV2

type RouteCheckResult struct {
	Summaries []RouteSummary
	Warnings  []string
	Infos     []string
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

	return result, nil
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
	for _, warning := range result.Warnings {
		fmt.Fprintln(w, warning)
	}
	for _, info := range result.Infos {
		fmt.Fprintln(w, info)
	}
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

	for _, name := range []string{"cc", "claude", "cx", "codex", "ge", "gemini", "op", "opencode"} {
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
