package monitor

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RenderStatus formats a status line.
func RenderStatus(elapsed time.Duration, dirtyCount int, lastActivity time.Duration, warnings []string) string {
	elapsedStr := formatDuration(elapsed)
	activityStr := "—"
	if lastActivity >= 0 {
		activityStr = formatDuration(lastActivity)
	}

	parts := []string{
		fmt.Sprintf("⏱ %s", elapsedStr),
		fmt.Sprintf("📁 %d file%s", dirtyCount, plural(dirtyCount)),
		fmt.Sprintf("last activity: %s", activityStr),
	}

	line := strings.Join(parts, "  │  ")
	if len(warnings) > 0 {
		line += "  │  " + strings.Join(warnings, " ")
	}
	return line
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// GitDirtyCount returns the number of dirty files in a git repository.
func GitDirtyCount(dir string) (int, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return 0, nil // not a git repo or error -> 0
	}
	lines := strings.Split(string(out), "\n")
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, nil
}

// LogLastActivity returns the time since the log file was last modified.
func LogLastActivity(logPath string) (time.Duration, error) {
	info, err := os.Stat(logPath)
	if err != nil {
		return 0, err
	}
	return time.Since(info.ModTime()).Round(time.Second), nil
}

// GetPIDsInGroup returns all PIDs that belong to the given process group.
func GetPIDsInGroup(pgid int) ([]int, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		pgidFromProc, err := readPGID(pid)
		if err != nil {
			continue
		}
		if pgidFromProc == pgid {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func readPGID(pid int) (int, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 5 {
		return 0, fmt.Errorf("unexpected stat format")
	}
	// The process group is the 5th field (index 4).
	return strconv.Atoi(fields[4])
}

// CountTCPConnections counts established TCP connections from /proc/net/tcp.
func CountTCPConnections(pids []int) (int, error) {
	if runtime.GOOS != "linux" {
		return 0, nil
	}
	if len(pids) == 0 {
		return 0, nil
	}

	socketInodes, err := socketInodesForPIDs(pids)
	if err != nil {
		return 0, err
	}
	if len(socketInodes) == 0 {
		return 0, nil
	}

	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(data), "\n")
	count := 0
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// State is the 4th field (index 3). 01 = ESTABLISHED.
		if fields[3] == "01" && len(fields) > 9 {
			if _, ok := socketInodes[fields[9]]; ok {
				count++
			}
		}
	}
	return count, nil
}

func socketInodesForPIDs(pids []int) (map[string]struct{}, error) {
	inodes := make(map[string]struct{})
	for _, pid := range pids {
		fdDir := fmt.Sprintf("/proc/%d/fd", pid)
		entries, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			target, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
			if err != nil {
				continue
			}
			if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			if inode != "" {
				inodes[inode] = struct{}{}
			}
		}
	}
	return inodes, nil
}

// ReadIOBytes returns the cumulative read+write bytes for a list of PIDs.
func ReadIOBytes(pids []int) (uint64, error) {
	if runtime.GOOS != "linux" {
		return 0, nil
	}
	var total uint64
	for _, pid := range pids {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/io", pid))
		if err != nil {
			continue // PID may have exited
		}
		var rbytes, wbytes uint64
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "read_bytes:") {
				v, _ := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "read_bytes:")), 10, 64)
				rbytes = v
			}
			if strings.HasPrefix(line, "write_bytes:") {
				v, _ := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "write_bytes:")), 10, 64)
				wbytes = v
			}
		}
		total += rbytes + wbytes
	}
	return total, nil
}

// NetworkMonitor tracks network state and produces warnings.
type NetworkMonitor struct {
	pids         []int
	lastConnTime time.Time
	lastIOTime   time.Time
	lastIOBytes  uint64
}

// NewNetworkMonitor creates a NetworkMonitor for the given PIDs.
func NewNetworkMonitor(pids []int) *NetworkMonitor {
	now := time.Now()
	return &NetworkMonitor{
		pids:         pids,
		lastConnTime: now,
		lastIOTime:   now,
	}
}

func (n *NetworkMonitor) evaluate(now time.Time, conns int, ioBytes uint64) []string {
	if conns > 0 {
		n.lastConnTime = now
	}
	if ioBytes > n.lastIOBytes {
		n.lastIOTime = now
		n.lastIOBytes = ioBytes
	}

	var warnings []string
	if conns == 0 && now.Sub(n.lastConnTime) >= 30*time.Second {
		warnings = append(warnings, "No TCP… (30s)")
	}
	if conns > 0 && now.Sub(n.lastIOTime) >= 30*time.Second {
		warnings = append(warnings, "No network I/O… (30s)")
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
	ioBytes, err := ReadIOBytes(n.pids)
	if err != nil {
		return nil
	}
	return n.evaluate(time.Now(), conns, ioBytes)
}

// Monitor produces a live status line during try execution.
type Monitor struct {
	workspaceDir  string
	logPath       string
	pgid          int
	startTime     time.Time
	netMon        *NetworkMonitor
	cursorUpLines int

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
	m.ticker = time.NewTicker(5 * time.Second)
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

	var warnings []string
	if m.netMon != nil {
		m.UpdatePIDs()
		warnings = m.netMon.Check()
	}

	return RenderStatus(elapsed, dirtyCount, lastActivity, warnings), nil
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

// SetCursorUpLines reserves the given number of lines above the current cursor
// for monitor updates.
func (m *Monitor) SetCursorUpLines(lines int) {
	m.mu.Lock()
	m.cursorUpLines = lines
	m.mu.Unlock()
}

func (m *Monitor) render(out io.Writer, line string) {
	m.mu.Lock()
	cursorUpLines := m.cursorUpLines
	m.mu.Unlock()

	if cursorUpLines <= 0 {
		fmt.Fprintf(out, "\r%s", line)
		return
	}
	fmt.Fprintf(out, "\x1b[%dA\r\x1b[2K%s\x1b[%dB\r", cursorUpLines, line, cursorUpLines)
}

func (m *Monitor) clear(out io.Writer) {
	m.mu.Lock()
	cursorUpLines := m.cursorUpLines
	m.mu.Unlock()

	if cursorUpLines <= 0 {
		fmt.Fprint(out, "\n")
		return
	}
	fmt.Fprintf(out, "\x1b[%dA\r", cursorUpLines)
	for i := 0; i < cursorUpLines; i++ {
		fmt.Fprint(out, "\x1b[2K")
		if i < cursorUpLines-1 {
			fmt.Fprint(out, "\n")
		}
	}
	fmt.Fprint(out, "\r\n")
}
