package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/relay/runner"
	"github.com/mitchell-wallace/rally/internal/routing"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

type RelayStartOptions struct {
	WorkspaceDir  string
	Config        config.V2Config
	TaskPrompt    string
	AgentMixSpecs []string
	UsedOverride  bool
	TargetIters   int
	LapsEnabled   bool
	DataDir       string
	Telemetry     TelemetryBuild

	DiscardUnfinishedRelay bool
	ResetAgentStatus       bool
	OverwriteMixOnResume   bool

	Out io.Writer
	Err io.Writer
}

type TelemetryBuild struct {
	DefaultNewRelicLicenseKey      string
	DefaultNewRelicAppName         string
	DefaultNewRelicHostDisplayName string
}

type ResumeInfo struct {
	HasUnfinished       bool
	RelayID             int
	CompletedIterations int
	TargetIterations    int
	AgentMix            string
}

func InspectResume(workspaceDir string) (ResumeInfo, error) {
	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		return ResumeInfo{}, fmt.Errorf("load store: %w", err)
	}

	r, found, err := relay.ResumeRelay(s)
	if err != nil {
		return ResumeInfo{}, err
	}
	if !found {
		return ResumeInfo{}, nil
	}

	return ResumeInfo{
		HasUnfinished:       true,
		RelayID:             r.ID,
		CompletedIterations: r.CompletedIterations,
		TargetIterations:    r.TargetIterations,
		AgentMix:            r.AgentMix,
	}, nil
}

func StartRelay(ctx context.Context, opts RelayStartOptions) error {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}

	rallyDir := store.RallyDir(opts.WorkspaceDir)
	if _, err := os.Stat(rallyDir); os.IsNotExist(err) {
		return fmt.Errorf("rally not initialized; run `rally init` first")
	}

	executors := BuildExecutors(opts.Config)

	providerIndex, err := opts.Config.BuildProviderIndex()
	if err != nil {
		return fmt.Errorf("resolve providers: %w", err)
	}

	runnerCfg := runner.Config{
		WorkspaceDir:           opts.WorkspaceDir,
		DataDir:                opts.DataDir,
		AgentMixSpecs:          opts.AgentMixSpecs,
		RouteSpecs:             opts.Config.Routes,
		Reasoning:              opts.Config.Reasoning,
		ReasoningResolver:      opts.Config.ResolveRoleReasoning,
		Providers:              providerIndex,
		UseOverrideRoute:       opts.UsedOverride,
		TargetIterations:       opts.TargetIters,
		StallThreshold:         opts.Config.Reliability.StallThreshold(),
		LivenessProbe:          opts.Config.Reliability.LivenessProbe,
		RetryBudget:            opts.Config.Reliability.RetryBudget,
		RunTimeout:             opts.Config.Reliability.RunTimeout(),
		TryTimeout:             opts.Config.Reliability.TryTimeout(),
		HandoffTimeout:         opts.Config.Reliability.HandoffTimeout(),
		RunHooksOnAutoCommit:   opts.Config.RunHooksOnAutoCommit,
		LapsEnabled:            opts.LapsEnabled,
		LapsInstructionsFile:   opts.Config.Laps.InstructionsFile,
		FreeRunPromptFile:      opts.Config.FreeRun.PromptFile,
		RecentTryCount:         opts.Config.Reliability.RecentTryCount,
		RecentTryCharLimit:     opts.Config.Reliability.RecentTryCharLimit,
		RecentContextCharLimit: opts.Config.Reliability.RecentContextCharLimit,
		TaskPrompt:             opts.TaskPrompt,
		OverwriteMixOnResume:   opts.OverwriteMixOnResume,
	}

	runnerCfg.Resolver = func(spec string) (harnessapi.ResolvedAgent, error) {
		ra, err := opts.Config.ResolveAgent(spec)
		if err != nil {
			return harnessapi.ResolvedAgent{}, err
		}
		return ra, nil
	}

	if opts.UsedOverride {
		if _, err := routing.BuildOverrideRoute("override", opts.AgentMixSpecs, opts.Config.Routes, routing.AgentResolver(runnerCfg.Resolver)); err != nil {
			return err
		}
	}

	instructionsPath := filepath.Join(rallyDir, "instructions.md")
	if data, err := os.ReadFile(instructionsPath); err == nil {
		runnerCfg.Instructions = string(data)
	}

	s, err := store.NewStore(rallyDir)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}

	if opts.DiscardUnfinishedRelay {
		relays := s.RecentRelays(1)
		if len(relays) > 0 && relays[0].EndedAt == "" {
			_ = relay.CompleteRelay(s, relays[0].ID)
		}
	}
	if opts.ResetAgentStatus {
		if err := s.ResetAgentStatus(); err != nil {
			return fmt.Errorf("reset agent status: %w", err)
		}
	}

	// Init telemetry only for relay execution. Mechanical commands like help,
	// version, and update never reach this path, so baked release credentials
	// cannot create machine-id files or open a telemetry client for those
	// commands.
	telemetryResult := telemetry.InitWithIdentity(telemetryConfigForRelay(opts.Config, opts.DataDir, opts.Telemetry))
	defer telemetryResult.Cleanup()
	runnerCfg.MachineID = telemetryResult.MachineID

	r := runner.NewRunner(s, runnerCfg, executors)
	r.SetTelemetry(telemetryResult.Sink)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	// Silent double-press: ignore a single Ctrl+C (accidental touches
	// shouldn't kill a long-running relay), fire on the second within the
	// confirm window.
	const doublePressWindow = 3 * time.Second
	go func() {
		var lastPress time.Time
		for {
			select {
			case <-sigCh:
				now := time.Now()
				if !lastPress.IsZero() && now.Sub(lastPress) <= doublePressWindow {
					r.RequestStop()
					cancel()
					return
				}
				lastPress = now
			case <-runCtx.Done():
				return
			}
		}
	}()

	if err := r.Run(runCtx); err != nil {
		return err
	}
	fmt.Fprintln(out, "Relay complete.")
	return nil
}

func telemetryConfigForRelay(cfg config.V2Config, dataDir string, build TelemetryBuild) telemetry.Config {
	appName := cfg.Telemetry.NewRelicAppName
	if appName == "" {
		appName = build.DefaultNewRelicAppName
	}
	hostDisplayName := cfg.Telemetry.NewRelicHostDisplayName
	if hostDisplayName == "" {
		hostDisplayName = build.DefaultNewRelicHostDisplayName
	}
	trueValue := true
	falseValue := false
	return telemetry.Config{
		Enabled:                                  cfg.Telemetry.Enabled,
		DefaultNewRelicLicenseKey:                build.DefaultNewRelicLicenseKey,
		NewRelicAppName:                          appName,
		NewRelicHostDisplayName:                  hostDisplayName,
		NewRelicAppLogEnabled:                    &trueValue,
		NewRelicAppLogForwardingEnabled:          &trueValue,
		NewRelicAppLogMetricsEnabled:             &trueValue,
		NewRelicAppLogDecoratingEnabled:          &falseValue,
		NewRelicAppLogForwardingMaxSamplesStored: 1000,
		DataDir:                                  dataDir,
	}
}
