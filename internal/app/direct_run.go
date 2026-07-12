package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent_prompt"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/roleinstructions"
	"github.com/mitchell-wallace/rally/internal/roles"
	"github.com/mitchell-wallace/rally/internal/routing"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

const defaultDirectRole = "intern"

// DirectRunOptions configures one standalone driver invocation. Unlike a relay,
// a direct run does not create a store, relay record, active-try marker, or laps
// state. Its only durable workflow result is OutputPath when one is supplied.
type DirectRunOptions struct {
	WorkspaceDir string
	InputPath    string
	Params       []string
	Role         string
	OutputPath   string
	Config       config.V2Config
	DataDir      string
	Telemetry    TelemetryBuild
	Out          io.Writer
	Err          io.Writer
}

type directRunDeps struct {
	executors map[string]harnessapi.Executor
	sink      telemetry.Sink
}

// RunDirect routes and executes a single path-backed task without relay or
// queue bookkeeping.
func RunDirect(ctx context.Context, opts DirectRunOptions) error {
	telemetryResult := telemetry.InitWithIdentity(telemetryConfigForRelay(opts.Config, opts.DataDir, opts.Telemetry))
	defer telemetryResult.Cleanup()
	return runDirect(ctx, opts, directRunDeps{
		executors: BuildExecutors(opts.Config),
		sink:      telemetryResult.Sink,
	})
}

func runDirect(ctx context.Context, opts DirectRunOptions, deps directRunDeps) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Err == nil {
		opts.Err = os.Stderr
	}
	if deps.sink == nil {
		deps.sink = telemetry.NoopSink{}
	}

	role := strings.ToLower(strings.TrimSpace(opts.Role))
	if role == "" {
		role = defaultDirectRole
	}
	inputPath, inputKind, err := resolveDirectInput(opts.InputPath)
	if err != nil {
		return err
	}
	candidates, routeName, err := resolveDirectCandidates(opts.Config, role)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return fmt.Errorf("rally run: route %q has no enabled runners", routeName)
	}

	roleInstructions, err := loadDirectRoleInstructions(opts.WorkspaceDir, role)
	if err != nil {
		return fmt.Errorf("rally run: load role instructions: %w", err)
	}
	projectInstructions := readDirectProjectInstructions(opts.WorkspaceDir)
	taskPrompt, err := directTaskPrompt(inputPath, inputKind, opts.Params)
	if err != nil {
		return fmt.Errorf("rally run: encode parameters: %w", err)
	}

	runCtx := ctx
	cancelRun := func() {}
	if timeout := opts.Config.Reliability.RunTimeout(); timeout > 0 {
		runCtx, cancelRun = context.WithTimeout(ctx, timeout)
	}
	defer cancelRun()

	runCtx, runSpan := deps.sink.StartSpan(runCtx, "run", "direct-run")
	runSpan.SetTag("mode", "direct")
	runSpan.SetTag("role", role)
	runSpan.SetTag("route", routeName)
	defer runSpan.Finish()

	maxAttempts := opts.Config.Reliability.RetryBudget
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	candidateIndex := 0
	resumeSessionID := ""
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := runCtx.Err(); err != nil {
			return fmt.Errorf("rally run: %w", err)
		}
		picked := candidates[candidateIndex]
		exec := deps.executors[picked.Harness]
		if exec == nil {
			return fmt.Errorf("rally run: no executor registered for harness %q", picked.Harness)
		}

		tryCtx := runCtx
		cancelTry := func() {}
		if timeout := opts.Config.Reliability.TryTimeout(); timeout > 0 {
			tryCtx, cancelTry = context.WithTimeout(runCtx, timeout)
		}
		tryCtx, trySpan := deps.sink.StartSpan(tryCtx, "try", fmt.Sprintf("direct-run-try-%d", attempt))
		trySpan.SetTag("mode", "direct")
		trySpan.SetTag("role", role)
		trySpan.SetTag("route", routeName)
		trySpan.SetTag("harness", picked.Harness)
		trySpan.SetTag("model", picked.Model)

		logFile, err := os.CreateTemp("", "rally-direct-try-*.log")
		if err != nil {
			cancelTry()
			trySpan.Finish()
			return fmt.Errorf("rally run: create try log: %w", err)
		}
		logPath := logFile.Name()
		_ = logFile.Close()

		startedAt := time.Now()
		result, execErr := exec.Execute(tryCtx, harnessapi.RunOptions{
			Persona:            picked.Harness,
			Model:              picked.Model,
			ReasoningEffort:    picked.ReasoningEffort,
			Role:               role,
			RoleWritePolicy:    directRoleWritePolicy(role),
			RoleRequiredSkills: directRoleRequiredSkills(role),
			TaskName:           "direct run: " + filepath.Base(inputPath),
			TaskRequirements:   "Workflow input: " + inputPath,
			Instructions:       projectInstructions,
			RoleInstructions:   roleInstructions,
			TaskPrompt:         taskPrompt,
			DirectRun:          true,
			LogPath:            logPath,
			ResumeSessionID:    resumeSessionID,
			WorkspaceDir:       opts.WorkspaceDir,
		})
		runtime := time.Since(startedAt)
		cancelTry()

		if execErr == nil && result != nil && result.Completed {
			resolvedModel := picked.Model
			if result.ResolvedModel != "" {
				resolvedModel = result.ResolvedModel
			}
			deps.sink.EmitTryLog(tryCtx, directTryFields(role, routeName, picked.Harness, resolvedModel, attempt, runtime, "completed", ""))
			trySpan.SetTag("outcome", "completed")
			trySpan.Finish()
			_ = os.Remove(logPath)
			return writeDirectOutput(opts.Out, opts.OutputPath, result.Summary)
		}

		logLines := readDirectLogLines(logPath)
		_ = os.Remove(logPath)
		var evidence *reliability.FailureEvidence
		if result != nil {
			evidence = result.Evidence
		}
		decision := reliability.ClassifyError(logLines, picked.Harness, nil, evidence)
		outcome := string(reliability.OutcomeFailed)
		if errors.Is(tryCtx.Err(), context.DeadlineExceeded) {
			outcome = string(reliability.OutcomeRunTimeout)
		}
		deps.sink.EmitTryLog(tryCtx, directTryFields(role, routeName, picked.Harness, picked.Model, attempt, runtime, outcome, string(decision.Category)))
		trySpan.SetTag("outcome", outcome)
		trySpan.SetTag("failure_category", string(decision.Category))
		trySpan.Finish()

		lastErr = directAttemptError(attempt, maxAttempts, picked, result, execErr, decision)
		if attempt == maxAttempts {
			break
		}

		resumeSessionID = ""
		if result != nil && result.SessionID != "" && exec.ResumeSupported() &&
			(decision.Strategy == reliability.StrategyResume || decision.Strategy == reliability.StrategyWaitResume) {
			resumeSessionID = result.SessionID
		}
		if decision.Strategy == reliability.StrategyRotate && candidateIndex+1 < len(candidates) {
			candidateIndex++
			resumeSessionID = ""
		}
		fmt.Fprintf(opts.Err, "rally run: %v; retrying\n", lastErr)
		if decision.Strategy == reliability.StrategyWaitResume && decision.Cooldown > 0 {
			if err := waitDirectCooldown(runCtx, decision.Cooldown); err != nil {
				return fmt.Errorf("rally run: %w", err)
			}
		}
	}

	deps.sink.CaptureFailure(runCtx, lastErr.Error(), telemetry.FailureEvent{
		Tags: map[string]string{
			"mode":  "direct",
			"role":  role,
			"route": routeName,
		},
		Fingerprint: []string{"direct_run", role, routeName},
	})
	return fmt.Errorf("rally run failed after %d attempt(s): %w", maxAttempts, lastErr)
}

func resolveDirectInput(path string) (string, string, error) {
	if strings.TrimSpace(path) == "" {
		return "", "", fmt.Errorf("rally run: input path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("rally run: resolve input path: %w", err)
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", fmt.Errorf("rally run: inspect input %q: %w", abs, err)
	}
	if info.Mode().IsRegular() {
		return abs, "file", nil
	}
	if info.IsDir() {
		return abs, "directory", nil
	}
	return "", "", fmt.Errorf("rally run: input %q must be a regular file or directory", abs)
}

func resolveDirectCandidates(cfg config.V2Config, role string) ([]harnessapi.ResolvedAgent, string, error) {
	if err := config.ValidateRoutesTable(cfg.Routes); err != nil {
		return nil, "", err
	}
	selector, err := routing.NewSelector(cfg.Routes, false)
	if err != nil {
		return nil, "", err
	}
	route, err := selector.ActiveRoute(routing.Lap{Assignee: role}, nil)
	if err != nil {
		// An initialized workspace may rely on Rally's built-in role catalog
		// without declaring [routes] explicitly. Use that role's normal default
		// driver in this one-shot mode; custom roles still require a configured
		// role or default route.
		spec, builtIn := roles.Lookup(role)
		if !builtIn || len(spec.DefaultRoute) == 0 {
			return nil, "", fmt.Errorf("rally run: select role %q: %w", role, err)
		}
		route, err = routing.ParseRoute(role, spec.DefaultRoute)
		if err != nil {
			return nil, "", fmt.Errorf("rally run: built-in route %q: %w", role, err)
		}
	}
	providerIndex, err := cfg.BuildProviderIndex()
	if err != nil {
		return nil, "", fmt.Errorf("rally run: resolve providers: %w", err)
	}
	candidates := make([]harnessapi.ResolvedAgent, 0, len(route.Entries))
	for _, entry := range route.Entries {
		picked, err := cfg.ResolveAgent(entry.Spec)
		if err != nil {
			return nil, "", fmt.Errorf("rally run: route %q: %w", route.Name, err)
		}
		picked, err = routing.ApplyRoleReasoningFallback(picked, entry, role, cfg.Reasoning, cfg.ResolveRoleReasoning)
		if err != nil {
			return nil, "", fmt.Errorf("rally run: role reasoning: %w", err)
		}
		if providerIndex.Disabled(picked.Harness, picked.Model) {
			continue
		}
		candidates = append(candidates, picked)
	}
	return candidates, route.Name, nil
}

func loadDirectRoleInstructions(workspaceDir, role string) (string, error) {
	onDisk, err := (roleinstructions.Loader{WorkspaceDir: workspaceDir}).Load(role)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(onDisk) != "" {
		return onDisk, nil
	}
	embedded, _ := agent_prompt.Role(role)
	return embedded, nil
}

func readDirectProjectInstructions(workspaceDir string) string {
	data, err := os.ReadFile(filepath.Join(store.RallyDir(workspaceDir), "instructions.md"))
	if err != nil {
		return ""
	}
	return string(data)
}

func directTaskPrompt(inputPath, inputKind string, params []string) (string, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	var convention string
	if inputKind == "directory" {
		convention = "The input is a directory. Inspect its tree directly and read the files relevant to the requested work; Rally does not concatenate or embed the directory contents."
	} else {
		convention = "The input is a file. Read it directly and treat its contents as the primary workflow context."
	}
	return fmt.Sprintf("Workflow input: `%s`\n\n%s\n\nOrdered parameters (JSON array; supplied after `--`):\n```json\n%s\n```\n\nUse the parameters exactly as workflow inputs and return the requested deliverable in your final response.", inputPath, convention, encoded), nil
}

func directRoleWritePolicy(role string) harnessapi.RoleWritePolicy {
	spec, _ := roles.Lookup(role)
	switch spec.WritePolicy {
	case roles.PolicyPlanOnly:
		return harnessapi.RolePolicyPlanOnly
	case roles.PolicyReadOnlyGate:
		return harnessapi.RolePolicyReadOnlyGate
	case roles.PolicyReconcile:
		return harnessapi.RolePolicyReconcile
	default:
		return harnessapi.RolePolicyImplementation
	}
}

func directRoleRequiredSkills(role string) []string {
	spec, _ := roles.Lookup(role)
	return append([]string(nil), spec.RequiredSkills...)
}

func directTryFields(role, route, harness, model string, attempt int, runtime time.Duration, outcome, category string) map[string]interface{} {
	return map[string]interface{}{
		"mode":             "direct",
		"role":             role,
		"route":            route,
		"harness":          harness,
		"model":            model,
		"attempt":          attempt,
		"runtime_ms":       runtime.Milliseconds(),
		"outcome":          outcome,
		"failure_category": category,
	}
}

func directAttemptError(attempt, maxAttempts int, picked harnessapi.ResolvedAgent, result *harnessapi.TryResult, execErr error, decision reliability.StrategyDecision) error {
	runner := picked.Harness
	if picked.Model != "" {
		runner += ":" + picked.Model
	}
	detail := decision.DisplayLabel
	if detail == "" {
		detail = "agent did not complete"
	}
	if execErr != nil {
		detail = execErr.Error()
	} else if result != nil && strings.TrimSpace(result.Summary) != "" {
		detail += ": " + strings.TrimSpace(result.Summary)
	}
	return fmt.Errorf("attempt %d/%d via %s failed: %s", attempt, maxAttempts, runner, detail)
}

func readDirectLogLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 50 {
		lines = lines[len(lines)-50:]
	}
	return lines
}

func waitDirectCooldown(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func writeDirectOutput(out io.Writer, outputPath, value string) error {
	data := []byte(strings.TrimRight(value, "\n") + "\n")
	if outputPath == "" {
		_, err := out.Write(data)
		return err
	}
	abs, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("rally run: resolve output path: %w", err)
	}
	if info, err := os.Stat(abs); err == nil && info.IsDir() {
		return fmt.Errorf("rally run: output %q is a directory; --output requires a file path", abs)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rally run: inspect output path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("rally run: create output directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".rally-run-output-*")
	if err != nil {
		return fmt.Errorf("rally run: create output: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("rally run: write output: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("rally run: close output: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("rally run: chmod output: %w", err)
	}
	if err := os.Rename(tmpName, abs); err != nil {
		return fmt.Errorf("rally run: publish output: %w", err)
	}
	return nil
}
