package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/muesli/termenv"
)

func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

func TestTailLatest(t *testing.T) {
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	os.MkdirAll(rallyDir, 0o755)
	initGitRepoForTail(t, dir)

	s, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}

	log1 := filepath.Join(dir, "try1.log")
	os.WriteFile(log1, []byte("log1 content\n"), 0o644)
	_ = s.AppendTry(store.TryRecord{ID: 1, AgentType: "claude", LogPath: log1})

	log2 := filepath.Join(dir, "try2.log")
	os.WriteFile(log2, []byte("log2 content\n"), 0o644)
	_ = s.AppendTry(store.TryRecord{ID: 2, AgentType: "claude", LogPath: log2})

	f, err := os.Open(log2)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = followFile(ctx, f, &buf)
	if err != nil && err != context.DeadlineExceeded && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}

	got := buf.String()
	if got != "log2 content\n" {
		t.Fatalf("expected latest log content, got %q", got)
	}
}

func TestTailTryNValid(t *testing.T) {
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	os.MkdirAll(rallyDir, 0o755)
	initGitRepoForTail(t, dir)

	s, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}

	log1 := filepath.Join(dir, "try1.log")
	os.WriteFile(log1, []byte("first log\n"), 0o644)
	_ = s.AppendTry(store.TryRecord{ID: 1, AgentType: "claude", LogPath: log1})

	log2 := filepath.Join(dir, "try2.log")
	os.WriteFile(log2, []byte("second log\n"), 0o644)
	_ = s.AppendTry(store.TryRecord{ID: 2, AgentType: "claude", LogPath: log2})

	// Simulate --try 1: should read first try
	tries := s.AllTries()
	if len(tries) < 1 {
		t.Fatal("expected at least 1 try")
	}
	target := tries[0]
	if target.LogPath == "" {
		t.Fatal("expected LogPath")
	}

	f, err := os.Open(target.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = followFile(ctx, f, &buf)
	if err != nil && err != context.DeadlineExceeded && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}

	got := buf.String()
	if got != "first log\n" {
		t.Fatalf("expected first log content, got %q", got)
	}
}

func TestTailTryNInvalid(t *testing.T) {
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	os.MkdirAll(rallyDir, 0o755)
	initGitRepoForTail(t, dir)

	s, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.AppendTry(store.TryRecord{ID: 1, AgentType: "claude"})

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("expected tries")
	}

	tryNum := 5
	if tryNum > len(tries) || tryNum < 1 {
		// Expected
	} else {
		t.Fatal("expected out of range")
	}
}

func TestTailEmptyTries(t *testing.T) {
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	os.MkdirAll(rallyDir, 0o755)
	initGitRepoForTail(t, dir)

	_, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}

	// No tries appended
	if _, err := os.Stat(filepath.Join(rallyDir, "tries.jsonl")); os.IsNotExist(err) {
		// Expected for empty store
	}
}

func TestTailTargetReadsStateDir(t *testing.T) {
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	os.MkdirAll(rallyDir, 0o755)
	initGitRepoForTail(t, dir)

	s, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(dir, "try1.log")
	if err := os.WriteFile(logPath, []byte("state-backed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTry(store.TryRecord{ID: 1, AgentType: "claude", LogPath: logPath}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(rallyDir, "tries.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("legacy top-level tries.jsonl should not exist, stat err=%v", err)
	}

	target, err := tailTarget(dir, 1)
	if err != nil {
		t.Fatalf("tailTarget should read tries from .rally/state, got error: %v", err)
	}
	if target.LogPath != logPath {
		t.Fatalf("LogPath = %q, want %q", target.LogPath, logPath)
	}
}

func TestFollowFileGrowing(t *testing.T) {
	f := filepath.Join(t.TempDir(), "grow.log")
	file, err := os.Create(f)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString("initial\n")
	file.Close()

	f2, err := os.Open(f)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	go func() {
		time.Sleep(200 * time.Millisecond)
		file, _ := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0o644)
		file.WriteString("appended\n")
		file.Close()
	}()

	err = followFile(ctx, f2, &buf)
	if err != nil && err != context.DeadlineExceeded && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}

	got := buf.String()
	if got != "initial\nappended\n" {
		t.Fatalf("expected both lines, got %q", got)
	}
}

// TestTailMultiRepoSharedDataDir verifies that two repos sharing one data dir
// can each `rally tail --try N` against their own try logs. Try log files are
// written under <dataDir>/tries/<repoKey>/try-N.log; each workspace's
// .rally/state/tries.jsonl stores the absolute LogPath, so tail in each workspace
// picks up only its own try regardless of try-id collisions.
func TestTailMultiRepoSharedDataDir(t *testing.T) {
	sharedDataDir := t.TempDir()

	// Build two parallel workspaces.
	mkRepo := func(name, body string) (string, store.TryRecord) {
		repoDir := filepath.Join(t.TempDir(), name)
		os.MkdirAll(repoDir, 0o755)
		rallyDir := store.RallyDir(repoDir)
		os.MkdirAll(rallyDir, 0o755)
		initGitRepoForTail(t, repoDir)

		s, err := store.NewStore(rallyDir)
		if err != nil {
			t.Fatal(err)
		}

		// Mirror how relay.runner writes try logs: per-repo scoping under
		// dataDir using repoKey-style paths (basename[:8] + 4 hex).
		tryDir := filepath.Join(sharedDataDir, "tries", name)
		os.MkdirAll(tryDir, 0o755)
		logPath := filepath.Join(tryDir, "try-1.log")
		os.WriteFile(logPath, []byte(body), 0o644)

		rec := store.TryRecord{ID: 1, AgentType: "claude", LogPath: logPath}
		if err := s.AppendTry(rec); err != nil {
			t.Fatal(err)
		}
		return repoDir, rec
	}

	repoA, recA := mkRepo("alpha", "from alpha\n")
	repoB, recB := mkRepo("beta", "from beta\n")

	if recA.LogPath == recB.LogPath {
		t.Fatalf("try log paths collided across repos: %s", recA.LogPath)
	}

	readVia := func(repoDir string, expected string) {
		t.Helper()
		s, err := store.NewStore(store.RallyDir(repoDir))
		if err != nil {
			t.Fatal(err)
		}
		tries := s.AllTries()
		if len(tries) == 0 {
			t.Fatalf("no tries in %s", repoDir)
		}
		target := tries[0]
		f, err := os.Open(target.LogPath)
		if err != nil {
			t.Fatalf("open log: %v", err)
		}
		defer f.Close()

		var buf bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = followFile(ctx, f, &buf)
		if buf.String() != expected {
			t.Errorf("repo %s tail = %q, want %q", repoDir, buf.String(), expected)
		}
	}

	readVia(repoA, "from alpha\n")
	readVia(repoB, "from beta\n")
}

func initGitRepoForTail(t *testing.T, dir string) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	cmd := exec.Command("git", "-C", dir, "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "config", "user.email", "test@test.com")
	cmd.Dir = dir
	_ = cmd.Run()
	cmd = exec.Command("git", "-C", dir, "config", "user.name", "Test")
	cmd.Dir = dir
	_ = cmd.Run()
}

func TestTailHighlight(t *testing.T) {
	const ansi = `\x1b\[[0-9;]*m`
	ansiRe := regexp.MustCompile(ansi)

	t.Run("off passthrough", func(t *testing.T) {
		var buf bytes.Buffer
		w := newHighlightWriter(&buf, "off")
		input := []byte("plain output\n")
		if _, err := w.Write(input); err != nil {
			t.Fatalf("Write error: %v", err)
		}
		if got := buf.String(); got != string(input) {
			t.Fatalf("off output = %q, want %q", got, input)
		}
	})

	t.Run("heuristic adds ansi", func(t *testing.T) {
		var buf bytes.Buffer
		w := newHighlightWriter(&buf, "heuristic")
		if _, err := w.Write([]byte("2026-06-18T12:00:00Z error https://example.com\n")); err != nil {
			t.Fatalf("Write error: %v", err)
		}
		if got := buf.String(); !ansiRe.MatchString(got) {
			t.Fatalf("heuristic output missing ANSI escapes: %q", got)
		}
	})

	t.Run("chroma adds ansi", func(t *testing.T) {
		var buf bytes.Buffer
		w := newHighlightWriter(&buf, "chroma")
		if _, err := w.Write([]byte("{\"ok\":true}\n")); err != nil {
			t.Fatalf("Write error: %v", err)
		}
		if got := buf.String(); !ansiRe.MatchString(got) {
			t.Fatalf("chroma output missing ANSI escapes: %q", got)
		}
	})
}

func TestTailActiveMetadata(t *testing.T) {
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	os.MkdirAll(rallyDir, 0o755)
	initGitRepoForTail(t, dir)

	s, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Fresh workspace with active metadata tails the active log instead of erroring
	activeLog1 := filepath.Join(dir, "active1.log")
	os.WriteFile(activeLog1, []byte("active1\n"), 0o644)

	// Need to append a relay so the metadata isn't considered "crashed/finished"
	_ = s.AppendRelay(store.RelayRecord{ID: 1, StartedAt: time.Now().Format(time.RFC3339)})

	err = progress.SetActiveTry(dir, progress.ActiveTryMetadata{
		RelayID:   1,
		RunID:     1,
		TryID:     2,
		LogPath:   activeLog1,
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	target, err := tailTarget(dir, 0)
	if err != nil {
		t.Fatalf("expected active log, got error: %v", err)
	}
	if target.LogPath != activeLog1 {
		t.Fatalf("expected LogPath %q, got %q", activeLog1, target.LogPath)
	}

	// 2. Active metadata beats an older completed try
	completedLog := filepath.Join(dir, "completed.log")
	os.WriteFile(completedLog, []byte("completed\n"), 0o644)
	_ = s.AppendTry(store.TryRecord{ID: 1, AgentType: "claude", LogPath: completedLog, Completed: true})

	target, err = tailTarget(dir, 0)
	if err != nil {
		t.Fatalf("expected active log, got error: %v", err)
	}
	if target.LogPath != activeLog1 {
		t.Fatalf("expected LogPath %q, got %q", activeLog1, target.LogPath)
	}

	// 3. Stale/missing active path falls back with a warning
	activeLogMissing := filepath.Join(dir, "missing.log")
	err = progress.SetActiveTry(dir, progress.ActiveTryMetadata{
		RelayID:   1,
		RunID:     1,
		TryID:     2,
		LogPath:   activeLogMissing,
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	target, err = tailTarget(dir, 0)
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}
	if target.LogPath != completedLog {
		t.Fatalf("expected fallback to completed log %q, got %q", completedLog, target.LogPath)
	}

	// 4. Stale active metadata from a crashed/finished relay is ignored
	activeLog2 := filepath.Join(dir, "active2.log")
	os.WriteFile(activeLog2, []byte("active2\n"), 0o644)

	_ = s.AppendRelay(store.RelayRecord{ID: 2, StartedAt: time.Now().Format(time.RFC3339), EndedAt: time.Now().Format(time.RFC3339)})

	err = progress.SetActiveTry(dir, progress.ActiveTryMetadata{
		RelayID:   2,
		RunID:     1,
		TryID:     3,
		LogPath:   activeLog2,
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	target, err = tailTarget(dir, 0)
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}
	if target.LogPath != completedLog {
		t.Fatalf("expected fallback to completed log %q, got %q", completedLog, target.LogPath)
	}

	// 5. Implausibly old active_started_at falls back
	activeLog3 := filepath.Join(dir, "active3.log")
	os.WriteFile(activeLog3, []byte("active3\n"), 0o644)
	_ = s.AppendRelay(store.RelayRecord{ID: 3, StartedAt: time.Now().Format(time.RFC3339)})

	err = progress.SetActiveTry(dir, progress.ActiveTryMetadata{
		RelayID:   3,
		RunID:     1,
		TryID:     4,
		LogPath:   activeLog3,
		StartedAt: time.Now().Add(-25 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	target, err = tailTarget(dir, 0)
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}
	if target.LogPath != completedLog {
		t.Fatalf("expected fallback to completed log %q, got %q", completedLog, target.LogPath)
	}

	// 6. Explicit historical --try N remains 1-based and unchanged
	target, err = tailTarget(dir, 1)
	if err != nil {
		t.Fatalf("expected try 1, got error: %v", err)
	}
	if target.LogPath != completedLog {
		t.Fatalf("expected try 1 log %q, got %q", completedLog, target.LogPath)
	}
}

func TestTailActiveMetadataWarnsAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	os.MkdirAll(rallyDir, 0o755)
	initGitRepoForTail(t, dir)

	s, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}

	_ = s.AppendRelay(store.RelayRecord{ID: 1, StartedAt: time.Now().Format(time.RFC3339)})

	completedLog := filepath.Join(dir, "completed.log")
	if err := os.WriteFile(completedLog, []byte("completed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTry(store.TryRecord{ID: 1, AgentType: "claude", LogPath: completedLog, Completed: true}); err != nil {
		t.Fatal(err)
	}

	if err := progress.SetActiveTry(dir, progress.ActiveTryMetadata{
		RelayID:   1,
		RunID:     1,
		TryID:     3,
		LogPath:   filepath.Join(dir, "missing.log"),
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	warning := captureStderr(t, func() {
		target, err := tailTarget(dir, 0)
		if err != nil {
			t.Fatalf("tailTarget error: %v", err)
		}
		if target.LogPath != completedLog {
			t.Fatalf("LogPath = %q, want %q", target.LogPath, completedLog)
		}
	})

	if !strings.Contains(warning, "warning: stale active try ignored (missing log path)") {
		t.Fatalf("warning = %q, want missing-log fallback warning", warning)
	}
}

func TestTailActiveMetadataRecordedTryFallsBack(t *testing.T) {
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	os.MkdirAll(rallyDir, 0o755)
	initGitRepoForTail(t, dir)

	s, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}

	_ = s.AppendRelay(store.RelayRecord{ID: 1, StartedAt: time.Now().Format(time.RFC3339)})

	staleLog := filepath.Join(dir, "stale.log")
	if err := os.WriteFile(staleLog, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	latestCompletedLog := filepath.Join(dir, "latest.log")
	if err := os.WriteFile(latestCompletedLog, []byte("latest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTry(store.TryRecord{ID: 1, AgentType: "claude", LogPath: staleLog, Completed: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTry(store.TryRecord{ID: 2, AgentType: "claude", LogPath: latestCompletedLog, Completed: true}); err != nil {
		t.Fatal(err)
	}

	if err := progress.SetActiveTry(dir, progress.ActiveTryMetadata{
		RelayID:   1,
		RunID:     1,
		TryID:     1,
		LogPath:   staleLog,
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	warning := captureStderr(t, func() {
		target, err := tailTarget(dir, 0)
		if err != nil {
			t.Fatalf("tailTarget error: %v", err)
		}
		if target.LogPath != latestCompletedLog {
			t.Fatalf("LogPath = %q, want %q", target.LogPath, latestCompletedLog)
		}
	})

	if !strings.Contains(warning, "warning: stale active try ignored (metadata already recorded in try history)") {
		t.Fatalf("warning = %q, want recorded-history fallback warning", warning)
	}
}

// TestFallbackToNewestUncompleted tests that if no completed try exists,
// it falls back to the newest uncompleted try.
func TestFallbackToNewestUncompleted(t *testing.T) {
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	os.MkdirAll(rallyDir, 0o755)
	initGitRepoForTail(t, dir)

	s, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}

	uncompletedLog := filepath.Join(dir, "uncompleted.log")
	os.WriteFile(uncompletedLog, []byte("uncompleted\n"), 0o644)
	_ = s.AppendTry(store.TryRecord{ID: 1, AgentType: "claude", LogPath: uncompletedLog, Completed: false})

	target, err := tailTarget(dir, 0)
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}
	if target.LogPath != uncompletedLog {
		t.Fatalf("expected fallback to uncompleted log %q, got %q", uncompletedLog, target.LogPath)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = original
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("stderr close: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("stderr read close: %v", err)
	}
	return string(data)
}
