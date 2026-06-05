//go:build !windows

package reliability

import (
	"errors"
	"syscall"
)

func sendProcessGroupSignal(pgid int, sig processSignal) error {
	var signal syscall.Signal
	switch sig {
	case signalTerminate:
		// SIGINT (not SIGTERM) so the stall killer uses the same signal as the
		// cancel path (internal/agent/exec.go SetProcessGroup), giving harnesses
		// a consistent graceful-shutdown signal across both kill paths.
		signal = syscall.SIGINT
	case signalKill:
		signal = syscall.SIGKILL
	default:
		return nil
	}
	err := syscall.Kill(-pgid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func processGroupRunning(pgid int) (bool, error) {
	err := syscall.Kill(-pgid, 0)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	case errors.Is(err, syscall.EPERM):
		return true, nil
	default:
		return false, err
	}
}
