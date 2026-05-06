package routing

import (
	"strings"
	"testing"
)

func newSelectorOrDie(t *testing.T, routes map[string][]string, noBackend bool) *Selector {
	t.Helper()
	selector, err := NewSelector(routes, noBackend)
	if err != nil {
		t.Fatalf("NewSelector() error = %v", err)
	}
	return selector
}

func TestSelector_OverrideWins(t *testing.T) {
	selector := newSelectorOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7"},
		"SENIOR":  {"codex:gpt-5.5"},
	}, false)

	override, err := ParseRoute("manual", []string{"opencode:opencode-go/kimi-k2.6:2"})
	if err != nil {
		t.Fatalf("ParseRoute() error = %v", err)
	}

	route, err := selector.ActiveRoute(Lap{Assignee: "SENIOR"}, &override)
	if err != nil {
		t.Fatalf("ActiveRoute() error = %v", err)
	}

	if route.Source != RouteSourceOverride {
		t.Fatalf("Source = %q, want %q", route.Source, RouteSourceOverride)
	}
	if route.Name != "manual" {
		t.Fatalf("Name = %q, want manual", route.Name)
	}
	if len(route.Entries) != 1 || route.Entries[0].Spec != "opencode:opencode-go/kimi-k2.6" {
		t.Fatalf("override entries = %+v, want kimi override", route.Entries)
	}
	if route.Warning != "" {
		t.Fatalf("Warning = %q, want empty", route.Warning)
	}
}

func TestSelector_AssigneeMatchIsCaseInsensitive(t *testing.T) {
	selector := newSelectorOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7"},
		"SENIOR":  {"codex:gpt-5.5:3"},
	}, false)

	route, err := selector.ActiveRoute(Lap{Assignee: "Senior"}, nil)
	if err != nil {
		t.Fatalf("ActiveRoute() error = %v", err)
	}

	if route.Source != RouteSourceAssignee {
		t.Fatalf("Source = %q, want %q", route.Source, RouteSourceAssignee)
	}
	if route.Name != "SENIOR" {
		t.Fatalf("Name = %q, want SENIOR", route.Name)
	}
	if len(route.Entries) != 1 || route.Entries[0].Spec != "codex:gpt-5.5" {
		t.Fatalf("entries = %+v, want codex SENIOR route", route.Entries)
	}
	if route.Warning != "" {
		t.Fatalf("Warning = %q, want empty", route.Warning)
	}
}

func TestSelector_FallsBackToDefaultWhenAssigneeMissing(t *testing.T) {
	selector := newSelectorOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7"},
	}, false)

	route, err := selector.ActiveRoute(Lap{Assignee: "ROLEX"}, nil)
	if err != nil {
		t.Fatalf("ActiveRoute() error = %v", err)
	}

	if route.Source != RouteSourceDefault {
		t.Fatalf("Source = %q, want %q", route.Source, RouteSourceDefault)
	}
	if route.Name != "default" {
		t.Fatalf("Name = %q, want default", route.Name)
	}
	if route.Warning == "" {
		t.Fatal("Warning = empty, want fallback warning")
	}
	if !strings.Contains(route.Warning, "ROLEX") {
		t.Fatalf("Warning = %q, want unmatched assignee name", route.Warning)
	}
}

func TestSelector_UsesDefaultWithoutWarningWhenNoAssignee(t *testing.T) {
	selector := newSelectorOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7"},
		"SENIOR":  {"codex:gpt-5.5"},
	}, false)

	route, err := selector.ActiveRoute(Lap{}, nil)
	if err != nil {
		t.Fatalf("ActiveRoute() error = %v", err)
	}

	if route.Source != RouteSourceDefault {
		t.Fatalf("Source = %q, want %q", route.Source, RouteSourceDefault)
	}
	if route.Warning != "" {
		t.Fatalf("Warning = %q, want empty", route.Warning)
	}
}

func TestSelector_ErrorsWhenAssigneeMissingRouteAndNoDefault(t *testing.T) {
	selector := newSelectorOrDie(t, map[string][]string{
		"SENIOR": {"codex:gpt-5.5"},
	}, false)

	_, err := selector.ActiveRoute(Lap{Assignee: "ROLEX"}, nil)
	if err == nil {
		t.Fatal("ActiveRoute() error = nil, want missing default error")
	}
	if !strings.Contains(err.Error(), "ROLEX") {
		t.Fatalf("error = %v, want unmatched assignee", err)
	}
	if !strings.Contains(err.Error(), "default") {
		t.Fatalf("error = %v, want default mentioned", err)
	}
}

func TestSelector_NoBackendAlwaysUsesDefault(t *testing.T) {
	selector := newSelectorOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7"},
		"SENIOR":  {"codex:gpt-5.5"},
	}, true)

	route, err := selector.ActiveRoute(Lap{Assignee: "SENIOR"}, nil)
	if err != nil {
		t.Fatalf("ActiveRoute() error = %v", err)
	}

	if route.Source != RouteSourceDefault {
		t.Fatalf("Source = %q, want %q", route.Source, RouteSourceDefault)
	}
	if route.Name != "default" {
		t.Fatalf("Name = %q, want default", route.Name)
	}
	if len(route.Entries) != 1 || route.Entries[0].Spec != "claude:opus-4.7" {
		t.Fatalf("entries = %+v, want default route", route.Entries)
	}
	if route.Warning != "" {
		t.Fatalf("Warning = %q, want empty", route.Warning)
	}
}

func TestSelector_NoBackendOverrideStillWins(t *testing.T) {
	selector := newSelectorOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7"},
	}, true)

	override, err := ParseRoute("", []string{"codex:gpt-5.5"})
	if err != nil {
		t.Fatalf("ParseRoute() error = %v", err)
	}

	route, err := selector.ActiveRoute(Lap{}, &override)
	if err != nil {
		t.Fatalf("ActiveRoute() error = %v", err)
	}

	if route.Source != RouteSourceOverride {
		t.Fatalf("Source = %q, want %q", route.Source, RouteSourceOverride)
	}
	if route.Name != "override" {
		t.Fatalf("Name = %q, want override", route.Name)
	}
}
