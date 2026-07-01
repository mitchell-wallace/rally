//go:build !windows

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// The helper processes below re-exec the test binary to model a real harness:
// a leader process (cmd.Process) that spawns a non-leader child inheriting its
// process group. Unlike shell background jobs (which POSIX forces to ignore
// SIGINT), these Go children keep the default/ catchable SIGINT disposition,
// matching how agent CLIs spawn their own subprocesses.

func TestHelperGroupLeader(t *testing.T) {
	if os.Getenv("RALLY_GROUP_HELPER") != "leader" {
		return
	}
	// Survive SIGINT so termination requires the SIGKILL escalation after
	// WaitDelay — this is what the parent test asserts.
	signal.Ignore(syscall.SIGINT)

	child := exec.Command(os.Args[0], "-test.run=^TestHelperGroupChild$")
	child.Env = append(os.Environ(),
		"RALLY_GROUP_HELPER=child",
		"RALLY_CHILD_INT_FILE="+os.Getenv("RALLY_CHILD_INT_FILE"),
		"RALLY_CHILD_READY_FILE="+os.Getenv("RALLY_CHILD_READY_FILE"),
		"RALLY_CHILD_PID_FILE="+os.Getenv("RALLY_CHILD_PID_FILE"),
		"RALLY_CHILD_IGNORE_INT="+os.Getenv("RALLY_CHILD_IGNORE_INT"),
	)
	// No Setpgid: the child inherits the leader's process group, so a
	// negative-PID signal must reach it for the group-reach assertion to hold.
	if err := child.Start(); err != nil {
		os.Exit(2)
	}

	_ = os.WriteFile(os.Getenv("RALLY_LEADER_READY_FILE"), []byte("1"), 0o644)
	// Block until SIGKILLed by the parent's WaitDelay escalation. Use a sleep
	// loop rather than select{}: the latter leaves no wakeable goroutine, so
	// the Go runtime's deadlock detector aborts the process instantly (which
	// would defeat the point of ignoring SIGINT). A pending timer keeps the
	// runtime from declaring a deadlock.
	for {
		time.Sleep(time.Hour)
	}
}

func TestHelperGroupChild(t *testing.T) {
	if os.Getenv("RALLY_GROUP_HELPER") != "child" {
		return
	}
	if pidFile := os.Getenv("RALLY_CHILD_PID_FILE"); pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
	if os.Getenv("RALLY_CHILD_IGNORE_INT") == "1" {
		// Ignore SIGINT so only the group-wide SIGKILL escalation can reap this
		// non-leader child. Go's WaitDelay would SIGKILL only the leader, so a
		// surviving child here proves the cancel path failed to kill the group.
		signal.Ignore(syscall.SIGINT)
		_ = os.WriteFile(os.Getenv("RALLY_CHILD_READY_FILE"), []byte("1"), 0o644)
		for {
			time.Sleep(time.Hour)
		}
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT)
	_ = os.WriteFile(os.Getenv("RALLY_CHILD_READY_FILE"), []byte("1"), 0o644)
	<-ch
	_ = os.WriteFile(os.Getenv("RALLY_CHILD_INT_FILE"), []byte("int"), 0o644)
	os.Exit(0)
}

// TestSetProcessGroupCancelSignalsGroupThenKills verifies the graceful-shutdown
// contract wired up by SetProcessGroup:
//
//  1. Cancelling the context sends SIGINT to the whole process GROUP (negative
//     PID), not just the leader. We pin the group-reach win by observing a
//     non-leader child — spawned by the leader and inheriting its process
//     group — record receipt of SIGINT. Today's default CommandContext cancel
//     SIGKILLs only cmd.Process, so this child would never see a signal.
//  2. When the leader ignores SIGINT, Go escalates to SIGKILL after WaitDelay,
//     so the process still terminates within a bounded window.
func TestSetProcessGroupCancelSignalsGroupThenKills(t *testing.T) {
	dir := t.TempDir()
	leaderReady := filepath.Join(dir, "leader-ready")
	childReady := filepath.Join(dir, "child-ready")
	childInt := filepath.Join(dir, "child-int")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperGroupLeader$")
	cmd.Env = append(os.Environ(),
		"RALLY_GROUP_HELPER=leader",
		"RALLY_LEADER_READY_FILE="+leaderReady,
		"RALLY_CHILD_READY_FILE="+childReady,
		"RALLY_CHILD_INT_FILE="+childInt,
	)
	SetProcessGroup(cmd)

	if cmd.WaitDelay != gracefulWaitDelay {
		t.Fatalf("WaitDelay = %v, want %v", cmd.WaitDelay, gracefulWaitDelay)
	}
	if cmd.Cancel == nil {
		t.Fatal("Cancel func not set by SetProcessGroup")
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for both the leader and its child to be running with the child's
	// SIGINT handler installed.
	if !waitForFile(t, leaderReady, 10*time.Second) || !waitForFile(t, childReady, 10*time.Second) {
		_ = cmd.Process.Kill()
		t.Fatal("helper processes did not become ready")
	}

	start := time.Now()
	cancel()

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case <-waitErr:
	case <-time.After(gracefulWaitDelay + 10*time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("leader did not exit within the WaitDelay window")
	}
	elapsed := time.Since(start)

	// Group-reach win: the non-leader child received SIGINT via the negative PID.
	data, err := os.ReadFile(childInt)
	if err != nil || string(data) != "int" {
		t.Fatalf("non-leader child did not record SIGINT (group not reached): data=%q err=%v", data, err)
	}

	// Escalation: the leader ignored SIGINT, so termination required the SIGKILL
	// that Go sends after WaitDelay. Confirm it took roughly that long and that
	// the leader was killed by SIGKILL rather than exiting cleanly.
	if elapsed < gracefulWaitDelay-time.Second {
		t.Fatalf("leader exited after %v, before the WaitDelay drain (%v); SIGINT alone should not have killed it", elapsed, gracefulWaitDelay)
	}
	if st, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
		if !st.Signaled() || st.Signal() != syscall.SIGKILL {
			t.Fatalf("leader exit status = %v, want killed by SIGKILL", cmd.ProcessState)
		}
	}
}

// TestSetProcessGroupCancelNoProcess ensures the Cancel func is safe to invoke
// before the process has started (cmd.Process is nil).
func TestSetProcessGroupCancelNoProcess(t *testing.T) {
	cmd := exec.Command("sh", "-c", "true")
	SetProcessGroup(cmd)
	if err := cmd.Cancel(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("Cancel before Start returned %v, want nil", err)
	}
}

func waitForFile(t *testing.T, path string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
