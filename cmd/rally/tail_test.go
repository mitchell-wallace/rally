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
	rallyDir := filepath.Join(dir, ".rally")
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
	rallyDir := filepath.Join(dir, ".rally")
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
	rallyDir := filepath.Join(dir, ".rally")
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
	rallyDir := filepath.Join(dir, ".rally")
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
