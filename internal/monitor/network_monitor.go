package monitor

import (
	"runtime"
	"time"
)

// NetworkMonitor tracks network state and produces warnings.
type NetworkMonitor struct {
	pids             []int
	lastConnTime     time.Time
	lastSyscallTime  time.Time
	lastSyscallBytes uint64
}

// NewNetworkMonitor creates a NetworkMonitor for the given PIDs.
func NewNetworkMonitor(pids []int) *NetworkMonitor {
	now := time.Now()
	return &NetworkMonitor{
		pids:            pids,
		lastConnTime:    now,
		lastSyscallTime: now,
	}
}

func (n *NetworkMonitor) evaluate(now time.Time, conns int, syscallBytes uint64) []string {
	if conns > 0 {
		n.lastConnTime = now
	}
	if syscallBytes > n.lastSyscallBytes {
		n.lastSyscallTime = now
		n.lastSyscallBytes = syscallBytes
	}

	var warnings []string
	if conns == 0 && now.Sub(n.lastConnTime) >= 30*time.Second {
		warnings = append(warnings, "No TCP… (30s)")
	}
	// Warn when connections exist but no syscall I/O (rchar+wchar) in 30s.
	// This detects rate-limited or idle agents that are connected but not
	// sending/receiving data.
	if conns > 0 && now.Sub(n.lastSyscallTime) >= 30*time.Second {
		warnings = append(warnings, "No I/O… (30s)")
	}

	return warnings
}

// Check evaluates network state and returns any warnings.
func (n *NetworkMonitor) Check() []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	conns, err := CountTCPConnections(n.pids)
	if err != nil {
		return nil
	}
	syscallBytes, err := ReadSyscallBytes(n.pids)
	if err != nil {
		return nil
	}
	return n.evaluate(time.Now(), conns, syscallBytes)
}
