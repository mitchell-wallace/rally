package monitor

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRenderStatus(t *testing.T) {
	tests := []struct {
		name         string
		elapsed      time.Duration
		dirtyCount   int
		lastActivity time.Duration
		warnings     []string
		wantContains []string
	}{
		{
			name:         "basic",
			elapsed:      5*time.Minute + 34*time.Second,
			dirtyCount:   11,
			lastActivity: 4 * time.Second,
			warnings:     nil,
			wantContains: []string{"⏱ 5m 34s", "📁 11 files", "last activity: < 1m ago"},
		},
		{
			name:         "no last activity",
			elapsed:      1 * time.Minute,
			dirtyCount:   0,
			lastActivity: -1,
			warnings:     nil,
			wantContains: []string{"⏱ 1m 00s", "📁 0 files", "last activity: —"},
		},
		{
			name:         "last activity over a minute",
			elapsed:      3 * time.Minute,
			dirtyCount:   2,
			lastActivity: 90 * time.Second,
			warnings:     nil,
			wantContains: []string{"last activity: 1m ago"},
		},
		{
			name:         "with warnings",
			elapsed:      10 * time.Minute,
			dirtyCount:   3,
			lastActivity: 30 * time.Second,
			warnings:     []string{"No TCP… (30s)"},
			wantContains: []string{"⏱ 10m 00s", "No TCP… (30s)"},
		},
		{
			name:         "singular file",
			elapsed:      30 * time.Second,
			dirtyCount:   1,
			lastActivity: 2 * time.Second,
			warnings:     nil,
			wantContains: []string{"📁 1 file"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderStatus(tc.elapsed, tc.dirtyCount, tc.lastActivity, tc.warnings)
			for _, want := range tc.wantContains {
				if !containsString(got, want) {
					t.Errorf("expected %q to contain %q", got, want)
				}
			}
		})
	}
}

func TestRenderStatusExtIndicators(t *testing.T) {
	base := func(ind Indicators) string {
		return RenderStatusExt(
			2*time.Minute+30*time.Second, 5, 10*time.Second, nil, ind,
		)
	}

	tests := []struct {
		name         string
		ind          Indicators
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "no indicators",
			ind:          Indicators{},
			wantContains: []string{"⏱ 2m 30s", "📁 5 files"},
			wantAbsent:   []string{"❄", "⚠", "↻", "tok"},
		},
		{
			name:         "stalled",
			ind:          Indicators{Reliability: "❄ stalled"},
			wantContains: []string{"❄ stalled"},
		},
		{
			name:         "slowing",
			ind:          Indicators{Reliability: "⚠ slowing"},
			wantContains: []string{"⚠ slowing"},
		},
		{
			name:         "recovered",
			ind:          Indicators{Reliability: "↻ recovered"},
			wantContains: []string{"↻ recovered"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := base(tc.ind)
			for _, want := range tc.wantContains {
				if !containsString(got, want) {
					t.Errorf("expected %q to contain %q", got, want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if containsString(got, absent) {
					t.Errorf("expected %q NOT to contain %q", got, absent)
				}
			}
		})
	}
}

func TestRenderStatusExtRetryField(t *testing.T) {
	got := RenderStatusExt(
		2*time.Minute, 1, 5*time.Second, nil, Indicators{Retry: "retry 3/5"},
	)
	if !containsString(got, "retry 3/5") {
		t.Errorf("expected status line to contain inline retry field, got %q", got)
	}

	// No retry field on a plain status line.
	plain := RenderStatusExt(2*time.Minute, 1, 5*time.Second, nil, Indicators{})
	if containsString(plain, "retry") {
		t.Errorf("expected no retry field when unset, got %q", plain)
	}
}

func TestMonitorSetRetry(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	logPath := filepath.Join(dir, "try.log")
	os.WriteFile(logPath, []byte("data"), 0o644)

	m := NewMonitor(dir, logPath, 0)

	// First attempt: no retry field.
	m.SetRetry(1, 5)
	line, err := m.Tick()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsString(line, "retry") {
		t.Errorf("expected no retry field on attempt 1, got %q", line)
	}

	// Retrying: inline retry N/M field appears.
	m.SetRetry(2, 5)
	line, err = m.Tick()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsString(line, "retry 2/5") {
		t.Errorf("expected inline 'retry 2/5' field, got %q", line)
	}

	// A non-positive budget clears the field.
	m.SetRetry(3, 0)
	line, err = m.Tick()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsString(line, "retry") {
		t.Errorf("expected retry field cleared for non-positive budget, got %q", line)
	}
}

func TestMonitorSlowingIndicator(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	logPath := filepath.Join(dir, "try.log")
	os.WriteFile(logPath, []byte("data"), 0o644)

	// Set mtime 560s in the past (≥60% of 900s threshold = 540s)
	past := time.Now().Add(-560 * time.Second)
	os.Chtimes(logPath, past, past)

	m := NewMonitor(dir, logPath, 0)
	m.SetStallThreshold(900 * time.Second)

	line, err := m.Tick()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsString(line, "⚠ slowing") {
		t.Errorf("expected slowing indicator, got %q", line)
	}
}

func TestMonitorSlowingWindowDerivedAt06x(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	logPath := filepath.Join(dir, "try.log")
	os.WriteFile(logPath, []byte("data"), 0o644)

	m := NewMonitor(dir, logPath, 0)
	m.SetStallThreshold(900 * time.Second)

	// 0.6 × 900s = 540s. Activity at 530s should NOT trigger slowing.
	past := time.Now().Add(-530 * time.Second)
	os.Chtimes(logPath, past, past)

	line, err := m.Tick()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsString(line, "⚠ slowing") {
		t.Errorf("slowing should NOT appear at 530s (below 540s window), got %q", line)
	}

	// Activity at 550s SHOULD trigger slowing (≥540s).
	past = time.Now().Add(-550 * time.Second)
	os.Chtimes(logPath, past, past)

	line, err = m.Tick()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsString(line, "⚠ slowing") {
		t.Errorf("expected slowing indicator at 550s (≥540s = 0.6×900s), got %q", line)
	}
}

func TestMonitorSlowingNotShownDuringNormalReasoning(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	logPath := filepath.Join(dir, "try.log")
	os.WriteFile(logPath, []byte("data"), 0o644)

	m := NewMonitor(dir, logPath, 0)
	m.SetStallThreshold(900 * time.Second)

	// 4m silence (240s) is well within a normal reasoning burst (≪ 540s window).
	past := time.Now().Add(-240 * time.Second)
	os.Chtimes(logPath, past, past)

	line, err := m.Tick()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsString(line, "⚠ slowing") {
		t.Errorf("slowing should NOT appear during normal reasoning (240s < 540s), got %q", line)
	}
}

func TestMonitorStalledIndicator(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	logPath := filepath.Join(dir, "try.log")
	os.WriteFile(logPath, []byte("data"), 0o644)

	m := NewMonitor(dir, logPath, 0)
	m.SetStalled(true)

	line, err := m.Tick()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsString(line, "❄ stalled") {
		t.Errorf("expected stalled indicator, got %q", line)
	}
	// Stalled takes priority over slowing
	if containsString(line, "⚠ slowing") {
		t.Errorf("stalled should take priority over slowing, got %q", line)
	}
}

func TestMonitorRecoveredIndicator(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	logPath := filepath.Join(dir, "try.log")
	os.WriteFile(logPath, []byte("data"), 0o644)

	m := NewMonitor(dir, logPath, 0)
	m.SetRecovered()

	// First tick should show recovered
	line, err := m.Tick()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsString(line, "↻ recovered") {
		t.Errorf("expected recovered indicator on first tick, got %q", line)
	}

	// Second tick should clear recovered
	line, err = m.Tick()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsString(line, "↻ recovered") {
		t.Errorf("expected recovered indicator to clear after one tick, got %q", line)
	}
}

func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGitDirtyCount(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	count, err := GitDirtyCount(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 dirty files, got %d", count)
	}

	// Create a file
	f := filepath.Join(dir, "new.txt")
	os.WriteFile(f, []byte("hello"), 0o644)

	count, err = GitDirtyCount(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 dirty file, got %d", count)
	}
}

func TestLogLastActivity(t *testing.T) {
	f := filepath.Join(t.TempDir(), "test.log")
	os.WriteFile(f, []byte("hello"), 0o644)

	// Small sleep to ensure some time has passed
	time.Sleep(50 * time.Millisecond)

	d, err := LogLastActivity(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d < 0 || d > 2*time.Second {
		t.Fatalf("unexpected duration: %v", d)
	}

	_, err = LogLastActivity("/nonexistent")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestNetworkMonitorCheck(t *testing.T) {
	nm := NewNetworkMonitor([]int{1})
	base := time.Now()
	nm.lastConnTime = base
	nm.lastSyscallTime = base

	if warnings := nm.evaluate(base.Add(31*time.Second), 0, 0); len(warnings) != 1 || warnings[0] != "No TCP… (30s)" {
		t.Fatalf("expected TCP warning, got %v", warnings)
	}

	nm = NewNetworkMonitor([]int{1})
	nm.lastConnTime = base
	nm.lastSyscallTime = base
	nm.lastSyscallBytes = 10
	if warnings := nm.evaluate(base.Add(31*time.Second), 2, 10); len(warnings) != 1 || warnings[0] != "No I/O… (30s)" {
		t.Fatalf("expected I/O warning, got %v", warnings)
	}

	if warnings := nm.evaluate(base.Add(32*time.Second), 2, 11); len(warnings) != 0 {
		t.Fatalf("expected warning to clear after activity resumes, got %v", warnings)
	}
}

func TestNetworkMonitorCheckNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux behavior covered by evaluate tests")
	}
	nm := NewNetworkMonitor([]int{1})
	if warnings := nm.Check(); len(warnings) != 0 {
		t.Fatalf("expected no warnings on non-linux, got %v", warnings)
	}
}

func TestMonitorTick(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	logPath := filepath.Join(dir, "try.log")
	os.WriteFile(logPath, []byte("log"), 0o644)

	m := NewMonitor(dir, logPath, 0)
	line, err := m.Tick()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if line == "" {
		t.Fatal("expected non-empty status line")
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	execGit(t, dir, "init")
	execGit(t, dir, "config", "user.email", "test@test.com")
	execGit(t, dir, "config", "user.name", "Test")
}

func execGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
