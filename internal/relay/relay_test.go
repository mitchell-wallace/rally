package relay

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/store"
)

func newRelayTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	rallyDir := store.RallyDir(dir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCreateRelay(t *testing.T) {
	s := newRelayTestStore(t)
	r, err := CreateRelay(s, 10, "cc cx")
	if err != nil {
		t.Fatalf("CreateRelay failed: %v", err)
	}
	if r.ID <= 0 {
		t.Fatalf("expected positive relay ID, got %d", r.ID)
	}
	if r.TargetIterations != 10 {
		t.Fatalf("expected target 10, got %d", r.TargetIterations)
	}
	if r.AgentMix != "cc cx" {
		t.Fatalf("expected agent mix 'cc cx', got %q", r.AgentMix)
	}
	if r.StartedAt == "" {
		t.Fatal("expected non-empty StartedAt")
	}
	if _, err := time.Parse(time.RFC3339, r.StartedAt); err != nil {
		t.Fatalf("StartedAt not RFC3339: %v", err)
	}
	if r.EndedAt != "" {
		t.Fatal("expected empty EndedAt for new relay")
	}
	if r.CompletedIterations != 0 {
		t.Fatalf("expected 0 completed, got %d", r.CompletedIterations)
	}

	got := s.GetRelay(r.ID)
	if got == nil {
		t.Fatal("relay not persisted in store")
	}
	if got.ID != r.ID {
		t.Fatalf("persisted ID mismatch: %d vs %d", got.ID, r.ID)
	}
}

func TestCreateRelaySecondGetsIncrementedID(t *testing.T) {
	s := newRelayTestStore(t)
	r1, err := CreateRelay(s, 10, "cc")
	if err != nil {
		t.Fatalf("CreateRelay 1 failed: %v", err)
	}
	r2, err := CreateRelay(s, 20, "cx")
	if err != nil {
		t.Fatalf("CreateRelay 2 failed: %v", err)
	}
	if r2.ID != r1.ID+1 {
		t.Fatalf("expected ID %d, got %d", r1.ID+1, r2.ID)
	}
}

func TestResumeRelay_FindsIncompleteRelay(t *testing.T) {
	s := newRelayTestStore(t)

	r1 := store.RelayRecord{
		ID:                  1,
		TargetIterations:    5,
		CompletedIterations: 5,
		AgentMix:            "cc",
		StartedAt:           time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
		EndedAt:             time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
	}
	r2 := store.RelayRecord{
		ID:                  2,
		TargetIterations:    10,
		CompletedIterations: 3,
		AgentMix:            "cx",
		StartedAt:           time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339),
	}
	if err := s.AppendRelay(r1); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendRelay(r2); err != nil {
		t.Fatal(err)
	}

	got, found, err := ResumeRelay(s)
	if err != nil {
		t.Fatalf("ResumeRelay failed: %v", err)
	}
	if !found {
		t.Fatal("expected to find an incomplete relay")
	}
	if got.ID != 2 {
		t.Fatalf("expected relay 2 (most recent incomplete), got %d", got.ID)
	}
	if got.AgentMix != "cx" {
		t.Fatalf("expected agent mix 'cx', got %q", got.AgentMix)
	}
}

func TestResumeRelay_NoIncompleteRelay(t *testing.T) {
	s := newRelayTestStore(t)

	r := store.RelayRecord{
		ID:                  1,
		TargetIterations:    5,
		CompletedIterations: 5,
		AgentMix:            "cc",
		StartedAt:           time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
		EndedAt:             time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
	}
	if err := s.AppendRelay(r); err != nil {
		t.Fatal(err)
	}

	got, found, err := ResumeRelay(s)
	if err != nil {
		t.Fatalf("ResumeRelay failed: %v", err)
	}
	if found {
		t.Fatalf("expected no resumable relay, got %+v", got)
	}
	if got != nil {
		t.Fatal("expected nil relay when none to resume")
	}
}

func TestResumeRelay_EmptyStore(t *testing.T) {
	s := newRelayTestStore(t)
	got, found, err := ResumeRelay(s)
	if err != nil {
		t.Fatalf("ResumeRelay failed: %v", err)
	}
	if found {
		t.Fatalf("expected no resumable relay in empty store, got %+v", got)
	}
	if got != nil {
		t.Fatal("expected nil relay for empty store")
	}
}

func TestCompleteRelay(t *testing.T) {
	s := newRelayTestStore(t)

	r := store.RelayRecord{
		ID:                  1,
		TargetIterations:    10,
		CompletedIterations: 10,
		AgentMix:            "cc cx",
		StartedAt:           time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
	}
	if err := s.AppendRelay(r); err != nil {
		t.Fatal(err)
	}

	if err := CompleteRelay(s, 1); err != nil {
		t.Fatalf("CompleteRelay failed: %v", err)
	}

	got := s.GetRelay(1)
	if got == nil {
		t.Fatal("relay not found after completion")
	}
	if got.EndedAt == "" {
		t.Fatal("expected non-empty EndedAt after CompleteRelay")
	}
	if _, err := time.Parse(time.RFC3339, got.EndedAt); err != nil {
		t.Fatalf("EndedAt not RFC3339: %v", err)
	}
}

func TestCompleteRelay_NotFound(t *testing.T) {
	s := newRelayTestStore(t)
	err := CompleteRelay(s, 99)
	if err == nil {
		t.Fatal("expected error for non-existent relay")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got %q", err.Error())
	}
}
