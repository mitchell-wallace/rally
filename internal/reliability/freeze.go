package reliability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/mitchell-wallace/rally/internal/monitor"
)

const (
	DefaultFreezeThreshold = 180 * time.Second
	DefaultFreezeTick      = 5 * time.Second
	defaultKillDrain       = 5 * time.Second
	defaultKillPoll        = 100 * time.Millisecond
	// NetworkSilentThreshold is how long an agent may hold open TCP connections
	// without any syscall I/O (rchar+wchar) before it is declared frozen.
	// This catches rate-limited agents that keep a connection alive but send no data.
	NetworkSilentThreshold = 5 * time.Minute
)

var errProcessGroupUnavailable = errors.New("freeze detector waiting for process group")

type SignalSnapshot struct {
	Now          time.Time
	LogSilentFor time.Duration
	Connections  int
	IOBytes      uint64
	SyscallBytes uint64 // rchar+wchar from /proc/PID/io — includes network socket I/O
}

type Assessment struct {
	Frozen    bool
	Ambiguous bool
}

type Detector struct {
	threshold         time.Duration
	platform          string
	lastIOBytes       uint64
	lastIOChange      time.Time
	lastSyscallBytes  uint64
	lastSyscallChange time.Time
	initialized       bool
}

func NewDetector(threshold time.Duration) *Detector {
	if threshold <= 0 {
		threshold = DefaultFreezeThreshold
	}
	return &Detector{
		threshold: threshold,
		platform:  runtime.GOOS,
	}
}

func NewDetectorForPlatform(platform string, threshold time.Duration) *Detector {
	if threshold <= 0 {
		threshold = DefaultFreezeThreshold
	}
	return &Detector{
		threshold: threshold,
		platform:  platform,
	}
}

func (d *Detector) Evaluate(snapshot SignalSnapshot) bool {
	return d.Assess(snapshot).Frozen
}

func (d *Detector) Assess(snapshot SignalSnapshot) Assessment {
	switch d.platform {
	case "windows":
		return Assessment{}
	case "darwin":
		return Assessment{Frozen: snapshot.LogSilentFor >= d.threshold}
	case "linux":
		if snapshot.Now.IsZero() {
			snapshot.Now = time.Now()
		}
		if !d.initialized {
			d.initialized = true
			d.lastIOBytes = snapshot.IOBytes
			d.lastIOChange = snapshot.Now
			d.lastSyscallBytes = snapshot.SyscallBytes
			d.lastSyscallChange = snapshot.Now
		}
		if snapshot.IOBytes != d.lastIOBytes {
			d.lastIOBytes = snapshot.IOBytes
			d.lastIOChange = snapshot.Now
		}
		if snapshot.SyscallBytes != d.lastSyscallBytes {
			d.lastSyscallBytes = snapshot.SyscallBytes
			d.lastSyscallChange = snapshot.Now
		}
		ioSilentFor := snapshot.Now.Sub(d.lastIOChange)
		syscallSilentFor := snapshot.Now.Sub(d.lastSyscallChange)
		// Classic: log silent + no connections for threshold.
		// We intentionally exclude the IO-silence check here: idle and background
		// processes do sporadic disk writes every 30-40s (GC, buffers, opencode
		// TUI) that would prevent ioSilentFor from ever reaching the threshold,
		// causing the freeze to never fire even when the process is clearly stuck.
		classicFrozen := snapshot.LogSilentFor >= d.threshold &&
			snapshot.Connections == 0
		// Connected-but-silent: agent holds open TCP connections but no syscall
		// I/O (send/recv) has occurred for NetworkSilentThreshold — catches
		// rate-limited agents that keep a connection alive without sending data.
		connectedFrozen := snapshot.LogSilentFor >= d.threshold &&
			snapshot.Connections > 0 &&
			syscallSilentFor >= NetworkSilentThreshold
		return Assessment{
			Frozen: classicFrozen || connectedFrozen,
			Ambiguous: snapshot.LogSilentFor < DefaultAmbiguousProbeWindow &&
				ioSilentFor >= DefaultAmbiguousProbeWindow,
		}
	default:
		return Assessment{}
	}
}

type FreezeController interface {
	SetProcessGroupID(int)
	Check(context.Context) (bool, error)
}

type freezeController struct {
	logPath      string
	netStatsPath string // JSONL file for per-tick network stats; empty = disabled
	pgid         int
	detector     *Detector
	killer       processGroupKiller
	now          func() time.Time
	platform     string
	probe        *LivenessProbe
	takeSnapshot func() (SignalSnapshot, error)
}

func NewFreezeController(logPath string, threshold time.Duration) FreezeController {
	return NewFreezeControllerWithProbe(logPath, threshold, nil)
}

func NewFreezeControllerWithProbe(logPath string, threshold time.Duration, probe *LivenessProbe) FreezeController {
	return NewFreezeControllerFull(logPath, threshold, probe, "")
}

// NewFreezeControllerFull creates a freeze controller with optional per-tick
// network stats logging. When netStatsPath is non-empty, each Check() call
// appends a JSONL record with snapshot metrics (connections, io_bytes,
// syscall_bytes, log_silent_s) for post-run analysis.
func NewFreezeControllerFull(logPath string, threshold time.Duration, probe *LivenessProbe, netStatsPath string) FreezeController {
	return &freezeController{
		logPath:      logPath,
		netStatsPath: netStatsPath,
		detector:     NewDetector(threshold),
		killer: processGroupKiller{
			drain:     defaultKillDrain,
			poll:      defaultKillPoll,
			now:       time.Now,
			sleep:     time.Sleep,
			send:      sendProcessGroupSignal,
			isRunning: processGroupRunning,
		},
		now:      time.Now,
		platform: runtime.GOOS,
		probe:    probe,
	}
}

func (c *freezeController) SetProcessGroupID(pgid int) {
	c.pgid = pgid
}

func (c *freezeController) Check(ctx context.Context) (bool, error) {
	snapshotFn := c.snapshot
	if c.takeSnapshot != nil {
		snapshotFn = c.takeSnapshot
	}

	snapshot, err := snapshotFn()
	if err != nil {
		if errors.Is(err, errProcessGroupUnavailable) {
			return false, nil
		}
		return false, err
	}

	if c.netStatsPath != "" {
		c.appendNetStat(snapshot)
	}

	assessment := c.detector.Assess(snapshot)
	probeConfirmedFreeze := false
	if assessment.Ambiguous && c.probe != nil {
		if c.probe.Check(ctx) {
			return false, nil
		}
		probeConfirmedFreeze = true
	}

	if !assessment.Frozen && !probeConfirmedFreeze {
		return false, nil
	}
	if err := c.killer.Kill(ctx, c.pgid); err != nil {
		return true, err
	}
	return true, nil
}

func (c *freezeController) snapshot() (SignalSnapshot, error) {
	lastActivity, err := monitor.LogLastActivity(c.logPath)
	if err != nil {
		return SignalSnapshot{}, fmt.Errorf("freeze detector log activity: %w", err)
	}

	snapshot := SignalSnapshot{
		Now:          c.now(),
		LogSilentFor: lastActivity,
	}

	if c.platform != "linux" {
		return snapshot, nil
	}
	if c.pgid <= 0 {
		return SignalSnapshot{}, errProcessGroupUnavailable
	}

	pids, err := monitor.GetPIDsInGroup(c.pgid)
	if err != nil {
		return SignalSnapshot{}, fmt.Errorf("freeze detector pids: %w", err)
	}
	conns, err := monitor.CountTCPConnections(pids)
	if err != nil {
		return SignalSnapshot{}, fmt.Errorf("freeze detector connections: %w", err)
	}
	ioBytes, err := monitor.ReadIOBytes(pids)
	if err != nil {
		return SignalSnapshot{}, fmt.Errorf("freeze detector io bytes: %w", err)
	}
	syscallBytes, err := monitor.ReadSyscallBytes(pids)
	if err != nil {
		return SignalSnapshot{}, fmt.Errorf("freeze detector syscall bytes: %w", err)
	}

	snapshot.Connections = conns
	snapshot.IOBytes = ioBytes
	snapshot.SyscallBytes = syscallBytes
	return snapshot, nil
}

type netStatEntry struct {
	Timestamp    string  `json:"ts"`
	LogSilentS   float64 `json:"log_silent_s"`
	Connections  int     `json:"connections"`
	IOBytes      uint64  `json:"io_bytes"`
	SyscallBytes uint64  `json:"syscall_bytes"`
}

func (c *freezeController) appendNetStat(snap SignalSnapshot) {
	entry := netStatEntry{
		Timestamp:    snap.Now.UTC().Format(time.RFC3339),
		LogSilentS:   snap.LogSilentFor.Seconds(),
		Connections:  snap.Connections,
		IOBytes:      snap.IOBytes,
		SyscallBytes: snap.SyscallBytes,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(c.netStatsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

type processSignal int

const (
	signalTerminate processSignal = iota
	signalKill
)

type processGroupKiller struct {
	drain     time.Duration
	poll      time.Duration
	now       func() time.Time
	sleep     func(time.Duration)
	send      func(int, processSignal) error
	isRunning func(int) (bool, error)
}

func (k processGroupKiller) Kill(ctx context.Context, pgid int) error {
	if pgid <= 0 {
		return fmt.Errorf("invalid process group id %d", pgid)
	}
	if err := k.send(pgid, signalTerminate); err != nil {
		return err
	}

	now := k.now
	if now == nil {
		now = time.Now
	}
	deadline := now().Add(k.drain)
	for {
		running, err := k.isRunning(pgid)
		if err != nil {
			return err
		}
		if !running {
			return nil
		}
		if now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		k.sleep(k.poll)
	}

	return k.send(pgid, signalKill)
}
