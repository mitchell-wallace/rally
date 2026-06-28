package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/laps"
)

func withStartupHeadPull(t *testing.T, fn func(context.Context, string) (laps.Lap, error)) {
	t.Helper()
	prev := headPullForStartupValidation
	headPullForStartupValidation = fn
	t.Cleanup(func() { headPullForStartupValidation = prev })
}

func TestValidateRelayStartupRoutes_QuotaErrorFails(t *testing.T) {
	workspaceDir := t.TempDir()
	cfg := config.V2Config{
		Routes: map[string][]string{
			"default": {"cc:0"},
		},
	}

	_, err := ValidateRelayStartupRoutes(context.Background(), workspaceDir, cfg, RelayStartupRouteOptions{})
	if err == nil {
		t.Fatal("ValidateRelayStartupRoutes() error = nil, want quota failure")
	}
	if !strings.Contains(err.Error(), `entry "cc:0"`) || !strings.Contains(err.Error(), "must be positive") {
		t.Fatalf("error = %q, want quota validation details", err.Error())
	}
}

func TestValidateRelayStartupRoutes_DuplicateByCaseFails(t *testing.T) {
	workspaceDir := t.TempDir()
	cfg := config.V2Config{
		Routes: map[string][]string{
			"default": {"cc"},
			"DEFAULT": {"cx"},
		},
	}

	_, err := ValidateRelayStartupRoutes(context.Background(), workspaceDir, cfg, RelayStartupRouteOptions{})
	if err == nil {
		t.Fatal("ValidateRelayStartupRoutes() error = nil, want duplicate-case failure")
	}
	if !strings.Contains(err.Error(), "duplicate route keys") {
		t.Fatalf("error = %q, want duplicate route key failure", err.Error())
	}
}

func TestValidateRelayStartupRoutes_RoleReferenceFails(t *testing.T) {
	workspaceDir := t.TempDir()
	cfg := config.V2Config{
		Routes: map[string][]string{
			"default": {"SENIOR"},
			"SENIOR":  {"cc"},
		},
	}

	_, err := ValidateRelayStartupRoutes(context.Background(), workspaceDir, cfg, RelayStartupRouteOptions{})
	if err == nil {
		t.Fatal("ValidateRelayStartupRoutes() error = nil, want role-reference failure")
	}
	if !strings.Contains(err.Error(), "role names are only valid in --agent") {
		t.Fatalf("error = %q, want role-reference rejection", err.Error())
	}
}

func TestValidateRelayStartupRoutes_PartialFailurePromptConfirmSucceeds(t *testing.T) {
	workspaceDir := t.TempDir()
	cfg := config.V2Config{
		Routes: map[string][]string{
			"default": {"cc"},
			"BROKEN":  {"claude:opus:4.7"},
		},
	}

	withStartupHeadPull(t, func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{Title: "assigned"}, nil
	})

	var output bytes.Buffer
	validRoutes, err := ValidateRelayStartupRoutes(context.Background(), workspaceDir, cfg, RelayStartupRouteOptions{
		In:          strings.NewReader("y\n"),
		Out:         &output,
		LapsEnabled: true,
	})
	if err != nil {
		t.Fatalf("ValidateRelayStartupRoutes() error = %v", err)
	}
	if len(validRoutes) != 1 || validRoutes["default"][0] != "cc" {
		t.Fatalf("validRoutes = %#v, want only default route", validRoutes)
	}
	if !strings.Contains(output.String(), `warning: route "BROKEN" is invalid and will be ignored`) {
		t.Fatalf("output = %q, want invalid-route warning", output.String())
	}
	if !strings.Contains(output.String(), continueRoutesPrompt) {
		t.Fatalf("output = %q, want continue prompt", output.String())
	}
}

func TestValidateRelayStartupRoutes_PartialFailurePromptEOFExits(t *testing.T) {
	workspaceDir := t.TempDir()
	cfg := config.V2Config{
		Routes: map[string][]string{
			"default": {"cc"},
			"BROKEN":  {"claude:opus:4.7"},
		},
	}

	withStartupHeadPull(t, func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{Title: "assigned"}, nil
	})

	var output bytes.Buffer
	_, err := ValidateRelayStartupRoutes(context.Background(), workspaceDir, cfg, RelayStartupRouteOptions{
		In:          strings.NewReader(""),
		Out:         &output,
		LapsEnabled: true,
	})
	if err == nil {
		t.Fatal("ValidateRelayStartupRoutes() error = nil, want EOF abort")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("error = %q, want aborted error", err.Error())
	}
	if !strings.Contains(output.String(), continueRoutesPrompt) {
		t.Fatalf("output = %q, want continue prompt", output.String())
	}
}

func TestValidateRelayStartupRoutes_MissingDefaultWithQueuePrompts(t *testing.T) {
	workspaceDir := t.TempDir()
	cfg := config.V2Config{
		Routes: map[string][]string{
			"SENIOR": {"cc"},
		},
	}

	withStartupHeadPull(t, func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{Title: "assigned"}, nil
	})

	var output bytes.Buffer
	validRoutes, err := ValidateRelayStartupRoutes(context.Background(), workspaceDir, cfg, RelayStartupRouteOptions{
		In:          strings.NewReader("yes\n"),
		Out:         &output,
		LapsEnabled: true,
	})
	if err != nil {
		t.Fatalf("ValidateRelayStartupRoutes() error = %v", err)
	}
	if len(validRoutes) != 1 || validRoutes["SENIOR"][0] != "cc" {
		t.Fatalf("validRoutes = %#v, want SENIOR route preserved", validRoutes)
	}
	if !strings.Contains(output.String(), "warning: no valid default route is configured") {
		t.Fatalf("output = %q, want missing-default warning", output.String())
	}
	if !strings.Contains(output.String(), continueRoutesPrompt) {
		t.Fatalf("output = %q, want continue prompt", output.String())
	}
}

func TestValidateRelayStartupRoutes_MissingDefaultWithEmptyQueueWarnsAndExits(t *testing.T) {
	workspaceDir := t.TempDir()
	cfg := config.V2Config{
		Routes: map[string][]string{
			"SENIOR": {"cc"},
		},
	}

	var output bytes.Buffer
	_, err := ValidateRelayStartupRoutes(context.Background(), workspaceDir, cfg, RelayStartupRouteOptions{
		Out:         &output,
		LapsEnabled: false,
	})
	if err == nil {
		t.Fatal("ValidateRelayStartupRoutes() error = nil, want empty-queue failure")
	}
	if !strings.Contains(err.Error(), "no laps are available") {
		t.Fatalf("error = %q, want empty-queue failure", err.Error())
	}
	if !strings.Contains(output.String(), "warning: no valid default route is configured") {
		t.Fatalf("output = %q, want missing-default warning", output.String())
	}
	if strings.Contains(output.String(), continueRoutesPrompt) {
		t.Fatalf("output = %q, want no prompt on empty queue", output.String())
	}
}

func TestValidateRelayStartupRoutes_RemovedGeminiAliasesWarnWithoutPrompt(t *testing.T) {
	workspaceDir := t.TempDir()
	cfg := config.V2Config{
		Routes: map[string][]string{
			"ALPHA":   {"ge:pro"},
			"UI":      {"gemini:flash"},
			"default": {"ge:flash"},
		},
	}

	var output bytes.Buffer
	validRoutes, err := ValidateRelayStartupRoutes(context.Background(), workspaceDir, cfg, RelayStartupRouteOptions{
		In:          strings.NewReader(""),
		Out:         &output,
		LapsEnabled: false,
	})
	if err != nil {
		t.Fatalf("ValidateRelayStartupRoutes() error = %v, want nil for removed gemini aliases", err)
	}
	if len(validRoutes) != 0 {
		t.Fatalf("validRoutes = %#v, want all removed-alias routes to be ignored", validRoutes)
	}

	got := output.String()
	if strings.Count(got, `removed gemini alias "ge"`) != 1 {
		t.Fatalf("output = %q, want exactly one ge warning", got)
	}
	if strings.Count(got, `removed gemini alias "gemini"`) != 1 {
		t.Fatalf("output = %q, want exactly one gemini warning", got)
	}
	if !strings.Contains(got, `route "ALPHA" entry "ge:pro" uses removed gemini alias "ge"; replace it with antigravity (ag)`) {
		t.Fatalf("output = %q, want route/entry/alias guidance for ge", got)
	}
	if !strings.Contains(got, `route "UI" entry "gemini:flash" uses removed gemini alias "gemini"; replace it with antigravity (ag)`) {
		t.Fatalf("output = %q, want route/entry/alias guidance for gemini", got)
	}
	if strings.Contains(got, continueRoutesPrompt) {
		t.Fatalf("output = %q, want no continue prompt for removed gemini aliases", got)
	}
	if strings.Contains(got, `is invalid and will be ignored`) {
		t.Fatalf("output = %q, want removed aliases to warn without generic invalid-route noise", got)
	}
	if strings.Contains(got, `no valid default route is configured`) {
		t.Fatalf("output = %q, want removed aliases to avoid the missing-default startup block", got)
	}
}
