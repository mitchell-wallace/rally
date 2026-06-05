package agent

import (
	"context"
	"os/exec"
	"syscall"

	"github.com/mitchell-wallace/rally/internal/reliability"
)

// gracefulWaitDelay is the WaitDelay backstop for the cancel path. It tracks the
// shared reliability grace window so the pipe-draining/leader-kill backstop never
// fires before KillProcessGroup's own group-wide escalation has run.
const gracefulWaitDelay = reliability.GracefulShutdownGrace

// SetProcessGroup sets the subprocess to run in its own process group and wires
// up graceful, group-wide cancellation. When the command's context is
// cancelled, Cmd.Cancel escalates across the WHOLE process group via the shared
// reliability.KillProcessGroup helper: SIGINT first (giving the harness and any
// children a chance to clean up), then SIGKILL for any survivors after the grace
// window. WaitDelay is kept as a backstop and to bound pipe draining, but it is
// no longer the escalation authority — Go's WaitDelay would only SIGKILL the
// group leader, leaving a non-leader child that ignores SIGINT orphaned.
func SetProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Setpgid makes the leader its own group leader, so its PID is the
		// process group ID. Use a fresh context: the command's context is
		// already cancelled (that is why Cancel fired), and KillProcessGroup's
		// own grace window bounds the wait. An already-exited group surfaces as
		// nil, so cancellation does not produce a spurious error.
		return reliability.KillProcessGroup(context.Background(), cmd.Process.Pid)
	}
	cmd.WaitDelay = gracefulWaitDelay
}
