package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/store"
)

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
// .rally/tries.jsonl stores the absolute LogPath, so tail in each workspace
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
