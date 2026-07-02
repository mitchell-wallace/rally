package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mitchell-wallace/rally/internal/agent_prompt"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/routing"
	"github.com/mitchell-wallace/rally/internal/user_prompt/roleloader"
)

const defaultRouteKey = "default"

type removedAliasRouteError struct {
	msg     string
	alias   string
	warning string
}

func (e *removedAliasRouteError) Error() string {
	return e.msg
}

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
