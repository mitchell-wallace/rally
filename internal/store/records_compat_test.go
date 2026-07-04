package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTryRecordOutingIDJSONCompatibility(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "old only", input: `{"id":1,"run_id":7}`, want: 7},
		{name: "new only", input: `{"id":1,"outing_id":8}`, want: 8},
		{name: "equal both", input: `{"id":1,"run_id":9,"outing_id":9}`, want: 9},
		{name: "conflicting both", input: `{"id":1,"run_id":9,"outing_id":10}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rec TryRecord
			err := json.Unmarshal([]byte(tt.input), &rec)
			if tt.wantErr {
				if err == nil {
					t.Fatal("json.Unmarshal succeeded, want error")
				}
				if !strings.Contains(err.Error(), "outing_id") || !strings.Contains(err.Error(), "run_id") {
					t.Fatalf("error %q does not name both keys", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("json.Unmarshal error: %v", err)
			}
			if rec.OutingID != tt.want {
				t.Fatalf("OutingID = %d, want %d", rec.OutingID, tt.want)
			}
		})
	}
}

func TestTryRecordMarshalWritesOnlyOutingID(t *testing.T) {
	data, err := json.Marshal(TryRecord{ID: 1, OutingID: 7})
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	assertJSONKey(t, data, "outing_id")
	assertNoJSONKey(t, data, "run_id")
}

func TestMessageRecordConsumedByOutingIDJSONCompatibility(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "old only", input: `{"id":1,"consumed_by_run_id":7}`, want: 7},
		{name: "new only", input: `{"id":1,"consumed_by_outing_id":8}`, want: 8},
		{name: "equal both", input: `{"id":1,"consumed_by_run_id":9,"consumed_by_outing_id":9}`, want: 9},
		{name: "conflicting both", input: `{"id":1,"consumed_by_run_id":9,"consumed_by_outing_id":10}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rec MessageRecord
			err := json.Unmarshal([]byte(tt.input), &rec)
			if tt.wantErr {
				if err == nil {
					t.Fatal("json.Unmarshal succeeded, want error")
				}
				if !strings.Contains(err.Error(), "consumed_by_outing_id") || !strings.Contains(err.Error(), "consumed_by_run_id") {
					t.Fatalf("error %q does not name both keys", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("json.Unmarshal error: %v", err)
			}
			if rec.ConsumedByOutingID == nil || *rec.ConsumedByOutingID != tt.want {
				t.Fatalf("ConsumedByOutingID = %v, want %d", rec.ConsumedByOutingID, tt.want)
			}
		})
	}
}

func TestMessageRecordMarshalWritesOnlyConsumedByOutingID(t *testing.T) {
	outingID := 7
	data, err := json.Marshal(MessageRecord{ID: 1, ConsumedByOutingID: &outingID})
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	assertJSONKey(t, data, "consumed_by_outing_id")
	assertNoJSONKey(t, data, "consumed_by_run_id")
}

func TestLegacyStoreStateLoadsAndFreshWriteUsesNewKeys(t *testing.T) {
	workspaceDir := t.TempDir()
	stateDir := StateDir(workspaceDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "tries.jsonl"), []byte(`{"id":1,"run_id":3,"agent_type":"claude"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write legacy tries: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "messages.jsonl"), []byte(`{"id":1,"status":"pending","scope":"run","consumed_by_run_id":3}`+"\n"), 0o644); err != nil {
		t.Fatalf("write legacy messages: %v", err)
	}

	s, err := NewStore(RallyDir(workspaceDir))
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	if got := s.AllTries()[0].OutingID; got != 3 {
		t.Fatalf("legacy try OutingID = %d, want 3", got)
	}
	messages := s.GetMessages()
	if messages[0].ConsumedByOutingID == nil || *messages[0].ConsumedByOutingID != 3 {
		t.Fatalf("legacy message ConsumedByOutingID = %v, want 3", messages[0].ConsumedByOutingID)
	}

	if err := s.AppendTry(TryRecord{ID: 2, OutingID: 4}); err != nil {
		t.Fatalf("AppendTry error: %v", err)
	}
	outingID := 4
	messages[0].ConsumedByOutingID = &outingID
	if err := s.UpdateMessage(messages[0]); err != nil {
		t.Fatalf("UpdateMessage error: %v", err)
	}

	tryData, err := os.ReadFile(filepath.Join(stateDir, "tries.jsonl"))
	if err != nil {
		t.Fatalf("read tries: %v", err)
	}
	if !strings.Contains(string(tryData), `"outing_id":4`) {
		t.Fatalf("fresh try write missing outing_id: %s", tryData)
	}
	messageData, err := os.ReadFile(filepath.Join(stateDir, "messages.jsonl"))
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	if strings.Contains(string(messageData), "consumed_by_run_id") {
		t.Fatalf("fresh message rewrite still contains consumed_by_run_id: %s", messageData)
	}
	if !strings.Contains(string(messageData), `"consumed_by_outing_id":4`) {
		t.Fatalf("fresh message rewrite missing consumed_by_outing_id: %s", messageData)
	}
}

func assertJSONKey(t *testing.T, data []byte, key string) {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
	if _, ok := raw[key]; !ok {
		t.Fatalf("JSON %s missing key %q", data, key)
	}
}

func assertNoJSONKey(t *testing.T, data []byte, key string) {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
	if _, ok := raw[key]; ok {
		t.Fatalf("JSON %s unexpectedly contains key %q", data, key)
	}
}
