package routing

import (
	"fmt"
	"strings"
)

const defaultRouteKey = "default"

type RouteSource string

const (
	RouteSourceOverride RouteSource = "override"
	RouteSourceAssignee RouteSource = "assignee"
	RouteSourceDefault  RouteSource = "default"
)

type Bead struct {
	Assignee string
}

type Route struct {
	Name    string
	Entries []ParsedEntry
	Source  RouteSource
	Warning string
}

type Selector struct {
	routes       map[string]Route
	defaultRoute *Route
	noBackend    bool
}

func ParseRoute(name string, rawEntries []string) (Route, error) {
	entries, err := ParseEntries(rawEntries)
	if err != nil {
		return Route{}, fmt.Errorf("routing: route %q: %w", name, err)
	}

	return Route{
		Name:    name,
		Entries: entries,
	}, nil
}

func NewSelector(routeSpecs map[string][]string, noBackend bool) (*Selector, error) {
	routes := make(map[string]Route, len(routeSpecs))
	var defaultRoute *Route

	for name, specs := range routeSpecs {
		route, err := ParseRoute(name, specs)
		if err != nil {
			return nil, err
		}

		lower := strings.ToLower(name)
		routes[lower] = route
		if lower == defaultRouteKey {
			cp := route
			defaultRoute = &cp
		}
	}

	return &Selector{
		routes:       routes,
		defaultRoute: defaultRoute,
		noBackend:    noBackend,
	}, nil
}

func (s *Selector) ActiveRoute(bead Bead, override *Route) (Route, error) {
	if override != nil {
		route := cloneRoute(*override)
		route.Source = RouteSourceOverride
		route.Warning = ""
		if route.Name == "" {
			route.Name = "override"
		}
		return route, nil
	}

	if s.noBackend {
		return s.selectDefault("")
	}

	assignee := strings.TrimSpace(bead.Assignee)
	if assignee == "" {
		return s.selectDefault("")
	}

	if route, ok := s.routes[strings.ToLower(assignee)]; ok {
		selected := cloneRoute(route)
		selected.Source = RouteSourceAssignee
		selected.Warning = ""
		return selected, nil
	}

	return s.selectDefault(assignee)
}

func (s *Selector) selectDefault(unmatchedAssignee string) (Route, error) {
	if s.defaultRoute == nil {
		if unmatchedAssignee != "" {
			return Route{}, fmt.Errorf("routing: no route matched assignee %q and no default route is configured", unmatchedAssignee)
		}
		if s.noBackend {
			return Route{}, fmt.Errorf("routing: no-backend mode requires a default route")
		}
		return Route{}, fmt.Errorf("routing: no default route is configured")
	}

	selected := cloneRoute(*s.defaultRoute)
	selected.Source = RouteSourceDefault
	selected.Warning = ""
	if unmatchedAssignee != "" {
		selected.Warning = fmt.Sprintf("routing: no route matched assignee %q; falling back to default", unmatchedAssignee)
	}
	return selected, nil
}

func cloneRoute(route Route) Route {
	cloned := route
	if route.Entries != nil {
		cloned.Entries = append([]ParsedEntry(nil), route.Entries...)
	}
	return cloned
}
