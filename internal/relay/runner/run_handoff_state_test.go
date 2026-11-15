package runner

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

// handoffClassify runs the full classification table over a synthetic attempt
// in a clean git workspace and returns the resulting state.
func handoffClassify(t *testing.T, attempt *runAttemptState, state *runOneState) (*Runner, string) {
	t.Helper()
	workspaceDir := t.TempDir()
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")
	r := &Runner{cfg: Config{WorkspaceDir: workspaceDir}}
	var log bytes.Buffer
	task := runTask{Name: "t", Assignee: "senior", LapID: "rall-x", IsLapsBacked: true}
	attempt.attempt = 1
	r.classifyAttemptOutcome(&store.RelayRecord{ID: 1}, 0, harnessapi.ResolvedAgent{Harness: "claude", Model: "m"}, task, state, attempt, &log)
	return r, log.String()
}

func TestHandoffStateEndsAttempt_H1NoChanges(t *testing.T) {
	state := &runOneState{maxAttempts: 5}
	attempt := &runAttemptState{result: &harnessapi.TryResult{Completed: true}, runtime: time.Minute, handoffState: 1}
	r, _ := handoffClassify(t, attempt, state)
	if attempt.failed {
		t.Fatalf("attempt.failed = true (reason %q), want false for handoffState=1", state.failReason)
	}
	if attempt.attemptOutcome != reliability.OutcomeHandoffRequested {
		t.Fatalf("outcome = %q, want %q", attempt.attemptOutcome, reliability.OutcomeHandoffRequested)
	}
	d := r.decideRetryOrComplete(1, 1, runTask{Assignee: "senior"}, state, attempt, nil, &bytes.Buffer{})
	if d.action != runOneAttemptBreak || !state.success {
		t.Fatalf("decision = %v success=%v, want break+success (no retry)", d.action, state.success)
	}
}

func TestHandoffStateEndsAttempt_H2AgentError(t *testing.T) {
	state := &runOneState{maxAttempts: 5}
	attempt := &runAttemptState{result: &harnessapi.TryResult{Completed: false}, handoffState: 1}
	_, log := handoffClassify(t, attempt, state)
	if attempt.failed || !state.success {
		t.Fatalf("failed=%v success=%v reason=%q, want recovered", attempt.failed, state.success, state.failReason)
	}
	if attempt.attemptOutcome != reliability.OutcomeHandoffRequested {
		t.Fatalf("outcome = %q, want handoff_requested", attempt.attemptOutcome)
	}
	if !strings.Contains(log, "handoff recovery: laps handoff signalled for pinned lap \"rall-x\"") || !strings.Contains(log, "was: agent error") {
		t.Fatalf("missing recovery log line: %s", log)
	}
}

func TestHandoffStateEndsAttempt_H3StallKill(t *testing.T) {
	state := &runOneState{maxAttempts: 5, stallMarked: true}
	attempt := &runAttemptState{result: &harnessapi.TryResult{}, execErr: errors.New("killed by stall detector"), handoffState: 1}
	handoffClassify(t, attempt, state)
	if attempt.failed || !state.success {
		t.Fatalf("failed=%v success=%v reason=%q, want recovered", attempt.failed, state.success, state.failReason)
	}
	if attempt.attemptOutcome != reliability.OutcomeHandoffRequested {
		t.Fatalf("outcome = %q, want handoff_requested", attempt.attemptOutcome)
	}
}

func TestHandoffStateEndsAttempt_H4PinMismatchNotRecovered(t *testing.T) {
	state := &runOneState{maxAttempts: 5}
	attempt := &runAttemptState{result: &harnessapi.TryResult{Completed: false}, handoffState: 1, failed: true, lapPinMismatch: true}
	var log bytes.Buffer
	applyHandoffRecovery(&store.RelayRecord{ID: 1}, 0, runTask{LapID: "rall-x"}, state, attempt, &log)
	if !attempt.failed || state.success {
		t.Fatalf("failed=%v success=%v, want still failed on lapPinMismatch", attempt.failed, state.success)
	}
}

func TestHandoffStateEndsAttempt_H5NoStateStillNoChanges(t *testing.T) {
	state := &runOneState{maxAttempts: 5}
	attempt := &runAttemptState{result: &harnessapi.TryResult{Completed: true}, runtime: time.Minute}
	r, _ := handoffClassify(t, attempt, state)
	if !attempt.failed || state.failReason != "no changes made" {
		t.Fatalf("failed=%v reason=%q, want failed 'no changes made'", attempt.failed, state.failReason)
	}
	d := r.decideRetryOrComplete(1, 1, runTask{Assignee: "senior"}, state, attempt, nil, &bytes.Buffer{})
	if d.action != runOneAttemptContinue {
		t.Fatalf("decision = %v, want continue (retry)", d.action)
	}
}

func TestHandoffStateEndsAttempt_H6UsageLimitStillTerminal(t *testing.T) {
	state := &runOneState{maxAttempts: 5}
	attempt := &runAttemptState{
		result:  &harnessapi.TryResult{Completed: false, Evidence: &reliability.FailureEvidence{Category: reliability.CategoryUsageLimit, ResetAfter: time.Hour}},
		execErr: errors.New("usage limit"),
	}
	handoffClassify(t, attempt, state)
	if !attempt.failed || state.failureCategory != reliability.CategoryUsageLimit || !attempt.terminalForRun {
		t.Fatalf("failed=%v category=%q terminal=%v, want failed usage_limit terminal", attempt.failed, state.failureCategory, attempt.terminalForRun)
	}
}

func TestHandoffStateEndsAttempt_H7HandoffEntryRegression(t *testing.T) {
	state := &runOneState{maxAttempts: 5}
	attempt := &runAttemptState{result: &harnessapi.TryResult{Completed: true}, runtime: time.Minute, handoffEntry: &progress.HandoffEntry{}}
	handoffClassify(t, attempt, state)
	if attempt.failed {
		t.Fatalf("failed with handoffEntry, reason %q", state.failReason)
	}
	if attempt.attemptOutcome != reliability.OutcomeHandoffRequested {
		t.Fatalf("outcome = %q, want handoff_requested", attempt.attemptOutcome)
	}
}
