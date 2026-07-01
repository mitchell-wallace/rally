//go:build linux

package process

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// prSetChildSubreaper is PR_SET_CHILD_SUBREAPER from <linux/prctl.h>. Marking
// the test process as a subreaper makes orphaned descendants reparent to us
// (instead of PID 1) when their parent dies, so we can deterministically reap
// and inspect the leader's child without depending on PID 1's reaping cadence —
// which is unreliable in minimal containers.
const prSetChildSubreaper = 36

// TestSetProcessGroupCancelKillsEntireProcessGroup is the regression guard for
// the orphaned-child gap: Go's os/exec WaitDelay escalation only SIGKILLs the
// group leader (cmd.Process), so a non-leader child that ignores SIGINT would
// survive cancellation. Here BOTH the leader and an inherited non-leader child
// ignore SIGINT; after the grace window the cancel path must have SIGKILLed the
// whole process group, leaving no survivor.
func TestSetProcessGroupCancelKillsEntireProcessGroup(t *testing.T) {
	if _, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0, 0, 0, 0); errno != 0 {
		t.Skipf("cannot become child subreaper: %v", errno)
	}

	dir := t.TempDir()
	leaderReady := filepath.Join(dir, "leader-ready")
	childReady := filepath.Join(dir, "child-ready")
	childPID := filepath.Join(dir, "child-pid")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperGroupLeader$")
	cmd.Env = append(os.Environ(),
		"RALLY_GROUP_HELPER=leader",
		"RALLY_LEADER_READY_FILE="+leaderReady,
		"RALLY_CHILD_READY_FILE="+childReady,
		"RALLY_CHILD_PID_FILE="+childPID,
		"RALLY_CHILD_IGNORE_INT=1",
	)
	SetProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the leader and its SIGINT-ignoring child to be running.
	if !waitForFile(t, leaderReady, 10*time.Second) || !waitForFile(t, childReady, 10*time.Second) {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		t.Fatal("helper processes did not become ready")
	}
	pid := readPIDFile(t, childPID)

	start := time.Now()
	cancel()

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case <-waitErr:
	case <-time.After(gracefulWaitDelay + 15*time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		t.Fatal("leader did not exit within the grace window")
	}
	elapsed := time.Since(start)

	// The leader ignored SIGINT, so it could only have been removed by the
	// group-wide SIGKILL escalation after the grace window.
	if elapsed < gracefulWaitDelay-time.Second {
		t.Fatalf("leader exited after %v, before the grace drain (%v); SIGINT alone should not have killed it", elapsed, gracefulWaitDelay)
	}
	if st, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
		if !st.Signaled() || st.Signal() != syscall.SIGKILL {
			t.Fatalf("leader exit status = %v, want killed by SIGKILL", cmd.ProcessState)
		}
	}

	// The non-leader child also ignored SIGINT. Because we are a subreaper, it
	// reparents to us once the leader dies; reaping it confirms it is gone and
	// lets us assert it died from the group SIGKILL rather than surviving as an
	// orphan. This is precisely what Go's leader-only WaitDelay would miss.
	st, ok := reapChild(t, pid, 10*time.Second)
	if !ok {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("non-leader child (pid %d) survived cancel: process group not SIGKILLed", pid)
	}
	if !st.Signaled() || st.Signal() != syscall.SIGKILL {
		t.Fatalf("non-leader child exit status = %v, want killed by SIGKILL", st)
	}
}

func readPIDFile(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read child pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", data, err)
	}
	return pid
}

// reapChild waits up to timeout for pid (reparented to this subreaper test
// process) to exit and reaps it, returning its wait status. It tolerates ECHILD
// while the child is still parented to the not-yet-dead leader.
func reapChild(t *testing.T, pid int, timeout time.Duration) (syscall.WaitStatus, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var ws syscall.WaitStatus
	for time.Now().Before(deadline) {
		wpid, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
		if wpid == pid {
			return ws, true
		}
		// wpid == 0: child exists but has not exited yet.
		// err == ECHILD: not yet reparented to us (leader still alive).
		_ = err
		time.Sleep(20 * time.Millisecond)
	}
	return ws, false
}
