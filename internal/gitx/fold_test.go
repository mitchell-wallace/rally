package gitx

import (
	"strings"
	"testing"
)

func TestFoldRallyStateIntoHead_AmendsRunCommitWithSummary(t *testing.T) {
	dir := initRepo(t)
	writeFile(t, dir, "README.md", "hi")
	if _, err := GitOutput(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := GitOutput(dir, "commit", "--no-verify", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	// An agent-authored run commit (not rally-prefixed).
	writeFile(t, dir, "app.go", "package main")
	if _, err := GitOutput(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := GitOutput(dir, "commit", "--no-verify", "-m", "feature: done"); err != nil {
		t.Fatal(err)
	}
	beforeHash := strings.TrimSpace(mustOut(t, dir, "rev-parse", "HEAD"))
	beforeCount := commitCount(t, dir)

	// Simulate the summary.jsonl append that happens after the run commit.
	writeFile(t, dir, ".rally/summary.jsonl", "{\"run_id\":\"x\"}\n")

	newHash, err := FoldRallyStateIntoHead(dir)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	if newHash == "" || newHash == beforeHash {
		t.Fatalf("expected an amended HEAD hash, got %q (before %q)", newHash, beforeHash)
	}
	if got := commitCount(t, dir); got != beforeCount {
		t.Errorf("amend must not add a commit: count %d, want %d", got, beforeCount)
	}
	if subj := strings.TrimSpace(mustOut(t, dir, "log", "-1", "--format=%s")); subj != "feature: done" {
		t.Errorf("HEAD subject = %q, want the preserved %q", subj, "feature: done")
	}
	if !trackedFiles(t, dir)[".rally/summary.jsonl"] {
		t.Error(".rally/summary.jsonl should be tracked in HEAD after the fold")
	}
	if dirty, _ := IsGitDirty(dir); dirty {
		t.Error("working tree should be clean after the fold")
	}

	// Idempotent: nothing staged → no-op, returns the same HEAD.
	again, err := FoldRallyStateIntoHead(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again != newHash {
		t.Errorf("idempotent fold returned %q, want unchanged %q", again, newHash)
	}
}

func TestFoldRallyStateIntoHead_NoChangesReturnsHeadUnchanged(t *testing.T) {
	dir := initRepo(t)
	writeFile(t, dir, "README.md", "hi")
	if _, err := GitOutput(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := GitOutput(dir, "commit", "--no-verify", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	before := strings.TrimSpace(mustOut(t, dir, "rev-parse", "HEAD"))

	got, err := FoldRallyStateIntoHead(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != before {
		t.Errorf("no-op fold returned %q, want unchanged %q", got, before)
	}
}

func TestFoldRallyStateIntoHead_DoesNotStageUserCode(t *testing.T) {
	dir := initRepo(t)
	writeFile(t, dir, "README.md", "hi")
	if _, err := GitOutput(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := GitOutput(dir, "commit", "--no-verify", "-m", "feature: done"); err != nil {
		t.Fatal(err)
	}
	// An intentionally-uncommitted user file (e.g. a dirty handoff) plus rally state.
	writeFile(t, dir, "leftover.txt", "wip")
	writeFile(t, dir, ".rally/summary.jsonl", "{}\n")

	if _, err := FoldRallyStateIntoHead(dir); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if trackedFiles(t, dir)["leftover.txt"] {
		t.Error("user code must not be folded into the run commit")
	}
	if !trackedFiles(t, dir)[".rally/summary.jsonl"] {
		t.Error(".rally/summary.jsonl should be folded in")
	}
}

func mustOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := GitOutput(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}
