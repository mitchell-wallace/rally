package monitor

import (
	"fmt"
	"io"
	"sync"
	"time"
)

const TickInterval = 1 * time.Second

// Indicators carries optional reliability fields for the status line.
// Zero value means "nothing to show".
type Indicators struct {
	Reliability string // e.g. "❄ stalled", "⚠ slowing", "↻ recovered"
	Retry       string // e.g. "retry 2/5"; empty on the first attempt
	// Action is a transient operator-shortcut hint: the "press X again…" prompt
	// after a first press, or the "skipping…/pausing…" echo once a shortcut
	// fires. It rides the in-place status line so feedback never spills onto a
	// new line.
	Action string
}

// Monitor produces a live status line during try execution.
type Monitor struct {
	workspaceDir  string
	logPath       string
	pgid          int
	startTime     time.Time
	netMon        *NetworkMonitor
	cursorUpLines int

	// Reliability state
	stallThreshold time.Duration // 0 = slowing detection disabled
	stalled        bool
	stopping       bool
	recovered      bool
	recoveredTicks int // counts steady ticks after recovery; clears indicator

	// Retry state: rendered inline as "retry N/M" when set.
	retry string

	// Operator-shortcut feedback. armedMsg is the transient "press X again…"
	// hint shown until armedUntil; actingMsg is the "skipping…/pausing…" echo
	// shown once a shortcut fires (and outranks any armed hint).
	armedMsg   string
	armedUntil time.Time
	actingMsg  string

	ticker *time.Ticker
	stopCh chan struct{}
	mu     sync.Mutex
}

// NewMonitor creates a new Monitor.
func NewMonitor(workspaceDir, logPath string, pgid int) *Monitor {
	return &Monitor{
		workspaceDir: workspaceDir,
		logPath:      logPath,
		pgid:         pgid,
		startTime:    time.Now(),
	}
}

// Start begins ticking every 5 seconds and writing status lines to out.
func (m *Monitor) Start(out io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ticker != nil {
		return
	}
	m.startTime = time.Now()
	m.netMon = NewNetworkMonitor(nil)
	m.ticker = time.NewTicker(TickInterval)
	m.stopCh = make(chan struct{})
	ticker := m.ticker
	stopCh := m.stopCh
	go m.run(out, ticker, stopCh)
}

// Stop halts the monitor and prints a newline.
func (m *Monitor) Stop() {
	m.mu.Lock()
	t := m.ticker
	ch := m.stopCh
	m.ticker = nil
	m.mu.Unlock()
	if t != nil {
		t.Stop()
		close(ch)
	}
}

// Tick generates one status line.
func (m *Monitor) Tick() (string, error) {
	elapsed := time.Since(m.startTime).Round(time.Second)

	dirtyCount, err := GitDirtyCount(m.workspaceDir)
	if err != nil {
		dirtyCount = 0
	}

	var lastActivity time.Duration = -1
	if m.logPath != "" {
		if la, err := LogLastActivity(m.logPath); err == nil {
			lastActivity = la
		}
	}

	if lastActivity > elapsed {
		lastActivity = elapsed
	}

	var warnings []string
	if m.netMon != nil {
		m.UpdatePIDs()
		warnings = m.netMon.Check()
	}

	m.mu.Lock()
	ind := m.computeIndicators(lastActivity)
	m.mu.Unlock()

	return RenderStatusExt(elapsed, dirtyCount, lastActivity, warnings, ind), nil
}

// computeIndicators returns the current reliability and token indicators.
// Must be called with m.mu held.
func (m *Monitor) computeIndicators(lastActivity time.Duration) Indicators {
	var ind Indicators

	ind.Retry = m.retry

	// Operator-shortcut feedback: a fired action ("skipping…") outranks a
	// pending "press X again…" hint, which is cleared once its window lapses.
	if m.actingMsg != "" {
		ind.Action = "■ " + m.actingMsg
	} else if m.armedMsg != "" {
		if time.Now().Before(m.armedUntil) {
			ind.Action = "⌨ " + m.armedMsg
		} else {
			m.armedMsg = ""
		}
	}

	// Reliability indicator priority: stopping > stalled > recovered > slowing.
	// "stopping…" wins so a quit-now cancel drain never looks frozen, even if
	// the try was already flagged stalled when the operator pressed Ctrl+C.
	if m.stopping {
		ind.Reliability = "■ stopping…"
	} else if m.stalled {
		ind.Reliability = "❄ stalled"
	} else if m.recovered {
		m.recoveredTicks++
		if m.recoveredTicks > 1 {
			// Clear after one full steady-state tick
			m.recovered = false
			m.recoveredTicks = 0
		} else {
			ind.Reliability = "↻ recovered"
		}
	} else if m.stallThreshold > 0 && lastActivity >= 0 {
		slowingAt := time.Duration(float64(m.stallThreshold) * 0.6)
		if lastActivity >= slowingAt {
			ind.Reliability = "⚠ slowing"
		}
	}

	return ind
}

func (m *Monitor) run(out io.Writer, ticker *time.Ticker, stopCh chan struct{}) {
	for {
		select {
		case <-stopCh:
			m.clear(out)
			return
		case <-ticker.C:
			line, err := m.Tick()
			if err != nil {
				continue
			}
			m.render(out, line)
		}
	}
}

// UpdatePIDs refreshes the PID list for the monitor's process group.
func (m *Monitor) UpdatePIDs() {
	m.mu.Lock()
	pgid := m.pgid
	m.mu.Unlock()
	if pgid <= 0 {
		return
	}
	pids, err := GetPIDsInGroup(pgid)
	if err != nil {
		return
	}
	m.mu.Lock()
	if m.netMon != nil {
		m.netMon.pids = pids
	}
	m.mu.Unlock()
}

// SetProcessGroupID attaches the monitor to a process group after the child starts.
func (m *Monitor) SetProcessGroupID(pgid int) {
	m.mu.Lock()
	m.pgid = pgid
	m.mu.Unlock()
}

// SetStallThreshold enables the "slowing" indicator at ≥60% of threshold.
func (m *Monitor) SetStallThreshold(d time.Duration) {
	m.mu.Lock()
	m.stallThreshold = d
	m.mu.Unlock()
}

// SetRetry sets the inline "retry N/M" status field from the current attempt
// and retry budget. The field is shown only while retrying (attempt > 1); the
// first attempt and any non-positive budget clear it. This replaces printing a
// separate console block per retry attempt.
func (m *Monitor) SetRetry(attempt, maxAttempts int) {
	m.mu.Lock()
	if attempt > 1 && maxAttempts > 0 {
		m.retry = fmt.Sprintf("retry %d/%d", attempt, maxAttempts)
	} else {
		m.retry = ""
	}
	m.mu.Unlock()
}

// SetStalled marks the active try as stalled.
func (m *Monitor) SetStalled(v bool) {
	m.mu.Lock()
	m.stalled = v
	m.mu.Unlock()
}

// SetStopping marks the active try as being cancelled by a quit-now shortcut,
// surfacing a "stopping…" indicator so the cancel drain never looks frozen. It
// takes priority over the stalled indicator. Stopping is a fired action, so it
// also clears any pending "press X again…" hint.
func (m *Monitor) SetStopping(v bool) {
	m.mu.Lock()
	m.stopping = v
	if v {
		m.armedMsg = ""
	}
	m.mu.Unlock()
}

// SetArmed shows a transient "press X again…" hint for ttl, registering that a
// double-press shortcut's first press landed. A later SetActing/SetStopping
// (the shortcut firing) or the ttl lapsing clears it.
func (m *Monitor) SetArmed(msg string, ttl time.Duration) {
	m.mu.Lock()
	m.armedMsg = msg
	m.armedUntil = time.Now().Add(ttl)
	m.mu.Unlock()
}

// SetActing shows the "skipping…/pausing…" echo once a shortcut fires, so the
// operator sees exactly which action was taken. It clears any armed hint.
func (m *Monitor) SetActing(msg string) {
	m.mu.Lock()
	m.actingMsg = msg
	m.armedMsg = ""
	m.mu.Unlock()
}

// SetRecovered marks a stall-driven resume as recovered.  The indicator is
// shown for one steady-state tick, then cleared automatically.
func (m *Monitor) SetRecovered() {
	m.mu.Lock()
	m.recovered = true
	m.recoveredTicks = 0
	m.mu.Unlock()
}

// SetCursorUpLines reserves the given number of lines above the current cursor
// for monitor updates.
func (m *Monitor) SetCursorUpLines(lines int) {
	m.mu.Lock()
	m.cursorUpLines = lines
	m.mu.Unlock()
}
