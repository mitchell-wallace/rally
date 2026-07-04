package runner

import (
	"fmt"
	"io"
	osexec "os/exec"
	"strings"

	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/store"
)

func (r *Runner) reconcileAttemptProgress(relay *store.RelayRecord, runIndex int, picked harnessapi.ResolvedAgent, task runTask, state *runOneState, attempt *runAttemptState, log io.Writer) {
	runStateAfter, _ := progress.LoadRunState(r.cfg.WorkspaceDir)
	recordedLaps := []string{}
	lapsAttempted := []store.LapAttempt{}
	handoffState := 0
	if runStateAfter != nil {
		recordedLaps = append(recordedLaps, runStateAfter.RecordedLaps...)
		lapsAttempted = append(lapsAttempted, storeLapAttempts(runStateAfter.LapsAttempted)...)
		handoffState = runStateAfter.HandoffState
	}
	runEntry := recordedOutingEntryForRun(r.cfg.WorkspaceDir, state.runID, state.summaryEntryCountBeforeRun)
	if task.IsLapsBacked && runEntry != nil {
		recordedLaps = mergeStrings(recordedLaps, progressOutingEntryLapIDs(*runEntry))
	}
	handoffEntry := handoffEntryFromOutingEntry(runEntry)
	recoveryClassification := recoveryClassificationForRun(task, runEntry)

	runtime := attempt.endedAt.Sub(attempt.startedAt)
	runRuntime := runtime
	if !state.runStartedAt.IsZero() {
		runRuntime = attempt.endedAt.Sub(state.runStartedAt)
	}
	commitHash := ""
	commitHistory := []string{}
	preCommitFilesChanged := r.filesChangedList(attempt.result, attempt.headBefore, attempt.headAfter, "")
	dirtyBeforeAutoCommit, _ := gitx.IsWorkspaceDirty(r.cfg.WorkspaceDir)
	dirtyAfter, _ := gitx.WorkspaceDirtyPaths(r.cfg.WorkspaceDir)
	hasOwnUncommittedChanges := hasDirtyChangesSince(state.runStartDirtySnapshot, dirtyAfter)
	finalized := !task.IsLapsBacked || len(recordedLaps) > 0 || handoffEntry != nil || handoffState != 0 || (task.LapID == "" && attempt.result != nil && attempt.result.Completed)
	hasUserFileChanges := len(preCommitFilesChanged) > 0
	incomplete := task.IsLapsBacked && hasOwnUncommittedChanges && !finalized
	dirtyHandoff := handoffEntry != nil && hasOwnUncommittedChanges
	if attempt.headBefore != "" && attempt.headAfter != "" && attempt.headBefore != attempt.headAfter {
		commitHistory = r.commitRange(attempt.headBefore, attempt.headAfter)
		if len(commitHistory) == 0 {
			commitHistory = []string{attempt.headAfter}
		}
		commitHash = commitHistory[len(commitHistory)-1]
	} else if dirtyBeforeAutoCommit && hasUserFileChanges && !incomplete && !dirtyHandoff && finalized {
		hash, commitErr := r.autoCommit(runIndex, picked.Harness, attempt.attempt)
		if commitErr != nil {
			fmt.Fprintf(log, "relay %d run %d attempt %d auto-commit warning: %v\n", relay.ID, runIndex+1, attempt.attempt, commitErr)
		} else if hash != "" {
			commitHash = hash
			commitHistory = []string{hash}
		}
	}

	filesChangedList := preCommitFilesChanged
	if commitHash != "" {
		filesChangedList = r.filesChangedList(attempt.result, attempt.headBefore, attempt.headAfter, commitHash)
	}
	filesChangedCount := len(filesChangedList)

	// Fold Rally's own bookkeeping (state + the summary.jsonl append) into
	// this attempt's commit when it produced one, so the summary lands inside
	// that commit rather than trailing it as a separate `rally: update state`
	// commit or a leftover working-tree change. With no commit, fall back to
	// the no-commit insurance path. Done here — after filesChangedList is
	// computed but before the footer/try record — so reported hashes are the
	// amended hash while filesChanged still excludes folded rally state.
	if commitHash != "" {
		if newHash, foldErr := gitx.FoldRallyStateIntoHead(r.cfg.WorkspaceDir); foldErr != nil {
			fmt.Fprintf(log, "relay %d run %d attempt %d rally state fold warning: %v\n", relay.ID, runIndex+1, attempt.attempt, foldErr)
		} else if newHash != "" && newHash != commitHash {
			if len(commitHistory) > 0 && commitHistory[len(commitHistory)-1] == commitHash {
				commitHistory[len(commitHistory)-1] = newHash
			}
			commitHash = newHash
		}
	} else if foldErr := gitx.FoldRallyState(r.cfg.WorkspaceDir); foldErr != nil {
		fmt.Fprintf(log, "relay %d run %d attempt %d rally state fold warning: %v\n", relay.ID, runIndex+1, attempt.attempt, foldErr)
	}

	shortHash := ""
	commitTitle := ""
	if len(commitHash) >= 7 {
		shortHash = commitHash[:7]
		cmd := osexec.Command("git", "log", "-1", "--pretty=%s", commitHash)
		cmd.Dir = r.cfg.WorkspaceDir
		if out, err := cmd.Output(); err == nil {
			commitTitle = strings.TrimSpace(string(out))
		}
	} else if commitHash != "" {
		shortHash = commitHash
	}

	attempt.runtime = runtime
	attempt.runRuntime = runRuntime
	attempt.recordedLaps = recordedLaps
	attempt.lapsAttempted = lapsAttempted
	attempt.handoffState = handoffState
	attempt.handoffEntry = handoffEntry
	attempt.recoveryClassification = recoveryClassification
	attempt.commitHash = commitHash
	attempt.commitHistory = commitHistory
	attempt.filesChangedList = filesChangedList
	attempt.filesChangedCount = filesChangedCount
	attempt.finalized = finalized
	attempt.incomplete = incomplete
	attempt.dirtyHandoff = dirtyHandoff
	attempt.shortHash = shortHash
	attempt.commitTitle = commitTitle
}
