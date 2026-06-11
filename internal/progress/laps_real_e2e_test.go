package progress

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/testutil"
)

func prependPath(t *testing.T, path string) {
	t.Helper()
	oldPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", oldPath) })
	os.Setenv("PATH", path+string(os.PathListSeparator)+oldPath)
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func TestRealLapsDoneWrapupFlow(t *testing.T) {
	workspaceDir := testutil.SetupLapsFixtureProject(t)
	rallyBin := testutil.BuildRallyBinary(t)
	prependPath(t, filepath.Dir(rallyBin))

	changed, err := laps.InstallHooks(filepath.Join(workspaceDir, ".laps"))
	if err != nil {
		t.Fatalf("InstallHooks error: %v", err)
	}
	if !changed {
		t.Fatal("expected hook install to report changes on first install")
	}

	if err := SaveRunState(workspaceDir, &RunState{RunID: "run-real-1"}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	testutil.RunCommand(t, workspaceDir, "laps", "add", "head",
		"--title", "Implement auth",
		"--description", "Add login endpoint",
		"--assignee", "alice",
	)

	claimOutput := testutil.RunCommand(t, workspaceDir, "laps", "claim")
	if !strings.Contains(claimOutput, "Implement auth") {
		t.Fatalf("expected claim output to include lap title, got %q", claimOutput)
	}
	claimedID, err := (&laps.Adapter{WorkspaceDir: workspaceDir}).ReadClaim()
	if err != nil {
		t.Fatalf("ReadClaim error: %v", err)
	}
	if claimedID == "" {
		t.Fatal("expected laps claim to write .laps/claim")
	}

	doneOutput := testutil.RunCommand(t, workspaceDir, "laps", "done")
	if !strings.Contains(doneOutput, "laps wrapup --summary") {
		t.Fatalf("expected wrapup instructions in laps done output, got %q", doneOutput)
	}
	if first := firstNonEmptyLine(doneOutput); first != "Implement auth" {
		t.Fatalf("expected laps done to print completed lap title first, got %q in %q", first, doneOutput)
	}

	wrapupOutput := testutil.RunCommand(t, workspaceDir, "laps", "wrapup",
		"--summary", "Implemented auth",
		"--followup", "Add auth tests",
	)
	if !strings.Contains(wrapupOutput, "Progress recorded.") {
		t.Fatalf("expected progress recorded output, got %q", wrapupOutput)
	}

	entries, err := LoadSummaryEntries(workspaceDir)
	if err != nil {
		t.Fatalf("LoadSummaryEntries error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.RunID != "run-real-1" {
		t.Errorf("RunID = %q, want run-real-1", entry.RunID)
	}
	if entry.Summary != "Implemented auth" {
		t.Errorf("Summary = %q, want Implemented auth", entry.Summary)
	}
	lapsCompleted, ok := entry.LapsCompleted.([]interface{})
	if !ok || len(lapsCompleted) != 1 || lapsCompleted[0] != claimedID {
		t.Errorf("LapsCompleted = %v, want [%s]", entry.LapsCompleted, claimedID)
	}
	if entry.Handoff != nil {
		t.Errorf("expected no handoff entry, got %+v", entry.Handoff)
	}

	headLap, err := (&laps.Adapter{WorkspaceDir: workspaceDir}).HeadPull(context.Background())
	if err != nil {
		t.Fatalf("HeadPull error: %v", err)
	}
	if headLap != laps.NoLap {
		t.Errorf("remaining head lap = %+v, want NoLap", headLap)
	}
}

func TestRealLapsHandoffWrapupCreatesHeadFollowup(t *testing.T) {
	workspaceDir := testutil.SetupLapsFixtureProject(t)
	rallyBin := testutil.BuildRallyBinary(t)
	prependPath(t, filepath.Dir(rallyBin))

	if _, err := laps.InstallHooks(filepath.Join(workspaceDir, ".laps")); err != nil {
		t.Fatalf("InstallHooks error: %v", err)
	}

	if err := SaveRunState(workspaceDir, &RunState{RunID: "run-real-2"}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	testutil.RunCommand(t, workspaceDir, "laps", "add", "head",
		"--title", "Current task",
		"--description", "Still blocked",
	)

	handoffOutput := testutil.RunCommand(t, workspaceDir, "laps", "handoff")
	if !strings.Contains(handoffOutput, "laps wrapup --summary") {
		t.Fatalf("expected wrapup instructions in laps handoff output, got %q", handoffOutput)
	}

	followup := "Investigate token rotation and refresh flow"
	wrapupOutput := testutil.RunCommand(t, workspaceDir, "laps", "wrapup",
		"--summary", "Blocked on auth",
		"--followup", followup,
	)
	if !strings.Contains(wrapupOutput, "Progress recorded.") {
		t.Fatalf("expected progress recorded output, got %q", wrapupOutput)
	}

	entries, err := LoadSummaryEntries(workspaceDir)
	if err != nil {
		t.Fatalf("LoadSummaryEntries error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Handoff == nil {
		t.Fatal("expected handoff entry to be present")
	}
	if entry.Handoff.Summary != "Blocked on auth" {
		t.Errorf("Handoff.Summary = %q, want %q", entry.Handoff.Summary, "Blocked on auth")
	}
	if len(entry.Handoff.CreatedLapIDs) != 1 {
		t.Fatalf("CreatedLapIDs = %v, want 1 entry", entry.Handoff.CreatedLapIDs)
	}

	headLap, err := (&laps.Adapter{WorkspaceDir: workspaceDir}).HeadPull(context.Background())
	if err != nil {
		t.Fatalf("HeadPull error: %v", err)
	}
	if headLap.Description != followup {
		t.Errorf("head description = %q, want %q", headLap.Description, followup)
	}
	wantTitle := followup
	if len(wantTitle) > 30 {
		wantTitle = wantTitle[:30] + "..."
	}
	if headLap.Title != wantTitle {
		t.Errorf("head title = %q, want %q", headLap.Title, wantTitle)
	}
}
