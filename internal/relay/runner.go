package relay

import (
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
	"github.com/mitchell-wallace/rally/internal/store"
)

type Config struct {
	WorkspaceDir         string
	DataDir              string
	AgentMixSpecs        []string
	TargetIterations     int
	RunHooksOnAutoCommit bool
	BeadsEnabled         bool
	Instructions         string
	TaskPrompt           string
	OverwriteMixOnResume bool
}

type Runner struct {
	store      *store.Store
	cfg        Config
	executors  map[string]agent.Executor
	stopFlag   atomic.Bool
	log        io.WriteCloser
	resilience *Resilience
}

func NewRunner(s *store.Store, cfg Config, executors map[string]agent.Executor) *Runner {
	return &Runner{
		store:     s,
		cfg:       cfg,
		executors: executors,
	}
}

func (r *Runner) RequestStop() {
	r.stopFlag.Store(true)
}

func (r *Runner) Run(ctx context.Context) error {
	relay, resumed, err := ResumeRelay(r.store)
	if err != nil {
		return err
	}

	var mix AgentMix
	if resumed {
		// Resuming an existing relay
		if r.cfg.OverwriteMixOnResume {
			// Use new mix from CLI and update relay record
			mix, err = ParseAgentMix(r.cfg.AgentMixSpecs)
			if err != nil {
				return err
			}
			relay.AgentMix = mix.Label
			if err := r.store.UpdateRelay(*relay); err != nil {
				return err
			}
		} else {
			// Use stored mix from relay
			mix, err = ParseAgentMix(strings.Fields(relay.AgentMix))
			if err != nil {
				return err
			}
		}
	} else {
		// New relay
		mix, err = ParseAgentMix(r.cfg.AgentMixSpecs)
		if err != nil {
			return err
		}
		relay, err = CreateRelay(r.store, r.cfg.TargetIterations, mix.Label)
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

	fmt.Fprintf(log, "relay %d started (target %d iterations, mix: %s)\n", relay.ID, relay.TargetIterations, mix.Label)

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

		agentType, nextRunIndex, isHourlyRetry, err := resilience.SelectActiveAgent(mix, runIndex)
		if err != nil {
			if err.Error() == "all agents frozen" {
				fmt.Fprintf(log, "relay %d failed: all agents frozen\n", relay.ID)
				_ = CompleteRelay(r.store, relay.ID)
				return fmt.Errorf("relay failed: all agents frozen")
			}
			if err.Error() == "all agents paused" {
				sleepDuration := timeUntilNextRetry(resilience, mix)
				fmt.Fprintf(log, "relay %d all agents paused, waiting %v\n", relay.ID, sleepDuration)
				select {
				case <-time.After(sleepDuration):
					continue
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return err
		}

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

		success, addressed, interrupted, err := r.runOne(ctx, relay, runIndex, agentType, consumedMsg, relayMsg, isHourlyRetry, log)
		if err != nil {
			fmt.Fprintf(log, "relay %d run %d error: %v\n", relay.ID, runIndex+1, err)
			return err
		}
		if interrupted {
			fmt.Fprintf(log, "relay %d stop requested, halting\n", relay.ID)
			break
		}

		if isHourlyRetry {
			if success {
				if err := resilience.UnpauseAgent(agentType, relay.ID); err != nil {
					return err
				}
			} else {
				if err := resilience.RecordHourlyFailure(agentType, relay.ID); err != nil {
					return err
				}
			}
		} else {
			if !success {
				if err := resilience.PauseAgent(agentType, relay.ID); err != nil {
					return err
				}
			}
		}

		if success {
			relay.CompletedIterations++
			runIndex = nextRunIndex
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
			runIndex = nextRunIndex
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

	return nil
}

func timeUntilNextRetry(resilience *Resilience, mix AgentMix) time.Duration {
	minWait := time.Hour
	now := resilience.NowFunc()
	for _, a := range mix.Cycle {
		st, since := resilience.getState(a)
		if st == StatePaused {
			wait := since.Add(resilience.PauseDuration).Sub(now)
			if wait < minWait {
				minWait = wait
			}
		}
	}
	if minWait < 0 {
		minWait = 0
	}
	if minWait > time.Hour {
		minWait = time.Hour
	}
	return minWait
}

func (r *Runner) runOne(ctx context.Context, relay *store.RelayRecord, runIndex int, agentType string, consumedMsg *store.MessageRecord, relayMsg *store.MessageRecord, isHourlyRetry bool, log io.Writer) (bool, bool, bool, error) {
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
	success := false

	maxAttempts := 3
	if isHourlyRetry {
		maxAttempts = 1
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return false, false, false, ctx.Err()
		}

		// Graceful stop: don't start a new try if stop requested.
		// Current try always completes before this check is reached again.
		if r.stopFlag.Load() {
			return false, false, true, nil
		}

		opts := agent.RunOptions{
			Persona:          agentType,
			TaskName:         "relay run",
			TaskPrompt:       r.cfg.TaskPrompt,
			Instructions:     r.cfg.Instructions,
			InboxMessage:     inbox,
			RelayMessage:     relayMessage,
			PreviousSummary:  previousSummary,
			RecentTryContext: recentContext.String(),
			BeadsEnabled:     r.cfg.BeadsEnabled,
		}
		prompt := agent.BuildPrompt(opts)

		taskPath := filepath.Join(r.cfg.WorkspaceDir, ".rally", "current_task.md")
		if err := os.WriteFile(taskPath, []byte(prompt), 0o644); err != nil {
			return false, false, false, fmt.Errorf("write current_task.md: %w", err)
		}

		headBefore, _ := r.headHash()
		startedAt := time.Now().UTC()
		result, execErr := r.executeTry(ctx, agentType, opts)
		endedAt := time.Now().UTC()
		headAfter, _ := r.headHash()

		commitHash := ""
		if headBefore != "" && headAfter != "" && headBefore != headAfter {
			commitHash = headAfter
		} else {
			dirty, _ := gitx.IsGitDirty(r.cfg.WorkspaceDir)
			if dirty {
				hash, commitErr := r.autoCommit(runIndex, agentType, attempt)
				if commitErr != nil {
					fmt.Fprintf(log, "relay %d run %d attempt %d auto-commit warning: %v\n", relay.ID, runIndex+1, attempt, commitErr)
				} else {
					commitHash = hash
				}
			}
		}

		// Failure detection
		failed := false
		if execErr != nil {
			failed = true
		} else if result == nil || !result.Completed {
			failed = true
		} else {
			hasChanges := commitHash != ""
			if !hasChanges {
				dirty, _ := gitx.IsGitDirty(r.cfg.WorkspaceDir)
				hasChanges = dirty
			}
			noFileChanges := !hasChanges
			runtime := endedAt.Sub(startedAt)
			if noFileChanges && runtime < 3*time.Minute {
				failed = true
			}
		}

		tryRecord := store.TryRecord{
			ID:            r.store.NextTryID(),
			RunID:         runIndex + 1,
			RelayID:       relay.ID,
			AgentType:     agentType,
			Completed:     !failed && execErr == nil && result != nil && result.Completed,
			Summary:       "",
			RemainingWork: "",
			FilesChanged:  nil,
			CommitHash:    commitHash,
			StartedAt:     startedAt.Format(time.RFC3339),
			EndedAt:       endedAt.Format(time.RFC3339),
			AttemptNumber: attempt,
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

		if !failed {
			success = true
			lastResult = result
			break
		}

		if result != nil {
			previousSummary = result.Summary
			lastResult = result
		} else {
			previousSummary = ""
			lastResult = &agent.TryResult{Completed: false}
		}
	}

	addressed := false
	if lastResult != nil && lastResult.MessageAddressed != nil {
		addressed = *lastResult.MessageAddressed
	}
	return success, addressed, false, nil
}

func (r *Runner) executeTry(ctx context.Context, agentType string, opts agent.RunOptions) (*agent.TryResult, error) {
	exec, ok := r.executors[agentType]
	if !ok {
		return nil, fmt.Errorf("no executor for agent %s", agentType)
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

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}
