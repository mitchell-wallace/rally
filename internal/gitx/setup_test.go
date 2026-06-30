package gitx

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeFileMain(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func initGitTestRepo(t *testing.T) string {
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

func makeInitialCommit(t *testing.T, dir string) {
	t.Helper()
	writeFileMain(t, dir, "README.md", "# test\n")
	if _, err := GitOutput(dir, "add", "README.md"); err != nil {
		t.Fatalf("git add README.md: %v", err)
	}
	if _, err := GitOutput(dir, "commit", "-m", "initial commit"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
}

func countCommits(t *testing.T, dir string) int {
	t.Helper()
	out, err := GitOutput(dir, "rev-list", "--count", "HEAD")
	if err != nil {
		t.Fatalf("git rev-list --count HEAD: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse commit count %q: %v", strings.TrimSpace(string(out)), err)
	}
	return n
}

func lastCommitMessage(t *testing.T, dir string) string {
	t.Helper()
	out, err := GitOutput(dir, "log", "-1", "--format=%s")
	if err != nil {
		t.Fatalf("git log -1 --format=%%s: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func commitMessages(t *testing.T, dir string) []string {
	t.Helper()
	out, err := GitOutput(dir, "log", "--format=%s")
	if err != nil {
		t.Fatalf("git log --format=%%s: %v", err)
	}
	var msgs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			msgs = append(msgs, line)
		}
	}
	return msgs
}

func createInitSetupFiles(t *testing.T, dir string) {
	t.Helper()
	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")
	writeFileMain(t, dir, ".rally/README.md", "# Rally\n")
	if _, err := GitOutput(dir, "add", ".rally/.gitignore", ".rally/config.toml", ".rally/README.md"); err != nil {
		t.Fatalf("git add setup files: %v", err)
	}
	if _, err := GitOutput(dir, "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit setup files: %v", err)
	}
}

func TestCommitSetupFiles_InitCreatesExactlyOneCommit(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)

	before := countCommits(t, dir)

	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")
	writeFileMain(t, dir, ".rally/README.md", "# Rally\n")

	committed, err := CommitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
		".rally/README.md",
	}, "rally: initialize workspace")
	if err != nil {
		t.Fatalf("commitSetupFiles: %v", err)
	}
	if !committed {
		t.Fatal("expected committed=true on first init")
	}

	after := countCommits(t, dir)
	if after != before+1 {
		t.Fatalf("expected %d commits, got %d", before+1, after)
	}

	msg := lastCommitMessage(t, dir)
	if msg != "rally: initialize workspace" {
		t.Fatalf("commit message = %q, want %q", msg, "rally: initialize workspace")
	}
}

func TestCommitSetupFiles_InitRerunIsNoOp(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)
	createInitSetupFiles(t, dir)

	before := countCommits(t, dir)

	committed, err := CommitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
		".rally/README.md",
	}, "rally: initialize workspace")
	if err != nil {
		t.Fatalf("commitSetupFiles: %v", err)
	}
	if committed {
		t.Fatal("expected committed=false on rerun with nothing changed")
	}

	after := countCommits(t, dir)
	if after != before {
		t.Fatalf("expected %d commits (no new commit), got %d", before, after)
	}
}

func TestCommitSetupFiles_DirtyWorkingTreeNotSwept(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)

	userFile := filepath.Join(dir, "user-code.go")
	if err := os.WriteFile(userFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write user file: %v", err)
	}

	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")
	writeFileMain(t, dir, ".rally/README.md", "# Rally\n")

	committed, err := CommitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
		".rally/README.md",
	}, "rally: initialize workspace")
	if err != nil {
		t.Fatalf("commitSetupFiles: %v", err)
	}
	if !committed {
		t.Fatal("expected committed=true even with dirty tree")
	}

	out, err := GitOutput(dir, "status", "--porcelain", "user-code.go")
	if err != nil {
		t.Fatalf("git status user-code.go: %v", err)
	}
	status := strings.TrimSpace(string(out))
	if status == "" {
		t.Fatal("user-code.go should still appear in status (unstaged)")
	}
	if strings.Contains(status, "A ") || strings.Contains(status, "M ") {
		t.Fatalf("user-code.go should NOT be staged, got status %q", status)
	}

	msg := lastCommitMessage(t, dir)
	if msg != "rally: initialize workspace" {
		t.Fatalf("commit message = %q, want %q", msg, "rally: initialize workspace")
	}
}

func TestCommitSetupFiles_HookInstallCreatesCommit(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)
	createInitSetupFiles(t, dir)

	before := countCommits(t, dir)

	lapsDir := filepath.Join(dir, ".laps")
	hooksDir := filepath.Join(lapsDir, "hooks", "rally")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	writeFileMain(t, dir, ".laps/hooks.json", `{"version":1,"hooks":[]}`)
	writeFileMain(t, dir, ".laps/hooks/rally/laps-done-hook.sh", "#!/bin/sh\nexit 0\n")
	writeFileMain(t, dir, ".laps/hooks/rally/laps-handoff-hook.sh", "#!/bin/sh\nexit 0\n")
	writeFileMain(t, dir, ".laps/hooks/rally/laps-wrapup-hook.sh", "#!/bin/sh\nexit 0\n")

	hookPaths := []string{
		".laps/hooks.json",
		".laps/hooks/rally/laps-done-hook.sh",
		".laps/hooks/rally/laps-handoff-hook.sh",
		".laps/hooks/rally/laps-wrapup-hook.sh",
	}
	committed, err := CommitSetupFiles(dir, hookPaths, "rally: install laps hooks")
	if err != nil {
		t.Fatalf("commitSetupFiles: %v", err)
	}
	if !committed {
		t.Fatal("expected committed=true on first hook install")
	}

	after := countCommits(t, dir)
	if after != before+1 {
		t.Fatalf("expected %d commits, got %d", before+1, after)
	}

	msg := lastCommitMessage(t, dir)
	if msg != "rally: install laps hooks" {
		t.Fatalf("commit message = %q, want %q", msg, "rally: install laps hooks")
	}
}

func TestCommitSetupFiles_HookReinstallIsNoOp(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)
	createInitSetupFiles(t, dir)

	hookPaths := []string{
		".laps/hooks.json",
		".laps/hooks/rally/laps-done-hook.sh",
		".laps/hooks/rally/laps-handoff-hook.sh",
		".laps/hooks/rally/laps-wrapup-hook.sh",
	}

	lapsDir := filepath.Join(dir, ".laps")
	hooksDir := filepath.Join(lapsDir, "hooks", "rally")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	writeFileMain(t, dir, ".laps/hooks.json", `{"version":1,"hooks":[]}`)
	writeFileMain(t, dir, ".laps/hooks/rally/laps-done-hook.sh", "#!/bin/sh\nexit 0\n")
	writeFileMain(t, dir, ".laps/hooks/rally/laps-handoff-hook.sh", "#!/bin/sh\nexit 0\n")
	writeFileMain(t, dir, ".laps/hooks/rally/laps-wrapup-hook.sh", "#!/bin/sh\nexit 0\n")

	_, err := CommitSetupFiles(dir, hookPaths, "rally: install laps hooks")
	if err != nil {
		t.Fatalf("first commitSetupFiles: %v", err)
	}

	before := countCommits(t, dir)

	committed, err := CommitSetupFiles(dir, hookPaths, "rally: install laps hooks")
	if err != nil {
		t.Fatalf("reinstall commitSetupFiles: %v", err)
	}
	if committed {
		t.Fatal("expected committed=false on hook reinstall")
	}

	after := countCommits(t, dir)
	if after != before {
		t.Fatalf("expected %d commits (no new commit), got %d", before, after)
	}

	msgs := commitMessages(t, dir)
	initCount := 0
	for _, m := range msgs {
		if m == "rally: install laps hooks" {
			initCount++
		}
	}
	if initCount != 1 {
		t.Fatalf("expected exactly 1 'rally: install laps hooks' commit, got %d", initCount)
	}
}

// --- Edge-case hardening: special characters in commit messages ---

// commitSetupFiles must pass messages with double quotes and dollar signs
// through to git literally — no shell expansion because exec.Command is used.
func TestCommitSetupFiles_SpecialCharMessage_DoubleQuotesAndDollars(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)

	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")

	msg := "rally: init \"special\" with $HOME and `backtick`"
	committed, err := CommitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
	}, msg)
	if err != nil {
		t.Fatalf("commitSetupFiles: %v", err)
	}
	if !committed {
		t.Fatal("expected committed=true")
	}

	got := lastCommitMessage(t, dir)
	if got != msg {
		t.Errorf("commit message = %q, want %q", got, msg)
	}
}

// commitSetupFiles must handle single quotes and newlines in the message.
// The subject line (first line) is verified via git log.
func TestCommitSetupFiles_SpecialCharMessage_SingleQuotesAndNewlines(t *testing.T) {
	dir := initGitTestRepo(t)
	makeInitialCommit(t, dir)

	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")

	msg := "rally: init user's workspace\n\nBody with special 'chars'."
	committed, err := CommitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
	}, msg)
	if err != nil {
		t.Fatalf("commitSetupFiles: %v", err)
	}
	if !committed {
		t.Fatal("expected committed=true")
	}

	got := lastCommitMessage(t, dir)
	if got != `rally: init user's workspace` {
		t.Errorf("commit subject = %q, want %q", got, `rally: init user's workspace`)
	}
}

// --- Edge-case hardening: git unavailable ---

// commitSetupFiles on a non-git directory returns (false, nil) — no crash.
func TestCommitSetupFiles_NonGitDirIsNoOp(t *testing.T) {
	dir := t.TempDir()

	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")

	committed, err := CommitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
	}, "rally: initialize workspace")
	if err != nil {
		t.Fatalf("commitSetupFiles non-git dir: %v", err)
	}
	if committed {
		t.Error("expected committed=false for non-git dir")
	}
}

// commitSetupFiles in a directory with a corrupted/empty .git returns
// (false, nil) — graceful skip, no panic.
func TestCommitSetupFiles_CorruptedGitDirIsNoOp(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeFileMain(t, dir, ".rally/.gitignore", "state/\n")
	writeFileMain(t, dir, ".rally/config.toml", "schema_version = 2\n")

	committed, err := CommitSetupFiles(dir, []string{
		".rally/.gitignore",
		".rally/config.toml",
	}, "rally: initialize workspace")
	if err != nil {
		t.Fatalf("commitSetupFiles corrupted git dir: %v", err)
	}
	if committed {
		t.Error("expected committed=false for corrupted git dir")
	}
}
