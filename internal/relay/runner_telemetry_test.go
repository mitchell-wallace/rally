package relay

import (
	"testing"

	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

const testFullMachineID = "0123456789abcdef0123456789abcdef"

// recordingSpan captures the tags and data set on it so tests can assert what
// the relay span path attaches without standing up a real telemetry sink.
type recordingSpan struct {
	tags map[string]string
	data map[string]interface{}
}

func newRecordingSpan() *recordingSpan {
	return &recordingSpan{tags: map[string]string{}, data: map[string]interface{}{}}
}

func (s *recordingSpan) SetTag(k, v string)              { s.tags[k] = v }
func (s *recordingSpan) SetData(k string, v interface{}) { s.data[k] = v }
func (s *recordingSpan) Finish()                         {}

func testRallyContext() telemetry.RallyContext {
	return telemetry.RallyContext{
		RelayID:        3,
		RelayStartedAt: "2026-06-11T08:30:00Z",
		Repo:           "repo-abc123",
		MachineID:      testFullMachineID,
		Cwd:            "/srv/work/project",
	}
}

func TestRunner_rallyContext(t *testing.T) {
	r := &Runner{cfg: Config{WorkspaceDir: "/srv/work/project", MachineID: testFullMachineID}}
	relay := &store.RelayRecord{ID: 9, StartedAt: "2026-06-11T08:30:00Z"}

	rc := r.rallyContext(relay)

	if rc.RelayID != 9 {
		t.Errorf("RelayID = %d, want 9", rc.RelayID)
	}
	if rc.RelayStartedAt != "2026-06-11T08:30:00Z" {
		t.Errorf("RelayStartedAt = %q", rc.RelayStartedAt)
	}
	if rc.MachineID != testFullMachineID {
		t.Errorf("MachineID = %q, want full id", rc.MachineID)
	}
	if rc.Cwd != "/srv/work/project" {
		t.Errorf("Cwd = %q", rc.Cwd)
	}
	// Repo must reuse the existing repo-key derivation from the logging path.
	if want := repoKey("/srv/work/project"); rc.Repo != want {
		t.Errorf("Repo = %q, want %q (repoKey)", rc.Repo, want)
	}
}

func TestApplyRallyContext_SpanReceivesIdentityAndContext(t *testing.T) {
	rc := testRallyContext()
	span := newRecordingSpan()
	base := telemetry.Tags(telemetry.EventInfo{RelayID: rc.RelayID, Repo: rc.Repo})

	applyRallyContext(span, base, rc)

	// Base correlation tag preserved.
	if span.tags["relay_id"] != "3" {
		t.Errorf("relay_id tag = %q, want 3", span.tags["relay_id"])
	}
	// Machine-identity tags attached.
	if span.tags["machine_id_prefix"] != "0123456789ab" {
		t.Errorf("machine_id_prefix tag = %q", span.tags["machine_id_prefix"])
	}
	if span.tags["relay_guid"] == "" {
		t.Error("relay_guid tag missing on span")
	}
	if span.tags["relay_started_at"] != "2026-06-11T08:30:00Z" {
		t.Errorf("relay_started_at tag = %q", span.tags["relay_started_at"])
	}
	// Full machine id is context-only, never a tag.
	for k, v := range span.tags {
		if v == testFullMachineID {
			t.Errorf("full machine id leaked into span tag %q", k)
		}
	}
	// The rally context block is attached as span data with the full id.
	block, ok := span.data["rally"].(map[string]interface{})
	if !ok {
		t.Fatalf("span rally data = %T, want map", span.data["rally"])
	}
	if block["machine_id"] != testFullMachineID {
		t.Errorf("rally context machine_id = %v, want full id", block["machine_id"])
	}
}

func TestRallyFailure_TagsAndContext(t *testing.T) {
	rc := testRallyContext()
	base := telemetry.Tags(telemetry.EventInfo{RelayID: rc.RelayID, RunID: 2, Repo: rc.Repo})

	evt := rallyFailure(base, rc)

	// Base correlation tags survive the merge.
	if evt.Tags["relay_id"] != "3" {
		t.Errorf("relay_id tag = %q, want 3", evt.Tags["relay_id"])
	}
	if evt.Tags["run_id"] != "2" {
		t.Errorf("run_id tag = %q, want 2", evt.Tags["run_id"])
	}
	// Machine-identity tags merged in.
	if evt.Tags["machine_id_prefix"] != "0123456789ab" {
		t.Errorf("machine_id_prefix tag = %q", evt.Tags["machine_id_prefix"])
	}
	if evt.Tags["relay_guid"] == "" {
		t.Error("relay_guid tag missing on failure event")
	}
	// Full machine id is never a tag.
	for k, v := range evt.Tags {
		if v == testFullMachineID {
			t.Errorf("full machine id leaked into failure tag %q", k)
		}
	}
	// The rally context block carries the full machine id.
	block, ok := evt.Contexts["rally"]
	if !ok {
		t.Fatal("failure event missing rally context block")
	}
	if block["machine_id"] != testFullMachineID {
		t.Errorf("rally context machine_id = %v, want full id", block["machine_id"])
	}
}

func TestRallyFailure_DoesNotMutateBaseTags(t *testing.T) {
	rc := testRallyContext()
	base := telemetry.Tags(telemetry.EventInfo{RelayID: rc.RelayID, Repo: rc.Repo})

	_ = rallyFailure(base, rc)

	// The caller's map (also applied to a span) must be untouched by the merge.
	if _, found := base["relay_guid"]; found {
		t.Error("rallyFailure mutated the caller's base tag map")
	}
	if _, found := base["machine_id_prefix"]; found {
		t.Error("rallyFailure mutated the caller's base tag map")
	}
}

func TestLapPinMismatchDiagnosticEventCarriesReasonWithoutFailureCategory(t *testing.T) {
	rc := testRallyContext()
	base := telemetry.Tags(telemetry.EventInfo{RelayID: rc.RelayID, RunID: 2, TryID: 4, Repo: rc.Repo, LapID: "lap-1"})
	base["failure_category"] = "usage_limit"

	tests := []struct {
		name   string
		reason string
	}{
		{name: "wrong lap", reason: "wrong_lap_consumed"},
		{name: "multiple laps", reason: "multi_lap_consumed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := lapPinMismatchDiagnosticEvent(base, rc, telemetry.FailureState{
				Attempt:     1,
				MaxAttempts: 2,
				Outcome:     "failed",
				Category:    "agent_error",
				AgentState:  "active",
			}, tt.reason)

			if evt.Level != telemetry.LevelWarning {
				t.Fatalf("event level = %q, want %q", evt.Level, telemetry.LevelWarning)
			}
			wantTag(t, evt.Tags, "event_kind", "lap_pin_mismatch")
			wantTag(t, evt.Tags, "mismatch_reason", tt.reason)
			wantTag(t, evt.Tags, "outcome", "failed")
			wantTag(t, evt.Tags, "attempt", "1")
			wantTag(t, evt.Tags, "max_attempts", "2")
			wantTag(t, evt.Tags, "agent_state", "active")
			wantNoTag(t, evt.Tags, "failure_category")
			if evt.Level == telemetry.LevelError {
				t.Fatal("lap-pin mismatch diagnostic must not be error-level")
			}
		})
	}

	if base["failure_category"] != "usage_limit" {
		t.Fatalf("base tags were mutated, failure_category = %q", base["failure_category"])
	}
}
