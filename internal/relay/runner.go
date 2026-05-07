package relay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/keyboard"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/monitor"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/prompt/roleloader"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/style"
)

type Config struct {
	WorkspaceDir             string
	DataDir                  string
	AgentMixSpecs            []string
	RouteSpecs               map[string][]string
	UseOverrideRoute         bool
	TargetIterations         int
	FreezeThreshold          time.Duration
	LivenessProbe            bool
	CharsPerToken            map[string]float64
	RunHooksOnAutoCommit     bool
	LapsEnabled              bool
	Instructions             string
	TaskPrompt               string
	OverwriteMixOnResume     bool
	Resolver                 Resolver
	LapsInstructionsFile     string
	FallbackInstructionsFile string
}

type Runner struct {
	store      *store.Store
	cfg        Config
	executors  map[string]agent.Executor
	stopFlag   atomic.Bool
	skipFlag   atomic.Bool
	log        io.WriteCloser
	resilience *Resilience
	relayStart time.Time

	lapsInstructionsCache     string
	lapsWarned                bool
	fallbackInstructionsCache string
	fallbackWarned            bool

	freezeControllerFactory func(logPath string) reliability.FreezeController
}

var headPullLap = func(ctx context.Context, workspaceDir string) (laps.Lap, error) {
	return (&laps.Adapter{WorkspaceDir: workspaceDir}).HeadPull(ctx)
}

func NewRunner(s *store.Store, cfg Config, executors map[string]agent.Executor) *Runner {
	return &Runner{
		store:     s,
		cfg:       cfg,
		executors: executors,
	}
}

var freezeCheckInterval = monitor.TickInterval

const builtInDefaultFallback = "Continue the relay run. Review the current state of the codebase and continue making progress on the project."

type runTask struct {
	Name         string
	Requirements string
	Prompt       string
	Assignee     string
}

func (r *Runner) resolveInstructions() string {
	if !r.cfg.LapsEnabled {
		return r.cfg.Instructions
	}
	if r.cfg.LapsInstructionsFile == "" {
		return r.cfg.Instructions
	}
	if r.lapsInstructionsCache != "" {
		return r.lapsInstructionsCache
	}
	data, err := os.ReadFile(r.cfg.LapsInstructionsFile)
	if err != nil {
		if !r.lapsWarned {
			fmt.Fprintf(os.Stderr, "warning: laps instructions file %q not readable: %v; using default\n", r.cfg.LapsInstructionsFile, err)
			r.lapsWarned = true
		}
		return r.cfg.Instructions
	}
	r.lapsInstructionsCache = string(data)
	return r.lapsInstructionsCache
}

func (r *Runner) loadFallbackInstructions() string {
	if r.fallbackInstructionsCache != "" {
		return r.fallbackInstructionsCache
	}
	if r.cfg.FallbackInstructionsFile != "" {
		data, err := os.ReadFile(r.cfg.FallbackInstructionsFile)
		if err != nil {
			if !r.fallbackWarned {
				fmt.Fprintf(os.Stderr, "warning: fallback instructions file %q not readable: %v; using built-in default\n", r.cfg.FallbackInstructionsFile, err)
				r.fallbackWarned = true
			}
			return builtInDefaultFallback
		}
		r.fallbackInstructionsCache = string(data)
		return r.fallbackInstructionsCache
	}
	return builtInDefaultFallback
}

func (r *Runner) RequestStop() {
	r.stopFlag.Store(true)
}

func (r *Runner) Run(ctx context.Context) error {
	// Clear any stale run-state from a previous interrupted relay.
	_ = r.maybeWriteStubAndClearState("")

	relay, resumed, err := ResumeRelay(r.store)
	if err != nil {
		return err
	}

	routeRuntime := (*routeRuntime)(nil)
	selectionLabel := ""
	if resumed {
		if r.cfg.OverwriteMixOnResume {
			routeRuntime, selectionLabel, err = newRouteRuntimeFromConfig(r.cfg)
			if err != nil {
				return err
			}
			relay.AgentMix = selectionLabel
			if err := r.store.UpdateRelay(*relay); err != nil {
				return err
			}
		} else {
			routeRuntime, selectionLabel, err = newRouteRuntimeFromStoredLabel(r.cfg, relay.AgentMix)
			if err != nil {
				return err
			}
		}
	} else {
		routeRuntime, selectionLabel, err = newRouteRuntimeFromConfig(r.cfg)
		if err != nil {
			return err
		}
		relay, err = CreateRelay(r.store, r.cfg.TargetIterations, selectionLabel)
		if err != nil {
			return err
		}
	}

	log, err := openRelayLog(r.cfg.DataDir, r.cfg.WorkspaceDir, relay.ID)
	if err != nil {
		return err
	}
	r.log = log
	defer func() {
		_ = PruneRepoRelayLogs(r.cfg.WorkspaceDir, 10)
		_ = log.Close()
	}()

	fmt.Fprintf(log, "relay %d started (target %d iterations, mix: %s)\n", relay.ID, relay.TargetIterations, relay.AgentMix)
	r.relayStart = time.Now()

	resilience := r.resilience
	if resilience == nil {
		resilience = NewResilience(r.store)
	}

	// Consume oldest eligible relay-scoped message at relay start
	var relayMsg *store.MessageRecord
	relayPending := r.store.EligibleRelayScopedMessages(relay.ID)
	if len(relayPending) > 0 {
		msg := relayPending[0]
		// Record consumption at consume time (Task 6)
		if msg.ConsumedByRelayID == nil {
			msg.ConsumedByRelayID = &relay.ID
			if err := r.store.UpdateMessage(msg); err != nil {
				return err
			}
			// Append to ConsumedMessageIDs immediately
			relay.ConsumedMessageIDs = append(relay.ConsumedMessageIDs, msg.ID)
			if err := r.store.UpdateRelay(*relay); err != nil {
				return err
			}
		}
		relayMsg = &msg
	}

	runIndex := relay.CompletedIterations
	for relay.CompletedIterations < relay.TargetIterations {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.stopFlag.Load() {
			fmt.Fprintf(log, "relay %d stop requested, halting after current try\n", relay.ID)
			break
		}

		task, err := r.resolveRunTask(ctx)
		if err != nil {
			return err
		}

		selection, err := routeRuntime.next(task, resilience)
		if err != nil {
			var routeErr *routeSelectionError
			if errors.As(err, &routeErr) {
				if routeErr.AllFrozen {
					fmt.Fprintf(log, "relay %d failed: all agents frozen\n", relay.ID)
					_ = CompleteRelay(r.store, relay.ID)
					return fmt.Errorf("relay failed: all agents frozen")
				}
				fmt.Fprintf(log, "relay %d all agents paused, waiting %v\n", relay.ID, routeErr.Wait)
				select {
				case <-time.After(routeErr.Wait):
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return err
		}
		if selection.Route.Warning != "" {
			fmt.Fprintln(os.Stderr, selection.Route.Warning)
			fmt.Fprintln(log, selection.Route.Warning)
		}
		r.prepareExecutorForSelection(relay.ID, runIndex, selection, log)

		// Consume run-scoped message at start of each run
		// First check if there's an already-consumed message from a failed run
		runID := runIndex + 1
		var consumedMsg *store.MessageRecord
		if existingMsg := r.store.ConsumedRunScopedMessageForRun(runID); existingMsg != nil {
			// Reuse the message from the failed run
			consumedMsg = existingMsg
		} else {
			// Consume a new message
			pending := r.store.PendingMessages()
			for _, p := range pending {
				if p.Scope != "relay" && p.ConsumedByRunID == nil {
					msg := p
					msg.ConsumedByRunID = &runID
					if err := r.store.UpdateMessage(msg); err != nil {
						return err
					}
					consumedMsg = &msg
					break
				}
			}
		}

		success, addressed, interrupted, err := r.runOne(
			ctx,
			relay,
			runIndex,
			selection.Agent,
			task,
			consumedMsg,
			relayMsg,
			selection.HourlyRetry,
			func() {
				selection.Scheduler.OnAgentFailed(selection.Entry, "freeze")
			},
			func() {
				selection.Scheduler.OnAgentRecovered(selection.Entry)
			},
			log,
		)
		if err != nil {
			fmt.Fprintf(log, "relay %d run %d error: %v\n", relay.ID, runIndex+1, err)
			return err
		}
		if interrupted {
			fmt.Fprintf(log, "relay %d stop requested, halting\n", relay.ID)
			break
		}

		// If skipped, don't pause the agent — just advance rotation
		if r.skipFlag.Load() {
			r.skipFlag.Store(false)
			selection.Entry.Exhausted = true
			selection.Entry.Frozen = false
			runIndex++
			relay.LastTryID = r.store.NextTryID() - 1
			if relay.FirstTryID == 0 {
				relay.FirstTryID = relay.LastTryID
			}
			if err := r.store.UpdateRelay(*relay); err != nil {
				return err
			}
			continue
		}

		if selection.HourlyRetry {
			if success {
				if err := resilience.UnpauseAgent(selection.Agent.Harness, relay.ID); err != nil {
					return err
				}
			} else {
				selection.Scheduler.OnAgentFailed(selection.Entry, "retry-budget-exhausted")
				if err := resilience.RecordHourlyFailure(selection.Agent.Harness, relay.ID); err != nil {
					return err
				}
			}
		} else {
			if !success {
				selection.Scheduler.OnAgentFailed(selection.Entry, "retry-budget-exhausted")
				if err := resilience.PauseAgent(selection.Agent.Harness, relay.ID); err != nil {
					return err
				}
			}
		}

		if success {
			relay.CompletedIterations++
			runIndex++
			if consumedMsg != nil && addressed {
				consumedMsg.Status = "addressed"
				now := time.Now().UTC().Format(time.RFC3339)
				consumedMsg.UpdatedAt = now
				if err := r.store.UpdateMessage(*consumedMsg); err != nil {
					return err
				}
				// Add to ConsumedMessageIDs if not already present
				if !containsInt(relay.ConsumedMessageIDs, consumedMsg.ID) {
					relay.ConsumedMessageIDs = append(relay.ConsumedMessageIDs, consumedMsg.ID)
				}
			}
			if relayMsg != nil && addressed && relayMsg.Status == "pending" {
				relayMsg.Status = "addressed"
				now := time.Now().UTC().Format(time.RFC3339)
				relayMsg.UpdatedAt = now
				if err := r.store.UpdateMessage(*relayMsg); err != nil {
					return err
				}
				// Already added at consume time, but ensure no duplicates
				if !containsInt(relay.ConsumedMessageIDs, relayMsg.ID) {
					relay.ConsumedMessageIDs = append(relay.ConsumedMessageIDs, relayMsg.ID)
				}
			}
		} else {
			runIndex++
		}

		relay.LastTryID = r.store.NextTryID() - 1
		if relay.FirstTryID == 0 {
			relay.FirstTryID = relay.LastTryID
		}
		if err := r.store.UpdateRelay(*relay); err != nil {
			return err
		}
	}

	if relay.CompletedIterations >= relay.TargetIterations {
		if err := CompleteRelay(r.store, relay.ID); err != nil {
			return err
		}
		fmt.Fprintf(log, "relay %d completed\n", relay.ID)
	}

	// Print relay summary
	totalRuns := relay.CompletedIterations
	if totalRuns > 0 {
		passCount := 0
		failCount := 0
		for _, tr := range r.store.AllTries() {
			if tr.RelayID == relay.ID {
				if tr.Completed {
					passCount++
				} else {
					failCount++
				}
			}
		}
		totalDuration := time.Since(r.relayStart)
		summary := style.RenderSummary(totalRuns, passCount, failCount, totalDuration)
		fmt.Println(summary)
	}

	return nil
}

func (r *Runner) prepareExecutorForSelection(relayID, runIndex int, selection routeSelection, log io.Writer) {
	if selection.PreviousAgent == nil {
		return
	}
	if selection.PreviousAgent.Harness != selection.Agent.Harness {
		return
	}

	exec := r.executors[selection.Agent.Harness]
	if exec == nil || !exec.RotateSupported() {
		return
	}

	// Each Execute starts a fresh CLI process, so doing nothing here naturally
	// preserves the existing teardown/respawn fallback path. Rotation is only an
	// optimization when the adapter opts in and the swap succeeds.
	if err := exec.RotateModel(selection.Agent.Model); err != nil {
		fmt.Fprintf(log, "relay %d run %d rotate fallback for %s: %v\n", relayID, runIndex+1, selection.Agent.Harness, err)
	}
}

func (r *Runner) runOne(
	ctx context.Context,
	relay *store.RelayRecord,
	runIndex int,
	picked agent.ResolvedAgent,
	task runTask,
	consumedMsg *store.MessageRecord,
	relayMsg *store.MessageRecord,
	isHourlyRetry bool,
	onFreeze func(),
	onFreezeRecovered func(),
	log io.Writer,
) (bool, bool, bool, error) {
	// Initialize run-state for this run.
	runID := fmt.Sprintf("relay-%d-run-%d", relay.ID, runIndex+1)
	_ = progress.SaveRunState(r.cfg.WorkspaceDir, &progress.RunState{RunID: runID, HandoffState: 0, RecordedLaps: []string{}})

	inbox := ""
	if consumedMsg != nil {
		inbox = consumedMsg.Body
	}
	relayMessage := ""
	if relayMsg != nil {
		relayMessage = relayMsg.Body
	}

	recentTries := r.store.RecentTries(5)
	var recentContext strings.Builder
	for _, t := range recentTries {
		fmt.Fprintf(&recentContext, "Run %d (%s): completed=%v summary=%s\n", t.RunID, t.AgentType, t.Completed, t.Summary)
	}

	var previousSummary string
	var lastResult *agent.TryResult
	var sessionID string
	success := false
	freezeMarked := false

	roleInstructions, err := r.resolveRoleInstructions(task.Assignee)
	if err != nil {
		return false, false, false, err
	}

	exec := r.executors[picked.Harness]

	maxAttempts := 3
	if isHourlyRetry {
		maxAttempts = 1
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return false, false, false, ctx.Err()
		}
		if r.stopFlag.Load() {
			return false, false, true, nil
		}

		if attempt > 1 {
			if exec != nil && exec.ResumeSupported() && sessionID != "" {
				rs, rsErr := progress.LoadRunState(r.cfg.WorkspaceDir)
				if rsErr == nil {
					rs.SessionID = sessionID
					_ = progress.SaveRunState(r.cfg.WorkspaceDir, rs)
				}
			} else {
				sessionID = ""
				_ = progress.SaveRunState(r.cfg.WorkspaceDir, &progress.RunState{RunID: runID, HandoffState: 0, RecordedLaps: []string{}})
			}
		}

		opts := agent.RunOptions{
			Persona:          picked.Harness,
			Model:            picked.Model,
			TaskName:         task.Name,
			TaskRequirements: task.Requirements,
			TaskPrompt:       task.Prompt,
			Instructions:     r.resolveInstructions(),
			RoleInstructions: roleInstructions,
			InboxMessage:     inbox,
			RelayMessage:     relayMessage,
			PreviousSummary:  previousSummary,
			RecentTryContext: recentContext.String(),
			LapsEnabled:      r.cfg.LapsEnabled,
			ResumeSessionID:  sessionID,
		}
		prompt := agent.BuildPrompt(opts)

		taskPath := filepath.Join(r.cfg.WorkspaceDir, ".rally", "current_task.md")
		if err := os.WriteFile(taskPath, []byte(prompt), 0o644); err != nil {
			return false, false, false, fmt.Errorf("write current_task.md: %w", err)
		}

		tryLogPath := filepath.Join(r.cfg.DataDir, "tries", fmt.Sprintf("try-%d.log", r.store.NextTryID()))
		_ = os.MkdirAll(filepath.Dir(tryLogPath), 0o755)
		opts.LogPath = tryLogPath

		headBefore, _ := r.headHash()
		startedAt := time.Now().UTC()

		totalRuns := relay.TargetIterations
		header := style.RenderHeader(runIndex+1, totalRuns, picked.Harness, attempt, startedAt)
		fmt.Println(header)

		kb := keyboard.NewKeyboard(os.Stdin, os.Stdout)
		_ = kb.SetRawMode()
		kbCtx, kbCancel := context.WithCancel(ctx)
		actionCh := kb.Start(kbCtx)

		mon := monitor.NewMonitor(r.cfg.WorkspaceDir, tryLogPath, 0)
		freezeController := r.newFreezeController(tryLogPath, exec)

		// Wire reliability indicators into the monitor.
		freezeThreshold := r.cfg.FreezeThreshold
		if freezeThreshold <= 0 {
			freezeThreshold = reliability.DefaultFreezeThreshold
		}
		mon.SetFreezeThreshold(freezeThreshold)
		cpt := r.resolveCharsPerToken(picked.Harness, exec)
		mon.SetCharsPerToken(cpt)

		initialStatus, _ := mon.Tick()
		fmt.Println(initialStatus)
		fmt.Println("[Ctrl+S skip]  [Ctrl+P pause]  [Ctrl+X stop]  [Ctrl+C quit]")
		mon.SetCursorUpLines(2)
		mon.Start(os.Stdout)

		type tryResult struct {
			result *agent.TryResult
			err    error
		}
		tryCh := make(chan tryResult, 1)
		attemptCtx, cancelAttempt := context.WithCancel(ctx)
		defer cancelAttempt()
		pidCh := make(chan int, 1)
		opts.OnStart = func(pid int) {
			select {
			case pidCh <- pid:
			default:
			}
		}
		go func() {
			res, err := r.executeTry(attemptCtx, picked, opts)
			tryCh <- tryResult{res, err}
		}()

		var result *agent.TryResult
		var execErr error
		actionTaken := false
		freezeTriggered := false
		freezeTicker := time.NewTicker(freezeCheckInterval)
	actionLoop:
		for {
			select {
			case res := <-tryCh:
				result = res.result
				execErr = res.err
				break actionLoop
			case pid := <-pidCh:
				mon.SetProcessGroupID(pid)
				if freezeController != nil {
					freezeController.SetProcessGroupID(pid)
				}
			case <-freezeTicker.C:
				if freezeController == nil || freezeTriggered {
					continue
				}
				frozen, err := freezeController.Check(attemptCtx)
				if err != nil {
					fmt.Fprintf(log, "relay %d run %d attempt %d freeze check warning: %v\n", relay.ID, runIndex+1, attempt, err)
					continue
				}
				if !frozen {
					continue
				}
				freezeTriggered = true
				freezeMarked = true
				mon.SetFrozen(true)
				if onFreeze != nil {
					onFreeze()
				}
				fmt.Fprintf(log, "relay %d run %d attempt %d freeze detected for %s\n", relay.ID, runIndex+1, attempt, picked.Harness)
			case action := <-actionCh:
				switch action {
				case keyboard.ActionSkip:
					cancelAttempt()
					r.skipFlag.Store(true)
					actionTaken = true
					res := <-tryCh
					result = res.result
					execErr = res.err
					break actionLoop
				case keyboard.ActionPause:
					cancelAttempt()
					actionTaken = true
					res := <-tryCh
					result = res.result
					execErr = res.err
					break actionLoop
				case keyboard.ActionStop, keyboard.ActionQuit:
					r.stopFlag.Store(true)
				}
			}
		}
		freezeTicker.Stop()

		mon.Stop()
		kbCancel()
		_ = kb.Stop()

		endedAt := time.Now().UTC()

		headAfter, _ := r.headHash()

		commitHash := ""
		if headBefore != "" && headAfter != "" && headBefore != headAfter {
			commitHash = headAfter
		} else {
			dirty, _ := gitx.IsWorkspaceDirty(r.cfg.WorkspaceDir)
			if dirty {
				hash, commitErr := r.autoCommit(runIndex, picked.Harness, attempt)
				if commitErr != nil {
					fmt.Fprintf(log, "relay %d run %d attempt %d auto-commit warning: %v\n", relay.ID, runIndex+1, attempt, commitErr)
				} else {
					commitHash = hash
				}
			}
		}

		footerSuccess := execErr == nil && result != nil && result.Completed
		runtime := endedAt.Sub(startedAt)
		filesChangedCount := r.filesChangedCount(result, headBefore, headAfter, commitHash)
		shortHash := ""
		if len(commitHash) >= 7 {
			shortHash = commitHash[:7]
		} else if commitHash != "" {
			shortHash = commitHash
		}
		footer := style.RenderFooter(footerSuccess, runtime, filesChangedCount, shortHash)
		fmt.Println(footer)

		if err := gitx.CommitRallyState(r.cfg.WorkspaceDir); err != nil {
			fmt.Fprintf(log, "relay %d run %d attempt %d rally state commit warning: %v\n", relay.ID, runIndex+1, attempt, err)
		}

		failed := false
		if execErr != nil {
			failed = true
		} else if result == nil || !result.Completed {
			failed = true
		} else {
			hasChanges := commitHash != ""
			if !hasChanges {
				dirty, _ := gitx.IsWorkspaceDirty(r.cfg.WorkspaceDir)
				hasChanges = dirty
			}
			noFileChanges := !hasChanges
			tryRuntime := endedAt.Sub(startedAt)
			if noFileChanges && tryRuntime < 3*time.Minute {
				failed = true
			}
		}

		tryRecord := store.TryRecord{
			ID:            r.store.NextTryID(),
			RunID:         runIndex + 1,
			RelayID:       relay.ID,
			AgentType:     picked.Harness,
			Completed:     !failed && execErr == nil && result != nil && result.Completed,
			Summary:       "",
			RemainingWork: "",
			FilesChanged:  nil,
			CommitHash:    commitHash,
			StartedAt:     startedAt.Format(time.RFC3339),
			EndedAt:       endedAt.Format(time.RFC3339),
			AttemptNumber: attempt,
			LogPath:       tryLogPath,
		}
		if result != nil {
			tryRecord.Summary = result.Summary
			tryRecord.RemainingWork = result.RemainingWork
			tryRecord.FilesChanged = result.FilesChanged
		}
		if execErr != nil {
			tryRecord.Completed = false
			if tryRecord.Summary == "" {
				tryRecord.Summary = execErr.Error()
			}
		}
		if err := r.store.AppendTry(tryRecord); err != nil {
			return false, false, false, err
		}

		if actionTaken {
			if r.stopFlag.Load() {
				return false, false, true, nil
			}
			if r.skipFlag.Load() {
				return false, false, false, nil
			}
			fmt.Println("Paused — press Enter to resume")
			bufio.NewReader(os.Stdin).ReadString('\n')
			if result != nil {
				previousSummary = result.Summary
				lastResult = result
				if result.SessionID != "" {
					sessionID = result.SessionID
				}
			} else {
				previousSummary = ""
				lastResult = &agent.TryResult{Completed: false}
			}
			continue
		}

		if !failed {
			if freezeMarked && onFreezeRecovered != nil {
				onFreezeRecovered()
				mon.SetFrozen(false)
				mon.SetRecovered()
				freezeMarked = false
			}
			success = true
			lastResult = result
			break
		}

		if result != nil {
			previousSummary = result.Summary
			lastResult = result
			if result.SessionID != "" {
				sessionID = result.SessionID
			}
		} else {
			previousSummary = ""
			lastResult = &agent.TryResult{Completed: false}
		}
	}

	// Write stub entry if the agent did not finalize the run.
	stubSummary := ""
	if lastResult != nil {
		stubSummary = lastResult.Summary
	}
	_ = r.maybeWriteStubAndClearState(stubSummary)

	addressed := false
	if lastResult != nil && lastResult.MessageAddressed != nil {
		addressed = *lastResult.MessageAddressed
	}
	return success, addressed, false, nil
}

func (r *Runner) newFreezeController(logPath string, exec agent.Executor) reliability.FreezeController {
	if r.freezeControllerFactory != nil {
		return r.freezeControllerFactory(logPath)
	}
	threshold := r.cfg.FreezeThreshold
	if threshold <= 0 {
		threshold = reliability.DefaultFreezeThreshold
	}
	return reliability.NewFreezeControllerWithProbe(logPath, threshold, r.buildLivenessProbe(exec))
}

func (r *Runner) resolveCharsPerToken(harness string, exec agent.Executor) float64 {
	if r.cfg.CharsPerToken != nil {
		if v, ok := r.cfg.CharsPerToken[harness]; ok && v > 0 {
			return v
		}
	}
	if exec != nil {
		return exec.CharsPerToken()
	}
	return 0
}

func (r *Runner) buildLivenessProbe(exec agent.Executor) *reliability.LivenessProbe {
	if !r.cfg.LivenessProbe || exec == nil || !exec.LivenessProbeSupported() {
		return nil
	}
	return reliability.NewLivenessProbe(reliability.DefaultProbeTimeout, exec.ProbeLiveness)
}

func (r *Runner) resolveRunTask(ctx context.Context) (runTask, error) {
	task := runTask{
		Name:   "relay run",
		Prompt: r.cfg.TaskPrompt,
	}

	if !r.cfg.LapsEnabled {
		if task.Prompt == "" {
			task.Prompt = r.loadFallbackInstructions()
		}
		return task, nil
	}

	lap, err := headPullLap(ctx, r.cfg.WorkspaceDir)
	if err != nil {
		return runTask{}, fmt.Errorf("pull head lap: %w", err)
	}
	if lap == laps.NoLap {
		return task, nil
	}

	task.Name = lap.Title
	if strings.TrimSpace(lap.Description) != "" {
		task.Prompt = lap.Description
	} else {
		task.Prompt = lap.Title
	}
	task.Assignee = lap.Assignee

	var details []string
	if lap.ID != "" {
		details = append(details, "Lap ID: "+lap.ID)
	}
	if lap.Assignee != "" {
		details = append(details, "Assignee: "+lap.Assignee)
	}
	task.Requirements = strings.Join(details, "\n")

	return task, nil
}

func (r *Runner) resolveRoleInstructions(assignee string) (string, error) {
	if !r.cfg.LapsEnabled || strings.TrimSpace(assignee) == "" {
		return "", nil
	}

	return roleloader.Loader{WorkspaceDir: r.cfg.WorkspaceDir}.Load(assignee)
}

func (r *Runner) executeTry(ctx context.Context, picked agent.ResolvedAgent, opts agent.RunOptions) (*agent.TryResult, error) {
	exec, ok := r.executors[picked.Harness]
	if !ok {
		return nil, fmt.Errorf("no executor for agent %s", picked.Harness)
	}
	return exec.Execute(ctx, opts)
}

func (r *Runner) headHash() (string, error) {
	_, inGit, err := gitx.GitRepoRoot(r.cfg.WorkspaceDir)
	if err != nil || !inGit {
		return "", nil
	}
	out, err := gitx.GitOutput(r.cfg.WorkspaceDir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *Runner) autoCommit(runIndex int, agentType string, attempt int) (string, error) {
	repoRoot, ok, err := gitx.GitRepoRoot(r.cfg.WorkspaceDir)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}

	if _, err := gitx.GitOutput(repoRoot, "add", "-A"); err != nil {
		return "", err
	}

	_, err = gitx.GitOutput(repoRoot, "diff", "--cached", "--quiet")
	if err == nil {
		return "", nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return "", err
	}

	commitArgs := append(gitx.GitUserFallbackConfig(repoRoot), "commit")
	if !r.cfg.RunHooksOnAutoCommit {
		commitArgs = append(commitArgs, "--no-verify")
	}
	commitArgs = append(commitArgs, "-m", fmt.Sprintf("rally: run %d attempt %d (%s)", runIndex+1, attempt, agentType))
	if _, err := gitx.GitOutput(repoRoot, commitArgs...); err != nil {
		return "", err
	}

	hashOut, err := gitx.GitOutput(repoRoot, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(hashOut)), nil
}

func (r *Runner) filesChangedCount(result *agent.TryResult, headBefore, headAfter, commitHash string) int {
	if result != nil && len(result.FilesChanged) > 0 {
		return len(result.FilesChanged)
	}

	repoRoot, ok, err := gitx.GitRepoRoot(r.cfg.WorkspaceDir)
	if err == nil && ok {
		var out []byte
		if headBefore != "" && headAfter != "" && headBefore != headAfter {
			out, err = gitx.GitOutput(repoRoot, "diff", "--name-only", headBefore, headAfter)
		} else if commitHash != "" {
			out, err = gitx.GitOutput(repoRoot, "diff-tree", "--no-commit-id", "--name-only", "-r", commitHash)
		}
		if err == nil && len(out) > 0 {
			return countNonEmptyLines(string(out))
		}
	}

	dirtyCount, _ := monitor.GitDirtyCount(r.cfg.WorkspaceDir)
	return dirtyCount
}

func countNonEmptyLines(s string) int {
	count := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (r *Runner) maybeWriteStubAndClearState(lastOutput string) error {
	rs, err := progress.LoadRunState(r.cfg.WorkspaceDir)
	if err != nil {
		return err
	}
	// If no run-state file exists, LoadRunState returns a fresh empty state.
	// We only write a stub if the file actually existed on disk.
	if _, err := os.Stat(progress.RunStatePath(r.cfg.WorkspaceDir)); os.IsNotExist(err) {
		return nil
	}

	var lapsCompleted interface{}
	if r.cfg.LapsEnabled {
		if len(rs.RecordedLaps) > 0 {
			lapsCompleted = rs.RecordedLaps
		} else {
			lapsCompleted = "none"
		}
	}

	summary := lastOutput
	if summary == "" {
		summary = "(agent exited without finalizing)"
	}

	entry := progress.RunEntry{
		RunID:         rs.RunID,
		Summary:       truncate(summary, 160),
		LapsCompleted: lapsCompleted,
	}
	_ = progress.AppendRunEntry(r.cfg.WorkspaceDir, entry)
	_ = progress.ClearRunState(r.cfg.WorkspaceDir)
	return nil
}
