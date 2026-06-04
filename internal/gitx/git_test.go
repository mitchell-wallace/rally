package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a fresh git repository rooted at a temp dir and returns its
// path. It configures a local identity so commits succeed deterministically.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := GitOutput(dir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := GitOutput(dir, "config", "user.name", "Test"); err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if _, err := GitOutput(dir, "config", "user.email", "test@localhost"); err != nil {
		t.Fatalf("git config user.email: %v", err)
	}
	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// trackedFiles returns the set of paths git tracks at HEAD.
func trackedFiles(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out, err := GitOutput(dir, "ls-files")
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	set := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			set[line] = true
		}
	}
	return set
}

// commitCount returns the number of commits reachable from HEAD, or 0 when the
// repo has no commits yet.
func commitCount(t *testing.T, dir string) int {
	t.Helper()
	out, err := GitOutput(dir, "rev-list", "--count", "HEAD")
	if err != nil {
		// No commits yet (unborn HEAD) reports as 0.
		return 0
	}
	n := 0
	if _, scanErr := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n); scanErr != nil {
		t.Fatalf("parse commit count %q: %v", string(out), scanErr)
	}
	return n
}

// headSubject returns the subject line of HEAD's commit message.
func headSubject(t *testing.T, dir string) string {
	t.Helper()
	out, err := GitOutput(dir, "log", "-1", "--format=%s")
	if err != nil {
		t.Fatalf("git log -1: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// headAuthor returns "Name <email>" for HEAD's author.
func headAuthor(t *testing.T, dir string) string {
	t.Helper()
	out, err := GitOutput(dir, "log", "-1", "--format=%an <%ae>")
	if err != nil {
		t.Fatalf("git log -1 author: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// fileInHead reports whether rel is part of HEAD's tree.
func fileInHead(t *testing.T, dir, rel string) bool {
	t.Helper()
	_, err := GitOutput(dir, "cat-file", "-e", "HEAD:"+rel)
	return err == nil
}

// commitAll stages everything and commits with msg, mirroring how the runner's
// autoCommit folds the working tree (including summary.jsonl) into one work
// commit via `git add -A`.
func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	if _, err := GitOutput(dir, "add", "-A"); err != nil {
		t.Fatalf("git add -A: %v", err)
	}
	if _, err := GitOutput(dir, "commit", "--no-verify", "-m", msg); err != nil {
		t.Fatalf("git commit %q: %v", msg, err)
	}
}

// seedInitialCommit creates a first non-rally commit so HEAD exists. It also
// seeds tracked .rally/ and .laps/ entries because a real Rally workspace always
// has both by the time finalization runs (FoldRallyState's directory pathspecs
// require them to exist).
func seedInitialCommit(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, dir, "README.md", "seed\n")
	writeFile(t, dir, ".rally/summary.jsonl", "")
	writeFile(t, dir, ".laps/laps.json", "{}\n")
	commitAll(t, dir, "feat: initial project")
}

// Code-producing run: the work commit (made by autoCommit's `git add -A`)
// already contains the summary.jsonl append, so the follow-up FoldRallyState is
// a no-op — exactly one work commit, no separate `rally: update state`.
func TestFoldRallyStateCodeRunNoSeparateStateCommit(t *testing.T) {
	dir := initRepo(t)
	seedInitialCommit(t, dir)

	// Simulate a code-producing run: user code plus a summary.jsonl append, all
	// folded into one work commit the way autoCommit does it.
	writeFile(t, dir, "feature.go", "package main\n")
	writeFile(t, dir, ".rally/summary.jsonl", `{"run":1}`+"\n")
	commitAll(t, dir, "rally: run 1 attempt 1 (claude)")

	before := commitCount(t, dir)

	if err := FoldRallyState(dir); err != nil {
		t.Fatalf("FoldRallyState: %v", err)
	}

	if got := commitCount(t, dir); got != before {
		t.Errorf("commit count changed: before=%d after=%d (no state commit expected)", before, got)
	}
	if got := headSubject(t, dir); got != "rally: run 1 attempt 1 (claude)" {
		t.Errorf("HEAD subject mutated: %q (expected unchanged, no [+state])", got)
	}
	if !fileInHead(t, dir, ".rally/summary.jsonl") {
		t.Error("summary.jsonl not folded into the work commit")
	}
}

// No-code run with a rally-authored HEAD: the leftover state churn is folded by
// amending HEAD, whose subject gains ` [+state]` — no new commit is created.
func TestFoldRallyStateNoCodeAmendsRallyHead(t *testing.T) {
	dir := initRepo(t)
	seedInitialCommit(t, dir)

	// A prior code run committed work, including its identity.
	writeFile(t, dir, "feature.go", "package main\n")
	commitAll(t, dir, "rally: run 1 attempt 1 (claude)")
	wantAuthor := headAuthor(t, dir)
	before := commitCount(t, dir)

	// A subsequent no-code run only churns rally state.
	writeFile(t, dir, ".rally/summary.jsonl", `{"run":2}`+"\n")

	if err := FoldRallyState(dir); err != nil {
		t.Fatalf("FoldRallyState: %v", err)
	}

	if got := commitCount(t, dir); got != before {
		t.Errorf("commit count changed: before=%d after=%d (amend expected, not a new commit)", before, got)
	}
	if got := headSubject(t, dir); got != "rally: run 1 attempt 1 (claude) [+state]" {
		t.Errorf("HEAD subject = %q, want amended with [+state]", got)
	}
	if got := headAuthor(t, dir); got != wantAuthor {
		t.Errorf("amend changed authorship: got %q want %q", got, wantAuthor)
	}
	if !fileInHead(t, dir, ".rally/summary.jsonl") {
		t.Error("state churn not folded into amended HEAD")
	}

	// A second consecutive no-code run must not stack another ` [+state]`.
	writeFile(t, dir, ".rally/summary.jsonl", `{"run":3}`+"\n")
	if err := FoldRallyState(dir); err != nil {
		t.Fatalf("FoldRallyState (second): %v", err)
	}
	if got := headSubject(t, dir); got != "rally: run 1 attempt 1 (claude) [+state]" {
		t.Errorf("HEAD subject = %q, want a single [+state] suffix (no stacking)", got)
	}
	if got := commitCount(t, dir); got != before {
		t.Errorf("second fold added a commit: before=%d after=%d", before, got)
	}
}

// No-code run with a non-rally HEAD: a single `rally: update state` commit is
// created, and a second no-code finalization amends it rather than stacking a
// second state commit.
func TestFoldRallyStateNoCodeNonRallyHeadCreatesOneStateCommit(t *testing.T) {
	dir := initRepo(t)
	seedInitialCommit(t, dir) // HEAD subject: "feat: initial project"

	before := commitCount(t, dir)

	writeFile(t, dir, ".rally/summary.jsonl", `{"run":1}`+"\n")
	if err := FoldRallyState(dir); err != nil {
		t.Fatalf("FoldRallyState: %v", err)
	}

	if got := commitCount(t, dir); got != before+1 {
		t.Errorf("commit count = %d, want %d (one state commit)", got, before+1)
	}
	if got := headSubject(t, dir); got != "rally: update state" {
		t.Errorf("HEAD subject = %q, want %q", got, "rally: update state")
	}

	afterFirst := commitCount(t, dir)

	// A second no-code finalization: the new HEAD is itself rally-authored, so
	// it is amended rather than producing a second `rally: update state`.
	writeFile(t, dir, ".rally/summary.jsonl", `{"run":2}`+"\n")
	if err := FoldRallyState(dir); err != nil {
		t.Fatalf("FoldRallyState (second): %v", err)
	}
	if got := commitCount(t, dir); got != afterFirst {
		t.Errorf("second state commit stacked: before=%d after=%d", afterFirst, got)
	}
	if got := headSubject(t, dir); got != "rally: update state [+state]" {
		t.Errorf("HEAD subject = %q, want amended %q", got, "rally: update state [+state]")
	}
}

// No changes at all at finalization: neither an amend nor a new commit.
func TestFoldRallyStateNoChangesIsNoOp(t *testing.T) {
	dir := initRepo(t)
	seedInitialCommit(t, dir)
	writeFile(t, dir, "feature.go", "package main\n")
	commitAll(t, dir, "rally: run 1 attempt 1 (claude)")

	before := commitCount(t, dir)
	subjectBefore := headSubject(t, dir)

	if err := FoldRallyState(dir); err != nil {
		t.Fatalf("FoldRallyState: %v", err)
	}

	if got := commitCount(t, dir); got != before {
		t.Errorf("commit count changed on no-op: before=%d after=%d", before, got)
	}
	if got := headSubject(t, dir); got != subjectBefore {
		t.Errorf("HEAD subject changed on no-op: %q -> %q", subjectBefore, got)
	}
}

// Outside a git repo, FoldRallyState is a graceful no-op (no error, no panic).
func TestFoldRallyStateNonGitDirIsNoOp(t *testing.T) {
	dir := t.TempDir()
	if err := FoldRallyState(dir); err != nil {
		t.Fatalf("FoldRallyState in non-git dir: %v", err)
	}
}
