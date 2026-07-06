package laps

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeFakeAdapterLaps(t *testing.T) (string, string) {
	t.Helper()

	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.log")
	path := filepath.Join(dir, "laps")
	script := `#!/bin/sh
if [ -n "$LAPS_FAKE_ARGS_FILE" ]; then
  printf '%s\n' "$*" >> "$LAPS_FAKE_ARGS_FILE"
fi

stdout=${LAPS_FAKE_STDOUT:-'Fake title
Assignee: alice

Fake description
'}
stderr=${LAPS_FAKE_STDERR:-'fake stderr'}

if [ "$1" = "get" ] && [ "$2" = "head" ]; then
  code=${LAPS_FAKE_GET_EXIT:-0}
  if [ "$code" -eq 0 ]; then
    printf '%s' "$stdout"
  else
    printf '%s' "$stderr" >&2
  fi
  exit "$code"
fi

if [ "$1" = "claim" ]; then
  code=${LAPS_FAKE_CLAIM_EXIT:-0}
  if [ "$code" -eq 0 ]; then
    mkdir -p .laps
    if [ -n "$LAPS_FAKE_CLAIM_FILE" ]; then
      printf '%s' "$LAPS_FAKE_CLAIM_FILE" > .laps/claim
    else
      printf '%s' '{"lap":"rall-fake","file":"laps.json","claimedAt":"2026-07-06T00:00:00Z"}' > .laps/claim
    fi
    printf '%s' "$stdout"
  else
    printf '%s' "$stderr" >&2
  fi
  exit "$code"
fi

if [ "$1" = "list" ]; then
  if [ "$2" = "--root" ] && [ "$3" = "--all" ] && [ "$4" = "--json-output" ]; then
    printf '%s' "$LAPS_FAKE_LIST_JSON"
    exit ${LAPS_FAKE_LIST_EXIT:-0}
  fi
  printf '%s' "$LAPS_FAKE_LIST_OUTPUT"
  exit ${LAPS_FAKE_LIST_EXIT:-0}
fi

if [ "$1" = "status" ] && [ "$2" = "--json-output" ]; then
  printf '%s' "$LAPS_FAKE_STATUS_JSON"
  exit ${LAPS_FAKE_STATUS_EXIT:-0}
fi

if [ "$1" = "-f" ]; then
  name=$(basename "$2" .laps)
  env_name=$(printf '%s' "$name" | tr '[:lower:]-' '[:upper:]_')
  eval "out=\${LAPS_FAKE_STINT_${env_name}_JSON}"
  printf '%s' "$out"
  exit ${LAPS_FAKE_STINT_LIST_EXIT:-0}
fi

printf 'unexpected args: %s\n' "$*" >&2
exit 99
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake laps: %v", err)
	}

	oldPath := os.Getenv("PATH")
	oldArgsFile := os.Getenv("LAPS_FAKE_ARGS_FILE")
	t.Cleanup(func() {
		os.Setenv("PATH", oldPath)
		os.Setenv("LAPS_FAKE_ARGS_FILE", oldArgsFile)
		os.Unsetenv("LAPS_FAKE_GET_EXIT")
		os.Unsetenv("LAPS_FAKE_CLAIM_EXIT")
		os.Unsetenv("LAPS_FAKE_STDOUT")
		os.Unsetenv("LAPS_FAKE_STDERR")
		os.Unsetenv("LAPS_FAKE_CLAIM_FILE")
		os.Unsetenv("LAPS_FAKE_LIST_OUTPUT")
		os.Unsetenv("LAPS_FAKE_LIST_JSON")
		os.Unsetenv("LAPS_FAKE_LIST_EXIT")
		os.Unsetenv("LAPS_FAKE_STATUS_JSON")
		os.Unsetenv("LAPS_FAKE_STATUS_EXIT")
		os.Unsetenv("LAPS_FAKE_STINT_LIST_EXIT")
		os.Unsetenv("LAPS_FAKE_STINT_ALPHA_JSON")
	})
	os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
	os.Setenv("LAPS_FAKE_ARGS_FILE", argsFile)
	return dir, argsFile
}

func TestParseLapOutput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Lap
		wantErr bool
	}{
		{
			name:  "simple",
			input: "Title\n\nDescription\n",
			want:  Lap{Title: "Title", Description: "Description"},
		},
		{
			name:  "with assignee",
			input: "Title\nAssignee: alice\n\nDescription\n",
			want:  Lap{Title: "Title", Description: "Description", Assignee: "alice"},
		},
		{
			name:  "multiline description",
			input: "Title\n\nLine 1\nLine 2\n",
			want:  Lap{Title: "Title", Description: "Line 1\nLine 2"},
		},
		{
			name:  "empty description",
			input: "Title\n\n\n",
			want:  Lap{Title: "Title", Description: ""},
		},
		{
			name:  "empty description with assignee",
			input: "Title\nAssignee: bob\n\n\n",
			want:  Lap{Title: "Title", Description: "", Assignee: "bob"},
		},
		{
			name:  "claim output with undo hint",
			input: "Title\nAssignee: bob\n\nDescription\n\n-----\nNot the lap you intended to claim? Undo with 'laps claim undo'\n",
			want:  Lap{Title: "Title", Description: "Description", Assignee: "bob"},
		},
		{
			name:    "too short",
			input:   "Title\n",
			wantErr: true,
		},
		{
			name:    "single line",
			input:   "Title",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLapOutput(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseLapOutput() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got != tt.want {
				t.Errorf("parseLapOutput() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestAdapterHeadPull_RealLaps(t *testing.T) {
	if _, err := exec.LookPath("laps"); err != nil {
		t.Skip("laps binary not found on PATH")
	}

	ctx := context.Background()
	tmp := t.TempDir()

	// laps init requires .laps directory or .git in an ancestor.
	_ = os.MkdirAll(filepath.Join(tmp, ".laps"), 0o755)
	initCmd := exec.CommandContext(ctx, "laps", "init")
	initCmd.Dir = tmp
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("laps init failed: %v\noutput: %s", err, out)
	}

	adapter := &Adapter{WorkspaceDir: tmp}

	// No tasks yet → NoLap.
	lap, state, err := adapter.HeadPull(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != StateEmpty && state != StateComplete {
		t.Fatalf("empty queue state = %s, want empty or complete", state)
	}
	if lap != NoLap {
		t.Fatalf("expected NoLap, got %+v", lap)
	}

	// Add a task without assignee.
	addCmd := exec.CommandContext(ctx, "laps", "add", "head", "--title", "Test", "--description", "Desc")
	addCmd.Dir = tmp
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("laps add failed: %v\noutput: %s", err, out)
	}

	lap, state, err = adapter.HeadPull(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != StateLap {
		t.Fatalf("state = %s, want %s", state, StateLap)
	}
	if lap.Title != "Test" {
		t.Errorf("title = %q, want %q", lap.Title, "Test")
	}
	if lap.Description != "Desc" {
		t.Errorf("description = %q, want %q", lap.Description, "Desc")
	}
	if lap.Assignee != "" {
		t.Errorf("assignee = %q, want empty", lap.Assignee)
	}

	claimed, state, err := adapter.ClaimHead(ctx)
	if err != nil {
		t.Fatalf("ClaimHead error: %v", err)
	}
	if state != StateLap {
		t.Fatalf("claim state = %s, want %s", state, StateLap)
	}
	if claimed.ID == "" {
		t.Fatalf("claimed ID is empty")
	}
	if claimed.Title != "Test" {
		t.Errorf("claimed title = %q, want Test", claimed.Title)
	}
	if claimFileID, err := adapter.ReadClaim(); err != nil {
		t.Fatalf("ReadClaim error: %v", err)
	} else if claimFileID != claimed.ID {
		t.Fatalf("ReadClaim = %q, want %q", claimFileID, claimed.ID)
	}

	// Mark done → no tasks left. Bare done works because ClaimHead wrote .laps/claim.
	doneCmd := exec.CommandContext(ctx, "laps", "done")
	doneCmd.Dir = tmp
	if out, err := doneCmd.CombinedOutput(); err != nil {
		t.Fatalf("laps done failed: %v\noutput: %s", err, out)
	}

	lap, state, err = adapter.HeadPull(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != StateEmpty && state != StateComplete {
		t.Fatalf("empty queue state = %s, want empty or complete", state)
	}
	if lap != NoLap {
		t.Fatalf("expected NoLap after done, got %+v", lap)
	}
}

func TestAdapterClaimHead_NoLap(t *testing.T) {
	if _, err := exec.LookPath("laps"); err != nil {
		t.Skip("laps binary not found on PATH")
	}

	ctx := context.Background()
	tmp := t.TempDir()

	_ = os.MkdirAll(filepath.Join(tmp, ".laps"), 0o755)
	initCmd := exec.CommandContext(ctx, "laps", "init")
	initCmd.Dir = tmp
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("laps init failed: %v\noutput: %s", err, out)
	}

	lap, state, err := (&Adapter{WorkspaceDir: tmp}).ClaimHead(ctx)
	if err != nil {
		t.Fatalf("ClaimHead error: %v", err)
	}
	if state != StateEmpty && state != StateComplete {
		t.Fatalf("empty queue state = %s, want empty or complete", state)
	}
	if lap != NoLap {
		t.Fatalf("ClaimHead = %+v, want NoLap", lap)
	}
}

func TestAdapterHeadPull_WithAssignee(t *testing.T) {
	if _, err := exec.LookPath("laps"); err != nil {
		t.Skip("laps binary not found on PATH")
	}

	ctx := context.Background()
	tmp := t.TempDir()

	_ = os.MkdirAll(filepath.Join(tmp, ".laps"), 0o755)
	initCmd := exec.CommandContext(ctx, "laps", "init")
	initCmd.Dir = tmp
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("laps init failed: %v\noutput: %s", err, out)
	}

	adapter := &Adapter{WorkspaceDir: tmp}

	addCmd := exec.CommandContext(ctx, "laps", "add", "head", "--title", "Assigned", "--description", "Details", "--assignee", "alice")
	addCmd.Dir = tmp
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("laps add failed: %v\noutput: %s", err, out)
	}

	lap, state, err := adapter.HeadPull(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != StateLap {
		t.Fatalf("state = %s, want %s", state, StateLap)
	}
	if lap.Title != "Assigned" {
		t.Errorf("title = %q, want %q", lap.Title, "Assigned")
	}
	if lap.Description != "Details" {
		t.Errorf("description = %q, want %q", lap.Description, "Details")
	}
	if lap.Assignee != "alice" {
		t.Errorf("assignee = %q, want %q", lap.Assignee, "alice")
	}
}

func TestAdapterHeadPull_QueueStatesAndErrors(t *testing.T) {
	writeFakeAdapterLaps(t)
	ctx := context.Background()
	adapter := &Adapter{WorkspaceDir: t.TempDir()}

	tests := []struct {
		name      string
		exitCode  string
		wantState QueueState
		wantErr   bool
	}{
		{name: "held", exitCode: "10", wantState: StateHeld},
		{name: "empty", exitCode: "11", wantState: StateEmpty},
		{name: "complete", exitCode: "12", wantState: StateComplete},
		{name: "store error", exitCode: "2", wantErr: true},
		{name: "not found error", exitCode: "3", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("LAPS_FAKE_GET_EXIT", tt.exitCode)
			lap, state, err := adapter.HeadPull(ctx)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("HeadPull error = nil, want error")
				}
				if !strings.Contains(err.Error(), "exit code "+tt.exitCode) {
					t.Fatalf("HeadPull error = %q, want exit code %s", err.Error(), tt.exitCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("HeadPull error = %v", err)
			}
			if lap != NoLap {
				t.Fatalf("HeadPull lap = %+v, want NoLap", lap)
			}
			if state != tt.wantState {
				t.Fatalf("HeadPull state = %s, want %s", state, tt.wantState)
			}
		})
	}
}

func TestAdapterHeadPull_LapState(t *testing.T) {
	writeFakeAdapterLaps(t)
	os.Setenv("LAPS_FAKE_GET_EXIT", "0")
	os.Setenv("LAPS_FAKE_STDOUT", "Queued title\nAssignee: bob\n\nDo this work\n")

	lap, state, err := (&Adapter{WorkspaceDir: t.TempDir()}).HeadPull(context.Background())
	if err != nil {
		t.Fatalf("HeadPull error = %v", err)
	}
	if state != StateLap {
		t.Fatalf("HeadPull state = %s, want %s", state, StateLap)
	}
	want := Lap{Title: "Queued title", Assignee: "bob", Description: "Do this work"}
	if lap != want {
		t.Fatalf("HeadPull lap = %+v, want %+v", lap, want)
	}
}

func TestAdapterClaimHead_QueueStatesAndErrors(t *testing.T) {
	writeFakeAdapterLaps(t)
	ctx := context.Background()
	adapter := &Adapter{WorkspaceDir: t.TempDir()}

	tests := []struct {
		name      string
		exitCode  string
		wantState QueueState
		wantErr   bool
	}{
		{name: "held", exitCode: "10", wantState: StateHeld},
		{name: "empty", exitCode: "11", wantState: StateEmpty},
		{name: "complete", exitCode: "12", wantState: StateComplete},
		{name: "store error", exitCode: "2", wantErr: true},
		{name: "not found error", exitCode: "3", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("LAPS_FAKE_CLAIM_EXIT", tt.exitCode)
			lap, state, err := adapter.ClaimHead(ctx)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ClaimHead error = nil, want error")
				}
				if !strings.Contains(err.Error(), "exit code "+tt.exitCode) {
					t.Fatalf("ClaimHead error = %q, want exit code %s", err.Error(), tt.exitCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("ClaimHead error = %v", err)
			}
			if lap != NoLap {
				t.Fatalf("ClaimHead lap = %+v, want NoLap", lap)
			}
			if state != tt.wantState {
				t.Fatalf("ClaimHead state = %s, want %s", state, tt.wantState)
			}
		})
	}
}

func TestAdapterClaimHead_LapStateWithClaimID(t *testing.T) {
	writeFakeAdapterLaps(t)
	workspaceDir := t.TempDir()
	os.Setenv("LAPS_FAKE_CLAIM_EXIT", "0")
	os.Setenv("LAPS_FAKE_STDOUT", "Claimed title\n\nClaimed description\n")
	os.Setenv("LAPS_FAKE_CLAIM_FILE", `{"lap":"rall-abcd","file":"laps.json","claimedAt":"2026-07-06T00:00:00Z"}`)

	lap, state, err := (&Adapter{WorkspaceDir: workspaceDir}).ClaimHead(context.Background())
	if err != nil {
		t.Fatalf("ClaimHead error = %v", err)
	}
	if state != StateLap {
		t.Fatalf("ClaimHead state = %s, want %s", state, StateLap)
	}
	want := Lap{ID: "rall-abcd", Title: "Claimed title", Description: "Claimed description"}
	if lap != want {
		t.Fatalf("ClaimHead lap = %+v, want %+v", lap, want)
	}
}

func TestAdapterReadClaim_JSONAndLegacy(t *testing.T) {
	workspaceDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspaceDir, ".laps"), 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := &Adapter{WorkspaceDir: workspaceDir}
	claimPath := filepath.Join(workspaceDir, ".laps", "claim")

	if err := os.WriteFile(claimPath, []byte(`{"lap":"rall-abcd","file":"laps.json","claimedAt":"2026-07-06T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := adapter.ReadClaim()
	if err != nil {
		t.Fatalf("ReadClaim JSON error = %v", err)
	}
	if id != "rall-abcd" {
		t.Fatalf("ReadClaim JSON = %q, want rall-abcd", id)
	}

	if err := os.WriteFile(claimPath, []byte("  rall-legacy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err = adapter.ReadClaim()
	if err != nil {
		t.Fatalf("ReadClaim legacy error = %v", err)
	}
	if id != "rall-legacy" {
		t.Fatalf("ReadClaim legacy = %q, want rall-legacy", id)
	}
}

func TestAdapterQueueSize_UsesOnelineAndCountsLines(t *testing.T) {
	_, argsFile := writeFakeAdapterLaps(t)
	os.Setenv("LAPS_FAKE_LIST_OUTPUT", "1. rall-a - Alpha\n2. rall-b - Beta\n\n")

	size, err := (&Adapter{WorkspaceDir: t.TempDir()}).QueueSize(context.Background())
	if err != nil {
		t.Fatalf("QueueSize error = %v", err)
	}
	if size != 2 {
		t.Fatalf("QueueSize = %d, want 2", size)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read fake args: %v", err)
	}
	if !strings.Contains(string(args), "list --oneline") {
		t.Fatalf("fake laps args = %q, want list --oneline", string(args))
	}
}
