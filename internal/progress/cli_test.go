package progress

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTempWorkspace(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func installFakeLaps(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	fakeLaps := filepath.Join(binDir, "laps")
	script := `#!/bin/sh
if [ "$1" = "add" ] && [ "$2" = "head" ]; then
    echo "lap-fake-id"
    exit 0
fi
echo "fake laps error" >&2
exit 1
`
	if err := os.WriteFile(fakeLaps, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake laps: %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", oldPath) })
	os.Setenv("PATH", binDir+":"+oldPath)
	return tmp
}

func enableLapsInWorkspace(t *testing.T, workspaceDir string) {
	t.Helper()
	lapsDir := filepath.Join(workspaceDir, ".laps")
	if err := os.MkdirAll(lapsDir, 0o755); err != nil {
		t.Fatalf("mkdir .laps: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lapsDir, "laps.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write laps.json: %v", err)
	}
}

func overrideWorkspaceDir(t *testing.T, dir string) {
	t.Helper()
	old := getWorkspaceDir
	getWorkspaceDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { getWorkspaceDir = old })
}

func TestNewProgressCmd(t *testing.T) {
	cmd := NewProgressCmd()
	if cmd == nil {
		t.Fatal("NewProgressCmd returned nil")
	}
	if cmd.Name() != "progress" {
		t.Errorf("cmd.Name() = %q, want progress", cmd.Name())
	}
}

func TestProgressRecordLap(t *testing.T) {
	tmp := setupTempWorkspace(t)
	overrideWorkspaceDir(t, tmp)

	cmd := NewProgressCmd()
	cmd.SetArgs([]string{"--record-lap", "lap-1", "--record-lap", "lap-2"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	rs, err := LoadRunState(tmp)
	if err != nil {
		t.Fatalf("LoadRunState error: %v", err)
	}
	if len(rs.RecordedLaps) != 2 {
		t.Fatalf("RecordedLaps = %v, want 2 entries", rs.RecordedLaps)
	}
	if rs.RecordedLaps[0] != "lap-1" || rs.RecordedLaps[1] != "lap-2" {
		t.Errorf("RecordedLaps = %v, want [lap-1 lap-2]", rs.RecordedLaps)
	}
}

func TestProgressSetHandoff(t *testing.T) {
	tmp := setupTempWorkspace(t)
	overrideWorkspaceDir(t, tmp)

	cmd := NewProgressCmd()
	cmd.SetArgs([]string{"--set-handoff"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	rs, err := LoadRunState(tmp)
	if err != nil {
		t.Fatalf("LoadRunState error: %v", err)
	}
	if rs.HandoffState != 1 {
		t.Errorf("HandoffState = %d, want 1", rs.HandoffState)
	}
}

func TestProgressComplete(t *testing.T) {
	tmp := setupTempWorkspace(t)
	enableLapsInWorkspace(t, tmp)
	overrideWorkspaceDir(t, tmp)

	// Seed run state with a run ID and some laps.
	if err := SaveRunState(tmp, &RunState{
		RunID:        "run-99",
		RecordedLaps: []string{"lap-1"},
	}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	cmd := NewProgressCmd()
	cmd.SetArgs([]string{
		"--complete",
		"--summary", "Did something",
		"--followup", "Next thing",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	// Run state should be cleared.
	if _, err := os.Stat(RunStatePath(tmp)); !os.IsNotExist(err) {
		t.Fatal("expected run-state.json to be cleared")
	}

	// Progress log should have an entry.
	pl, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if len(pl.RecentRuns) != 1 {
		t.Fatalf("len(RecentRuns) = %d, want 1", len(pl.RecentRuns))
	}
	entry := pl.RecentRuns[0]
	if entry.RunID != "run-99" {
		t.Errorf("RunID = %q, want run-99", entry.RunID)
	}
	if entry.Summary != "Did something" {
		t.Errorf("Summary = %q, want Did something", entry.Summary)
	}
	laps, ok := entry.LapsCompleted.([]interface{})
	if !ok || len(laps) != 1 || laps[0] != "lap-1" {
		t.Errorf("LapsCompleted = %v, want [lap-1]", entry.LapsCompleted)
	}
	if entry.Handoff != nil {
		t.Errorf("expected no Handoff for complete, got %+v", entry.Handoff)
	}
}

func TestProgressHandoff(t *testing.T) {
	installFakeLaps(t)
	tmp := setupTempWorkspace(t)
	overrideWorkspaceDir(t, tmp)

	if err := SaveRunState(tmp, &RunState{
		RunID:        "run-88",
		RecordedLaps: []string{"lap-a"},
	}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	cmd := NewProgressCmd()
	cmd.SetArgs([]string{
		"--handoff",
		"--summary", "Blocked on auth",
		"--followup", "Investigate token rotation",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	// Run state should be cleared.
	if _, err := os.Stat(RunStatePath(tmp)); !os.IsNotExist(err) {
		t.Fatal("expected run-state.json to be cleared")
	}

	pl, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if len(pl.RecentRuns) != 1 {
		t.Fatalf("len(RecentRuns) = %d, want 1", len(pl.RecentRuns))
	}
	entry := pl.RecentRuns[0]
	if entry.RunID != "run-88" {
		t.Errorf("RunID = %q, want run-88", entry.RunID)
	}
	if entry.Handoff == nil {
		t.Fatal("expected Handoff to be present")
	}
	if entry.Handoff.Summary != "Blocked on auth" {
		t.Errorf("Handoff.Summary = %q, want Blocked on auth", entry.Handoff.Summary)
	}
	if len(entry.Handoff.CreatedLapIDs) != 1 || entry.Handoff.CreatedLapIDs[0] != "lap-fake-id" {
		t.Errorf("CreatedLapIDs = %v, want [lap-fake-id]", entry.Handoff.CreatedLapIDs)
	}
}

func TestProgressWrapupNoHandoff(t *testing.T) {
	tmp := setupTempWorkspace(t)
	enableLapsInWorkspace(t, tmp)
	overrideWorkspaceDir(t, tmp)

	if err := SaveRunState(tmp, &RunState{
		RunID:        "run-77",
		HandoffState: 0,
		RecordedLaps: []string{},
	}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	cmd := NewProgressCmd()
	cmd.SetArgs([]string{
		"--wrapup",
		"--summary", "All done",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	pl, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if len(pl.RecentRuns) != 1 {
		t.Fatalf("len(RecentRuns) = %d, want 1", len(pl.RecentRuns))
	}
	entry := pl.RecentRuns[0]
	if entry.Handoff != nil {
		t.Errorf("expected no Handoff for wrapup with handoff_state=0, got %+v", entry.Handoff)
	}
	if entry.LapsCompleted != "none" {
		t.Errorf("LapsCompleted = %v, want none", entry.LapsCompleted)
	}
}

func TestProgressWrapupWithHandoff(t *testing.T) {
	installFakeLaps(t)
	tmp := setupTempWorkspace(t)
	overrideWorkspaceDir(t, tmp)

	if err := SaveRunState(tmp, &RunState{
		RunID:        "run-66",
		HandoffState: 1,
		RecordedLaps: []string{},
	}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	cmd := NewProgressCmd()
	cmd.SetArgs([]string{
		"--wrapup",
		"--summary", "Blocked",
		"--followup", "Fix it",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	pl, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if len(pl.RecentRuns) != 1 {
		t.Fatalf("len(RecentRuns) = %d, want 1", len(pl.RecentRuns))
	}
	entry := pl.RecentRuns[0]
	if entry.Handoff == nil {
		t.Fatal("expected Handoff to be present")
	}
}

func TestProgressMissingSummary(t *testing.T) {
	tmp := setupTempWorkspace(t)
	overrideWorkspaceDir(t, tmp)

	for _, mode := range []string{"--complete", "--handoff", "--wrapup"} {
		t.Run(mode, func(t *testing.T) {
			cmd := NewProgressCmd()
			cmd.SetArgs([]string{mode})
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected error for missing summary")
			}
			if !strings.Contains(err.Error(), "--summary is required") {
				t.Errorf("error = %q, want summary required", err.Error())
			}
		})
	}
}

func TestProgressNoAction(t *testing.T) {
	tmp := setupTempWorkspace(t)
	overrideWorkspaceDir(t, tmp)

	cmd := NewProgressCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for no action")
	}
	if !strings.Contains(err.Error(), "no action specified") {
		t.Errorf("error = %q, want no action specified", err.Error())
	}
}

func TestProgressCompleteGeneratesRunID(t *testing.T) {
	tmp := setupTempWorkspace(t)
	overrideWorkspaceDir(t, tmp)

	// No run state file => LoadRunState returns empty RunID.
	cmd := NewProgressCmd()
	cmd.SetArgs([]string{
		"--complete",
		"--summary", "Done",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	pl, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if len(pl.RecentRuns) != 1 {
		t.Fatalf("len(RecentRuns) = %d, want 1", len(pl.RecentRuns))
	}
	if pl.RecentRuns[0].RunID == "" {
		t.Errorf("expected generated RunID, got empty")
	}
}

func TestProgressHandoffTruncatesTitle(t *testing.T) {
	installFakeLaps(t)
	tmp := setupTempWorkspace(t)
	overrideWorkspaceDir(t, tmp)

	if err := SaveRunState(tmp, &RunState{RunID: "run-55"}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	// Override execLapsAddHead to capture the title argument.
	var capturedTitle string
	oldExec := execLapsAddHead
	execLapsAddHead = func(workspaceDir, title, description string) (string, error) {
		capturedTitle = title
		return oldExec(workspaceDir, title, description)
	}
	defer func() { execLapsAddHead = oldExec }()

	longFollowup := strings.Repeat("a", 50)
	cmd := NewProgressCmd()
	cmd.SetArgs([]string{
		"--handoff",
		"--summary", "Blocked",
		"--followup", longFollowup,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	want := strings.Repeat("a", 30) + "..."
	if capturedTitle != want {
		t.Errorf("title = %q, want %q", capturedTitle, want)
	}
}

func TestProgressHandoffLapFailureStillWritesEntry(t *testing.T) {
	tmp := setupTempWorkspace(t)
	overrideWorkspaceDir(t, tmp)

	if err := SaveRunState(tmp, &RunState{RunID: "run-44"}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	// Make laps add head fail.
	oldExec := execLapsAddHead
	execLapsAddHead = func(_, _, _ string) (string, error) {
		return "", fmt.Errorf("laps error")
	}
	defer func() { execLapsAddHead = oldExec }()

	cmd := NewProgressCmd()
	cmd.SetArgs([]string{
		"--handoff",
		"--summary", "Blocked",
		"--followup", "Fix it",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	pl, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if len(pl.RecentRuns) != 1 {
		t.Fatalf("len(RecentRuns) = %d, want 1", len(pl.RecentRuns))
	}
	// Entry should still be written even though lap creation failed.
	entry := pl.RecentRuns[0]
	if entry.Handoff == nil {
		t.Fatal("expected Handoff to be present")
	}
	if len(entry.Handoff.CreatedLapIDs) != 0 {
		t.Errorf("CreatedLapIDs = %v, want empty", entry.Handoff.CreatedLapIDs)
	}
}
