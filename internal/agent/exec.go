package agent

import (
	"os/exec"
	"syscall"
)

// SetProcessGroup sets the subprocess to run in its own process group.
func SetProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
