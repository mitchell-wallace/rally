package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/routing"
	"github.com/mitchell-wallace/rally/internal/user_prompt"
)

// continueRoutesPrompt is the substring tests assert against; it must appear
// in both the TTY (huh) and plain-text fallback renderings of the confirm.
const continueRoutesPrompt = "Continue anyway?"

var headPullForStartupValidation = func(ctx context.Context, workspaceDir string) (laps.Lap, error) {
	return (&laps.Adapter{WorkspaceDir: workspaceDir}).HeadPull(ctx)
}

type RelayStartupRouteOptions struct {
	In          io.Reader
	Out         io.Writer
	LapsEnabled bool
}

type startupRouteIssue struct {
	RouteName string
	Err       error
}

func ValidateRelayStartupRoutes(ctx context.Context, workspaceDir string, cfg config.V2Config, opts RelayStartupRouteOptions) (map[string][]string, error) {
	if err := config.ValidateRoutesTable(cfg.Routes); err != nil {
		return nil, err
	}

	validRoutes, issues, validDefault, err := collectValidRoutes(cfg)
	if err != nil {
		return nil, err
	}

	if len(validRoutes) == 0 && len(issues) > 0 {
		return nil, issues[0].Err
	}

	warnings := make([]string, 0, len(issues)+1)
	needsPrompt := false
	warningsWritten := false
	removedAliasWarnings := map[string]bool{}

	for _, issue := range issues {
		var removed *removedAliasRouteError
		if errors.As(issue.Err, &removed) && removed.alias != "" && !removedAliasWarnings[removed.alias] {
			warnings = append(warnings, "warning: "+removed.warning)
			removedAliasWarnings[removed.alias] = true
		}
		warnings = append(warnings, fmt.Sprintf("warning: route %q is invalid and will be ignored: %v", issue.RouteName, issue.Err))
		needsPrompt = true
	}

	if len(cfg.Routes) > 0 && !validDefault {
		warnings = append(warnings, "warning: no valid default route is configured; laps without a matching assignee will fail at run-time")

		queueEmpty, err := relayQueueEmpty(ctx, workspaceDir, opts.LapsEnabled)
		if err != nil {
			return nil, fmt.Errorf("inspect relay queue: %w", err)
		}
		writeWarnings(opts.Out, warnings)
		warningsWritten = true
		if queueEmpty {
			return nil, fmt.Errorf("route startup validation failed: no valid default route is configured and no laps are available")
		}
		needsPrompt = true
	}

	if !needsPrompt {
		return validRoutes, nil
	}

	if !warningsWritten {
		writeWarnings(opts.Out, warnings)
	}
	if !promptContinueRoutes(opts.In, opts.Out) {
		return nil, fmt.Errorf("route startup validation aborted")
	}

	return validRoutes, nil
}

func collectValidRoutes(cfg config.V2Config) (map[string][]string, []startupRouteIssue, bool, error) {
	names := sortedRouteNames(cfg.Routes)
	validRoutes := make(map[string][]string, len(cfg.Routes))
	issues := make([]startupRouteIssue, 0)
	validDefault := false

	for _, name := range names {
		route, err := routing.ParseRoute(name, cfg.Routes[name])
		if err != nil {
			issues = append(issues, startupRouteIssue{
				RouteName: name,
				Err:       fmt.Errorf("routes check: %w", err),
			})
			continue
		}

		routeErr := error(nil)
		for _, entry := range route.Entries {
			if err := validateRouteEntry(cfg, name, entry); err != nil {
				routeErr = err
				break
			}
		}
		if routeErr != nil {
			issues = append(issues, startupRouteIssue{
				RouteName: name,
				Err:       routeErr,
			})
			continue
		}

		validRoutes[name] = append([]string(nil), cfg.Routes[name]...)
		if strings.EqualFold(name, defaultRouteKey) {
			validDefault = true
		}
	}

	return validRoutes, issues, validDefault, nil
}

func relayQueueEmpty(ctx context.Context, workspaceDir string, lapsEnabled bool) (bool, error) {
	if !lapsEnabled {
		return true, nil
	}

	lap, err := headPullForStartupValidation(ctx, workspaceDir)
	if err != nil {
		return false, err
	}
	return lap == laps.NoLap, nil
}

func promptContinueRoutes(in io.Reader, out io.Writer) bool {
	if out == nil {
		out = io.Discard
	}
	ok, err := user_prompt.Confirm(in, out,
		"Continue anyway?",
		"Invalid roles will fall back to DEFAULT.",
		false,
	)
	if err != nil {
		return false
	}
	return ok
}

func writeWarnings(out io.Writer, warnings []string) {
	if out == nil {
		return
	}
	for _, warning := range warnings {
		fmt.Fprintln(out, warning)
	}
}
