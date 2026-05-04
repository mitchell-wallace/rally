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
	// Test the logic directly by manipulating fields
	nm := NewNetworkMonitor([]int{1})

	// Initially no warnings
	warnings := nm.Check()
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings initially, got %v", warnings)
	}

	// Simulate having seen connections and IO
	nm.hasSeenConn = true
	nm.hasSeenIO = true
	nm.lastConnTime = time.Now().Add(-31 * time.Second)
	nm.lastIOTime = time.Now().Add(-31 * time.Second)

	// On non-Linux, Check should always return nil
	if runtime.GOOS != "linux" {
		warnings = nm.Check()
		if len(warnings) != 0 {
			t.Fatalf("expected no warnings on non-linux, got %v", warnings)
		}
		return
	}

	// On Linux, we can't easily mock /proc, but we can at least verify
	// the function runs without panic and returns consistent results.
	warnings = nm.Check()
	// It may or may not return warnings depending on actual system state.
	// We just verify it's a slice.
	_ = warnings
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
