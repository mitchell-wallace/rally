package agent

import (
	"os/exec"
	"syscall"
	"time"
)

// gracefulWaitDelay bounds how long Go waits after Cmd.Cancel (SIGINT to the
// process group) before escalating to SIGKILL on the leader. It mirrors the
// stall killer's drain window so both shutdown paths use the same grace period.
const gracefulWaitDelay = 5 * time.Second

// SetProcessGroup sets the subprocess to run in its own process group and wires
// up graceful cancellation. When the command's context is cancelled, Cmd.Cancel
// sends SIGINT to the whole process group (negative PID) instead of SIGKILL to
// only the leader, giving the harness a chance to clean up. WaitDelay bounds the
// grace window: if the process group has not exited within gracefulWaitDelay,
// Go escalates to SIGKILL.
func SetProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
	}
	cmd.WaitDelay = gracefulWaitDelay
}
