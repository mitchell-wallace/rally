package laps

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeStatusJSON = `{
  "file":"laps.json",
  "state":"held",
  "counts":{"todo":3,"done":2,"total":5},
  "head":{"id":"rall-2"},
  "claim":{"valid":true,"lap":"rall-2","file":"laps.json","claimedAt":"2026-07-06T00:00:00Z","ageSeconds":91},
  "assignees":[{"assignee":"senior","todo":2},{"assignee":"review","todo":1}],
  "activeStint":{"name":"alpha","scope":"ui","file":"stints/alpha.laps","todo":1,"done":1,"total":2,"queued":true,"archived":false,"active":true},
  "stints":[
    {"name":"alpha","scope":"ui","file":"stints/alpha.laps","todo":1,"done":1,"total":2,"queued":true,"archived":false,"active":true},
    {"name":"old","scope":"ui","file":"stints/old.laps","todo":0,"done":2,"total":2,"queued":true,"archived":true,"active":false}
  ],
  "gate":{"state":"held","stint":"alpha","scope":"ui","file":"stints/alpha.laps","message":"finish alpha first"}
}`

const fakeRootListJSON = `{"tasks":[
  {"kind":"lap","id":"rall-1","title":"Root done","assignee":"review","isDone":true,"order":1},
  {"kind":"stint","id":"stint-alpha","ref":"alpha","title":"Alpha stint","isDone":false,"order":2},
  {"kind":"lap","id":"rall-3","title":"Root todo","assignee":"senior","isDone":false,"order":3}
]}`

const fakeStintAlphaJSON = `{"tasks":[
  {"kind":"lap","id":"rall-a","title":"Alpha lap","assignee":"junior","isDone":false,"order":1}
]}`

func TestQueueSnapshotLoadsStatusRootAndQueuedStints(t *testing.T) {
	_, argsFile := writeFakeAdapterLaps(t)
	workspaceDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspaceDir, ".laps"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.Setenv("LAPS_FAKE_STATUS_JSON", fakeStatusJSON)
	os.Setenv("LAPS_FAKE_LIST_JSON", fakeRootListJSON)
	os.Setenv("LAPS_FAKE_STINT_ALPHA_JSON", fakeStintAlphaJSON)

	snapshot, err := (&Adapter{WorkspaceDir: workspaceDir}).QueueSnapshot(context.Background())
	if err != nil {
		t.Fatalf("QueueSnapshot error = %v", err)
	}
	if snapshot.Missing {
		t.Fatal("QueueSnapshot Missing = true, want false")
	}
	if snapshot.State != "held" || snapshot.Counts.Todo != 3 || snapshot.Counts.Done != 2 || snapshot.Counts.Total != 5 {
		t.Fatalf("snapshot state/counts = %s %+v", snapshot.State, snapshot.Counts)
	}
	if !snapshot.Claim.Valid || snapshot.Claim.Lap != "rall-2" || snapshot.Claim.AgeSeconds != 91 {
		t.Fatalf("claim = %+v", snapshot.Claim)
	}
	if snapshot.Gate == nil || snapshot.Gate.Stint != "alpha" || snapshot.Gate.Message != "finish alpha first" {
		t.Fatalf("gate = %+v", snapshot.Gate)
	}
	if snapshot.ActiveStint != "alpha" {
		t.Fatalf("active stint = %q, want alpha", snapshot.ActiveStint)
	}
	if len(snapshot.Assignees) != 2 || snapshot.Assignees[0].Assignee != "senior" || snapshot.Assignees[0].Todo != 2 {
		t.Fatalf("assignees = %+v", snapshot.Assignees)
	}
	if len(snapshot.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(snapshot.Entries))
	}
	stint := snapshot.Entries[1]
	if stint.Kind != "stint" || stint.Stint == nil || stint.Stint.Done != 1 || len(stint.Laps) != 1 {
		t.Fatalf("stint entry = %+v", stint)
	}
	if stint.Laps[0].ID != "rall-a" || stint.Laps[0].Assignee != "junior" {
		t.Fatalf("stint laps = %+v", stint.Laps)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := string(argsData)
	for _, want := range []string{
		"status --json-output",
		"list --root --all --json-output",
		"-f stints/alpha.laps list --all --json-output",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q missing %q", args, want)
		}
	}
	if strings.Contains(args, "stints/old.laps") {
		t.Fatalf("archived stint should not be listed, args = %q", args)
	}
}

func TestQueueSnapshotMissingWorkspaceOrBinary(t *testing.T) {
	workspaceDir := t.TempDir()
	snapshot, err := (&Adapter{WorkspaceDir: workspaceDir}).QueueSnapshot(context.Background())
	if err != nil {
		t.Fatalf("missing .laps error = %v", err)
	}
	if !snapshot.Missing {
		t.Fatal("missing .laps snapshot Missing = false")
	}

	if err := os.MkdirAll(filepath.Join(workspaceDir, ".laps"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", oldPath) })
	os.Setenv("PATH", t.TempDir())
	snapshot, err = (&Adapter{WorkspaceDir: workspaceDir}).QueueSnapshot(context.Background())
	if err != nil {
		t.Fatalf("missing binary error = %v", err)
	}
	if !snapshot.Missing {
		t.Fatal("missing binary snapshot Missing = false")
	}
}

func TestParseQueueListDecodesFixtureShape(t *testing.T) {
	entries, err := parseQueueList([]byte(fakeRootListJSON))
	if err != nil {
		t.Fatalf("parseQueueList error = %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	if entries[0].Kind != "lap" || entries[0].ID != "rall-1" || !entries[0].IsDone || entries[0].Assignee != "review" {
		t.Fatalf("lap entry = %+v", entries[0])
	}
	if entries[1].Kind != "stint" || entries[1].Ref != "alpha" {
		t.Fatalf("stint entry = %+v", entries[1])
	}
}
