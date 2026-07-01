package runner

import (
	"context"
	"fmt"
	"github.com/mitchell-wallace/rally/internal/agent"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestFilesChangedListUsesCommitDiff(t *testing.T) {
	workspaceDir := t.TempDir()
	initRepo(t, workspaceDir)

	os.WriteFile(filepath.Join(workspaceDir, "one.txt"), []byte("one\n"), 0o644)
	os.WriteFile(filepath.Join(workspaceDir, "two.txt"), []byte("two\n"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "init")

	before := strings.TrimSpace(runGit(t, workspaceDir, "rev-parse", "HEAD"))

	os.WriteFile(filepath.Join(workspaceDir, "one.txt"), []byte("one changed\n"), 0o644)
	os.WriteFile(filepath.Join(workspaceDir, "two.txt"), []byte("two changed\n"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "change two files")

	after := strings.TrimSpace(runGit(t, workspaceDir, "rev-parse", "HEAD"))

	r := &Runner{cfg: Config{WorkspaceDir: workspaceDir}}
	got := r.filesChangedList(nil, before, after, after)
	if len(got) != 2 {
		t.Fatalf("expected 2 changed files from commit diff, got %d (%v)", len(got), got)
	}
	wantSet := map[string]bool{"one.txt": true, "two.txt": true}
	for _, p := range got {
		if !wantSet[p] {
			t.Fatalf("unexpected path %q in %v", p, got)
		}
	}
}

func TestFilesChangedListFallsBackToDirtyFiles(t *testing.T) {
	workspaceDir := t.TempDir()
	initRepo(t, workspaceDir)

	os.WriteFile(filepath.Join(workspaceDir, "seed.txt"), []byte("seed\n"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "init")

	// Dirty the workspace without committing. Mix in a .rally/ file that
	// should be filtered out of the result.
	os.WriteFile(filepath.Join(workspaceDir, "user.txt"), []byte("user change\n"), 0o644)
	if err := os.MkdirAll(store.RallyDir(workspaceDir), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(store.RunStatePath(workspaceDir), []byte("{}"), 0o644)

	r := &Runner{cfg: Config{WorkspaceDir: workspaceDir}}
	got := r.filesChangedList(nil, "", "", "")
	if len(got) != 1 || got[0] != "user.txt" {
		t.Fatalf("expected only user.txt in fallback list, got %v", got)
	}
}

func TestFilesChangedListExcludesClaudeSettings(t *testing.T) {
	workspaceDir := t.TempDir()
	initRepo(t, workspaceDir)

	os.WriteFile(filepath.Join(workspaceDir, "seed.txt"), []byte("seed\n"), 0o644)
	os.MkdirAll(filepath.Join(workspaceDir, ".claude"), 0o755)
	os.WriteFile(filepath.Join(workspaceDir, ".claude", "settings.local.json"), []byte("{}\n"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "init")

	os.WriteFile(filepath.Join(workspaceDir, ".claude", "settings.local.json"), []byte("{\"changed\":true}\n"), 0o644)

	r := &Runner{cfg: Config{WorkspaceDir: workspaceDir}}
	got := r.filesChangedList(nil, "", "", "")
	if len(got) != 0 {
		t.Fatalf("expected empty list when only .claude/settings.local.json is dirty, got %v", got)
	}
}

func TestFilesChangedListExcludesAllTransientPaths(t *testing.T) {
	workspaceDir := t.TempDir()
	initRepo(t, workspaceDir)

	os.WriteFile(filepath.Join(workspaceDir, "seed.txt"), []byte("seed\n"), 0o644)
	os.MkdirAll(filepath.Join(workspaceDir, ".claude"), 0o755)
	os.WriteFile(filepath.Join(workspaceDir, ".claude", "settings.local.json"), []byte("{}\n"), 0o644)
	os.MkdirAll(store.RallyDir(workspaceDir), 0o755)
	os.WriteFile(store.RunStatePath(workspaceDir), []byte("{}"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "init")

	os.WriteFile(filepath.Join(workspaceDir, "user.txt"), []byte("change\n"), 0o644)
	os.WriteFile(store.RunStatePath(workspaceDir), []byte("{\"changed\":true}"), 0o644)
	os.MkdirAll(filepath.Join(workspaceDir, ".laps"), 0o755)
	os.WriteFile(filepath.Join(workspaceDir, ".laps", "laps.json"), []byte("{\"changed\":true}\n"), 0o644)
	os.WriteFile(filepath.Join(workspaceDir, ".claude", "settings.local.json"), []byte("{\"changed\":true}\n"), 0o644)

	r := &Runner{cfg: Config{WorkspaceDir: workspaceDir}}
	got := r.filesChangedList(nil, "", "", "")
	if len(got) != 1 || got[0] != "user.txt" {
		t.Fatalf("expected only user.txt, got %v", got)
	}
}

func TestCommitHashTracking_AgentCommitted(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	// Copy fixture project into workspace
	CopyFixtureProject(t, workspaceDir)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	diffPath, _ := filepath.Abs(filepath.Join("..", "..", "..", "testdata", "diffs", "add-feature.diff"))
	outputPath, _ := filepath.Abs(filepath.Join("..", "..", "..", "testdata", "outputs", "success.json"))
	exec := &agent.FixtureExecutor{
		DiffPath:   diffPath,
		OutputPath: outputPath,
		Dir:        workspaceDir,
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("expected 1 try, got %d", len(tries))
	}
	if tries[0].CommitHash == "" {
		t.Fatal("expected agent commit hash")
	}
}

func TestCommitHashTracking_AutoCommitted(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			// Create a file but don't commit it
			f, err := os.Create(filepath.Join(workspaceDir, "auto.txt"))
			if err != nil {
				return nil, err
			}
			f.WriteString("auto")
			f.Close()
			return &harnessapi.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("expected 1 try, got %d", len(tries))
	}
	if tries[0].CommitHash == "" {
		t.Fatal("expected auto-commit hash")
	}
}

func TestCommitHistoryTracking_MultipleAgentCommits(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	// Seed an initial commit so headBefore is non-empty.
	os.WriteFile(filepath.Join(workspaceDir, "seed.txt"), []byte("seed"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "initial", "--no-verify")
	seedHash := strings.TrimSpace(runGit(t, workspaceDir, "rev-parse", "HEAD"))

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			// Make three distinct commits within a single try.
			for i := 1; i <= 3; i++ {
				name := fmt.Sprintf("file%d.txt", i)
				if err := os.WriteFile(filepath.Join(workspaceDir, name), []byte("x"), 0o644); err != nil {
					return nil, err
				}
				runGit(t, workspaceDir, "add", ".")
				runGit(t, workspaceDir, "commit", "-m", fmt.Sprintf("commit %d", i), "--no-verify")
			}
			return &harnessapi.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	// Re-read from tries.jsonl (not just the in-memory cache) to prove the full
	// ordered history survives the round-trip to disk.
	reloaded := newTestStore(t, rallyDir)
	tries := reloaded.AllTries()
	if len(tries) != 1 {
		t.Fatalf("expected 1 try, got %d", len(tries))
	}
	got := tries[0]
	if len(got.CommitHistory) != 3 {
		t.Fatalf("expected 3 commits in CommitHistory, got %d: %v", len(got.CommitHistory), got.CommitHistory)
	}

	// The agent's three commits are the first three after the seed, in order.
	// A run that never finalizes (no laps wrapup, as here) writes a stub
	// summary.jsonl after its commits; the end-of-relay failover then commits
	// that leftover as a trailing "rally: commit leftover summary" commit, so it
	// sits after the agent commits and must not be mistaken for one.
	afterSeed := commitRangeAfter(t, workspaceDir, seedHash)
	if len(afterSeed) < 3 {
		t.Fatalf("expected at least 3 commits after seed, got %v", afterSeed)
	}
	wantHashes := afterSeed[:3]
	for i, want := range wantHashes {
		if got.CommitHistory[i] != want {
			t.Errorf("CommitHistory[%d] = %q, want %q", i, got.CommitHistory[i], want)
		}
	}

	// CommitHash backward compat: equals the last element of the history.
	if got.CommitHash != got.CommitHistory[len(got.CommitHistory)-1] {
		t.Errorf("CommitHash = %q, want last history element %q", got.CommitHash, got.CommitHistory[len(got.CommitHistory)-1])
	}

	// The leftover stub summary was committed by the failover, not left dirty.
	if subject := gitSubject(t, workspaceDir, "HEAD"); subject != "rally: commit leftover summary" {
		t.Errorf("HEAD subject = %q, want the failover commit", subject)
	}
	if dirty := strings.TrimSpace(runGit(t, workspaceDir, "status", "--porcelain")); dirty != "" {
		t.Errorf("working tree should be clean after the failover, got:\n%s", dirty)
	}
}

// commitRangeAfter returns the commit hashes reachable from HEAD but not from
// ref, oldest first.
func commitRangeAfter(t *testing.T, dir, ref string) []string {
	t.Helper()
	out := runGit(t, dir, "rev-list", "--reverse", ref+"..HEAD")
	var hashes []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if h := strings.TrimSpace(line); h != "" {
			hashes = append(hashes, h)
		}
	}
	return hashes
}

// gitSubject returns the subject line of the commit at ref.
func gitSubject(t *testing.T, dir, ref string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, dir, "log", "-1", "--format=%s", ref))
}

// commitHashesInOrder returns the most recent n commit hashes, oldest first.
func commitHashesInOrder(t *testing.T, dir string, n int) []string {
	t.Helper()
	out := runGit(t, dir, "rev-list", "--reverse", fmt.Sprintf("-%d", n), "HEAD")
	var hashes []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if h := strings.TrimSpace(line); h != "" {
			hashes = append(hashes, h)
		}
	}
	return hashes
}

func TestCommitHashTracking_NoChanges(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Millisecond,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   time.Now,
	}

	_ = r.Run(context.Background())

	// No-op tries (no changes + <3min) are treated as failures and retried up to 3x.
	// With the fix, failed runs don't count toward target, so the relay runs
	// hourly retries after pausing. Initial run has 3 tries; hourly retries have 1 each.
	tries := s.AllTries()
	if len(tries) <= 3 {
		t.Fatalf("expected > 3 tries (initial 3 + hourly retries), got %d", len(tries))
	}
	// First 3 tries should have no commit hash
	for i := 0; i < 3; i++ {
		if tries[i].CommitHash != "" {
			t.Fatalf("try %d expected no commit hash, got %q", i, tries[i].CommitHash)
		}
	}
}
