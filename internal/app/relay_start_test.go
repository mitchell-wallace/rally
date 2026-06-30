package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestInspectResumeCurrentLayout(t *testing.T) {
	workspaceDir := t.TempDir()
	if err := os.MkdirAll(store.RallyDir(workspaceDir), 0o755); err != nil {
		t.Fatalf("mkdir rally dir: %v", err)
	}

	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.AppendRelay(store.RelayRecord{
		ID:                  1,
		TargetIterations:    8,
		CompletedIterations: 3,
		AgentMix:            "cc cx",
	}); err != nil {
		t.Fatalf("append unfinished relay: %v", err)
	}
	if err := s.AppendRelay(store.RelayRecord{
		ID:                  2,
		TargetIterations:    2,
		CompletedIterations: 2,
		AgentMix:            "op",
		EndedAt:             "2026-06-30T00:00:00Z",
	}); err != nil {
		t.Fatalf("append completed relay: %v", err)
	}
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: "codex",
		Model:     "default",
		EventType: "frozen",
		Timestamp: "2026-06-30T00:00:00Z",
		RelayID:   1,
		Reason:    "test freeze",
	}); err != nil {
		t.Fatalf("append agent status: %v", err)
	}

	info, err := InspectResume(workspaceDir)
	if err != nil {
		t.Fatalf("InspectResume: %v", err)
	}
	if !info.HasUnfinished {
		t.Fatal("HasUnfinished = false, want true")
	}
	if info.RelayID != 1 || info.CompletedIterations != 3 || info.TargetIterations != 8 || info.AgentMix != "cc cx" {
		t.Fatalf("ResumeInfo = %+v, want relay 1 at 3/8 with mix cc cx", info)
	}

	reloaded, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	relay := reloaded.GetRelay(1)
	if relay == nil {
		t.Fatal("relay 1 missing after InspectResume")
	}
	if relay.EndedAt != "" {
		t.Fatalf("relay EndedAt = %q, want empty", relay.EndedAt)
	}
	status := reloaded.AllAgentStatus()
	if len(status) != 1 || status[0].EventType != "frozen" {
		t.Fatalf("agent status = %+v, want one frozen event", status)
	}
}

func TestInspectResumeLegacyTopLevelLayoutMigratesAndDoesNotMutateStatus(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatalf("mkdir rally dir: %v", err)
	}

	writeLegacyJSONL(t, filepath.Join(rallyDir, "relays.jsonl"),
		store.RelayRecord{ID: 1, TargetIterations: 4, CompletedIterations: 4, AgentMix: "cc", EndedAt: "2026-06-30T00:00:00Z"},
		store.RelayRecord{ID: 2, TargetIterations: 10, CompletedIterations: 6, AgentMix: "__routes__"},
	)
	writeLegacyJSONL(t, filepath.Join(rallyDir, "agent_status.jsonl"),
		store.AgentStatusEvent{AgentType: "claude", Model: "opus", EventType: "benched", Timestamp: "2026-06-30T00:00:00Z", RelayID: 2},
	)

	info, err := InspectResume(workspaceDir)
	if err != nil {
		t.Fatalf("InspectResume: %v", err)
	}
	if !info.HasUnfinished {
		t.Fatal("HasUnfinished = false, want true")
	}
	if info.RelayID != 2 || info.CompletedIterations != 6 || info.TargetIterations != 10 || info.AgentMix != "__routes__" {
		t.Fatalf("ResumeInfo = %+v, want relay 2 at 6/10 with mix __routes__", info)
	}

	if _, err := os.Stat(filepath.Join(rallyDir, "relays.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("legacy relays.jsonl stat error = %v, want not exist after migration", err)
	}
	if _, err := os.Stat(filepath.Join(rallyDir, "state", "relays.jsonl")); err != nil {
		t.Fatalf("migrated relays.jsonl missing: %v", err)
	}

	reloaded, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	relay := reloaded.GetRelay(2)
	if relay == nil {
		t.Fatal("relay 2 missing after InspectResume")
	}
	if relay.EndedAt != "" {
		t.Fatalf("relay EndedAt = %q, want empty", relay.EndedAt)
	}
	status := reloaded.AllAgentStatus()
	if len(status) != 1 || status[0].EventType != "benched" {
		t.Fatalf("agent status = %+v, want one benched event", status)
	}
}

func TestStartRelayResolvedStartDecisions(t *testing.T) {
	t.Setenv("RALLY_TELEMETRY", "0")

	tests := []struct {
		name                  string
		discard               bool
		reset                 bool
		overwrite             bool
		wantRelayCount        int
		wantOriginalEnded     bool
		wantOriginalAgentMix  string
		wantAgentStatusEvents int
	}{
		{
			name:                  "resume keeps unfinished relay and status",
			wantRelayCount:        1,
			wantOriginalAgentMix:  "claude",
			wantAgentStatusEvents: 1,
		},
		{
			name:                  "interactive start new discards without reset",
			discard:               true,
			wantRelayCount:        2,
			wantOriginalEnded:     true,
			wantOriginalAgentMix:  "claude",
			wantAgentStatusEvents: 1,
		},
		{
			name:                  "flag start new discards and resets status",
			discard:               true,
			reset:                 true,
			wantRelayCount:        2,
			wantOriginalEnded:     true,
			wantOriginalAgentMix:  "claude",
			wantAgentStatusEvents: 0,
		},
		{
			name:                  "resume overwrite applies resolved mix",
			overwrite:             true,
			wantRelayCount:        1,
			wantOriginalAgentMix:  "opencode",
			wantAgentStatusEvents: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceDir := seededStartRelayWorkspace(t)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			var out, errOut bytes.Buffer
			err := StartRelay(ctx, RelayStartOptions{
				WorkspaceDir:           workspaceDir,
				Config:                 startRelayTestConfig(),
				AgentMixSpecs:          []string{"op"},
				TargetIters:            1,
				DataDir:                filepath.Join(t.TempDir(), "data"),
				DiscardUnfinishedRelay: tt.discard,
				ResetAgentStatus:       tt.reset,
				OverwriteMixOnResume:   tt.overwrite,
				Out:                    &out,
				Err:                    &errOut,
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("StartRelay error = %v, want context.Canceled", err)
			}
			if out.Len() != 0 {
				t.Fatalf("Out = %q, want empty before relay completion", out.String())
			}
			if errOut.Len() != 0 {
				t.Fatalf("Err = %q, want no app-layer prompting or warnings", errOut.String())
			}

			reloaded, err := store.NewStore(store.RallyDir(workspaceDir))
			if err != nil {
				t.Fatalf("reload store: %v", err)
			}
			relays := reloaded.AllRelays()
			if len(relays) != tt.wantRelayCount {
				t.Fatalf("relay count = %d, want %d: %+v", len(relays), tt.wantRelayCount, relays)
			}
			original := reloaded.GetRelay(1)
			if original == nil {
				t.Fatal("original relay missing")
			}
			if gotEnded := original.EndedAt != ""; gotEnded != tt.wantOriginalEnded {
				t.Fatalf("original EndedAt = %q, ended=%v want %v", original.EndedAt, gotEnded, tt.wantOriginalEnded)
			}
			if original.AgentMix != tt.wantOriginalAgentMix {
				t.Fatalf("original AgentMix = %q, want %q", original.AgentMix, tt.wantOriginalAgentMix)
			}
			if got := len(reloaded.AllAgentStatus()); got != tt.wantAgentStatusEvents {
				t.Fatalf("agent status events = %d, want %d", got, tt.wantAgentStatusEvents)
			}
		})
	}
}

func TestStartRelayCompletesZeroIterationRelayWithCapturedOutput(t *testing.T) {
	t.Setenv("RALLY_TELEMETRY", "0")

	workspaceDir := t.TempDir()
	if err := os.MkdirAll(store.RallyDir(workspaceDir), 0o755); err != nil {
		t.Fatalf("mkdir rally dir: %v", err)
	}

	var out, errOut bytes.Buffer
	err := StartRelay(context.Background(), RelayStartOptions{
		WorkspaceDir:  workspaceDir,
		Config:        startRelayTestConfig(),
		AgentMixSpecs: []string{"cc"},
		TargetIters:   0,
		DataDir:       filepath.Join(t.TempDir(), "data"),
		Out:           &out,
		Err:           &errOut,
	})
	if err != nil {
		t.Fatalf("StartRelay: %v", err)
	}
	if got, want := out.String(), "Relay complete.\n"; got != want {
		t.Fatalf("Out = %q, want %q", got, want)
	}
	if errOut.Len() != 0 {
		t.Fatalf("Err = %q, want empty", errOut.String())
	}

	reloaded, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	relay := reloaded.GetRelay(1)
	if relay == nil {
		t.Fatal("created relay missing")
	}
	if relay.AgentMix != "claude" {
		t.Fatalf("AgentMix = %q, want claude", relay.AgentMix)
	}
	if relay.EndedAt == "" {
		t.Fatal("zero-iteration relay was not completed")
	}
}

func TestAppImportInvariant(t *testing.T) {
	direct := goList(t, "-f", "{{.Imports}}", "./internal/app")
	for _, forbidden := range []string{
		"github.com/mitchell-wallace/rally/internal/user_prompt",
		"github.com/mitchell-wallace/rally/internal/laps",
	} {
		if strings.Contains(direct, forbidden) {
			t.Fatalf("internal/app direct imports contain %s: %s", forbidden, direct)
		}
	}

	_ = goList(t, "-deps", "./internal/app")
}

func seededStartRelayWorkspace(t *testing.T) string {
	t.Helper()

	workspaceDir := t.TempDir()
	if err := os.MkdirAll(store.RallyDir(workspaceDir), 0o755); err != nil {
		t.Fatalf("mkdir rally dir: %v", err)
	}

	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.AppendRelay(store.RelayRecord{
		ID:                  1,
		TargetIterations:    2,
		CompletedIterations: 0,
		AgentMix:            "claude",
	}); err != nil {
		t.Fatalf("append unfinished relay: %v", err)
	}
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: "claude",
		Model:     "default",
		EventType: "frozen",
		Timestamp: "2026-06-30T00:00:00Z",
		RelayID:   1,
		Reason:    "test freeze",
	}); err != nil {
		t.Fatalf("append agent status: %v", err)
	}
	return workspaceDir
}

func startRelayTestConfig() config.V2Config {
	return config.V2Config{
		ClaudeModel:      "claude-test",
		CodexModel:       "codex-test",
		OpenCodeModel:    "opencode-test",
		AntigravityModel: "antigravity-test",
	}
}

func goList(t *testing.T, args ...string) string {
	t.Helper()

	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = filepath.Clean(filepath.Join("..", ".."))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeLegacyJSONL[T any](t *testing.T, path string, records ...T) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	for _, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("marshal record: %v", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}
