package store

import (
	"path/filepath"
	"testing"
)

func TestJSONLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tries.jsonl")

	recs := []TryRecord{
		{ID: 1, OutingID: 1, AgentType: "claude", Summary: "first"},
		{ID: 2, OutingID: 1, AgentType: "codex", Summary: "second"},
	}

	for _, r := range recs {
		if err := appendJSONL(path, r); err != nil {
			t.Fatal(err)
		}
	}

	read, err := readJSONL[TryRecord](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 2 {
		t.Fatalf("expected 2 records, got %d", len(read))
	}
	if read[0].ID != 1 || read[1].ID != 2 {
		t.Fatalf("unexpected IDs: %v", read)
	}

	// Test rewrite
	if err := rewriteJSONL(path, recs[:1]); err != nil {
		t.Fatal(err)
	}
	read, err = readJSONL[TryRecord](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 1 || read[0].ID != 1 {
		t.Fatalf("expected 1 record with ID 1, got %v", read)
	}
}

func TestReadJSONLMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.jsonl")
	recs, err := readJSONL[TryRecord](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("expected 0 records for missing file, got %d", len(recs))
	}
}
