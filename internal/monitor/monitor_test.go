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
			wantContains: []string{"⏱ 5m 34s", "📁 11 files", "last activity: 4s"},
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
			name:         "frozen",
			ind:          Indicators{Reliability: "❄ frozen"},
			wantContains: []string{"❄ frozen"},
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

func TestMonitorSlowingIndicator(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Create a log file with a stale mtime to simulate silence.
	logPath := filepath.Join(dir, "try.log")
	os.WriteFile(logPath, []byte("data"), 0o644)

	// Set mtime 120s in the past (≥60% of 180s threshold = 108s)
	past := time.Now().Add(-120 * time.Second)
	os.Chtimes(logPath, past, past)

	m := NewMonitor(dir, logPath, 0)
	m.SetFreezeThreshold(180 * time.Second)

	line, err := m.Tick()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsString(line, "⚠ slowing") {
		t.Errorf("expected slowing indicator, got %q", line)
	}
}

func TestMonitorFrozenIndicator(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	logPath := filepath.Join(dir, "try.log")
	os.WriteFile(logPath, []byte("data"), 0o644)

	m := NewMonitor(dir, logPath, 0)
	m.SetFrozen(true)

	line, err := m.Tick()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsString(line, "❄ frozen") {
		t.Errorf("expected frozen indicator, got %q", line)
	}
	// Frozen takes priority over slowing
	if containsString(line, "⚠ slowing") {
		t.Errorf("frozen should take priority over slowing, got %q", line)
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
