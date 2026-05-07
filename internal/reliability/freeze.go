package reliability

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/mitchell-wallace/rally/internal/monitor"
)

const (
	DefaultFreezeThreshold = 180 * time.Second
	DefaultFreezeTick      = 5 * time.Second
	defaultKillDrain       = 5 * time.Second
	defaultKillPoll        = 100 * time.Millisecond
)

var errProcessGroupUnavailable = errors.New("freeze detector waiting for process group")

type SignalSnapshot struct {
	Now          time.Time
	LogSilentFor time.Duration
	Connections  int
	IOBytes      uint64
}

type Assessment struct {
	Frozen    bool
	Ambiguous bool
}

type Detector struct {
	threshold    time.Duration
	platform     string
	lastIOBytes  uint64
	lastIOChange time.Time
	initialized  bool
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
		}
		if snapshot.IOBytes != d.lastIOBytes {
			d.lastIOBytes = snapshot.IOBytes
			d.lastIOChange = snapshot.Now
		}
		ioSilentFor := snapshot.Now.Sub(d.lastIOChange)
		return Assessment{
			Frozen: snapshot.LogSilentFor >= d.threshold &&
				snapshot.Connections == 0 &&
				ioSilentFor >= d.threshold,
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
	return &freezeController{
		logPath:  logPath,
		detector: NewDetector(threshold),
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

	snapshot.Connections = conns
	snapshot.IOBytes = ioBytes
	return snapshot, nil
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
