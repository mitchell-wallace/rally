package laps

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
	lap, err := adapter.HeadPull(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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

	lap, err = adapter.HeadPull(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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

	// Mark done → no tasks left.
	doneCmd := exec.CommandContext(ctx, "laps", "done")
	doneCmd.Dir = tmp
	if out, err := doneCmd.CombinedOutput(); err != nil {
		t.Fatalf("laps done failed: %v\noutput: %s", err, out)
	}

	lap, err = adapter.HeadPull(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lap != NoLap {
		t.Fatalf("expected NoLap after done, got %+v", lap)
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

	lap, err := adapter.HeadPull(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
