package runner

import (
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/relay"
)

func TestRouteRuntimeStoredLabelsRenderThroughRelayFormatter(t *testing.T) {
	routeSpecs := map[string][]string{
		"default": {"claude:opus-4.7", "op:opencode-go/kimi-k2.6"},
	}

	_, routesLabel, err := newRouteRuntimeFromConfig(Config{
		RouteSpecs: routeSpecs,
		Resolver:   testResolver,
	})
	if err != nil {
		t.Fatalf("newRouteRuntimeFromConfig() error = %v", err)
	}
	if routesLabel != "__routes__" {
		t.Fatalf("route label = %q, want __routes__", routesLabel)
	}
	if got := relay.FormatMixLabel(routesLabel); got != "configured routes" {
		t.Fatalf("FormatMixLabel(%q) = %q, want configured routes", routesLabel, got)
	}

	_, overrideLabel, err := newOverrideRouteRuntime([]string{"cc", "op:opencode-go/kimi-k2.6"}, routeSpecs, testResolver, false)
	if err != nil {
		t.Fatalf("newOverrideRouteRuntime() error = %v", err)
	}
	if overrideLabel != "__override__:cc op:opencode-go/kimi-k2.6" {
		t.Fatalf("override label = %q, want __override__:cc op:opencode-go/kimi-k2.6", overrideLabel)
	}
	if got := relay.FormatMixLabel(overrideLabel); got != "cc op:opencode-go/kimi-k2.6" {
		t.Fatalf("FormatMixLabel(%q) = %q, want override specs", overrideLabel, got)
	}
}

func TestRouteRuntime_SingleRunnerLaneWarns(t *testing.T) {
	rt, _ := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"solo": {"claude:opus-4.7"},
	}, false)

	warnings := rt.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "solo") || !strings.Contains(warnings[0], "single runner") {
		t.Fatalf("warning = %q, want single-runner warning for lane %q", warnings[0], "solo")
	}
}

func TestRouteRuntime_MultiRunnerLaneDoesNotWarn(t *testing.T) {
	rt, _ := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7", "codex:gpt-5.5"},
	}, false)

	warnings := rt.Warnings()
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for multi-runner lane, got %d: %v", len(warnings), warnings)
	}
}

func TestRouteRuntime_SingleRunnerOverrideWarns(t *testing.T) {
	rt, _ := newOverrideRouteRuntimeOrDie(t, []string{"op:opencode-go/fancy-model"}, map[string][]string{
		"default": {"claude:opus-4.7", "codex:gpt-5.5"},
	}, false)

	warnings := rt.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for single-runner override, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "override") || !strings.Contains(warnings[0], "single runner") {
		t.Fatalf("warning = %q, want single-runner warning for override lane", warnings[0])
	}
}

func TestRouteRuntime_MixedLanesWarnsOnlySingleRunner(t *testing.T) {
	rt, _ := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7", "codex:gpt-5.5"},
		"fragile": {"antigravity:pro"},
	}, false)

	warnings := rt.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning (fragile lane only), got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "fragile") {
		t.Fatalf("warning = %q, want warning for %q lane", warnings[0], "fragile")
	}
}
