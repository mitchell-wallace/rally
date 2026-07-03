package store

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mitchell-wallace/rally/internal/textutil"
)

func setupTempStore(t *testing.T) (string, *Store) {
	t.Helper()
	dir := t.TempDir()
	rallyDir := RallyDir(dir)
	if err := os.MkdirAll(rallyDir, 0755); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}
	return rallyDir, store
}

func mustAppendTry(t *testing.T, s *Store, rec TryRecord) {
	t.Helper()
	if err := s.AppendTry(rec); err != nil {
		t.Fatalf("AppendTry(%+v): %v", rec, err)
	}
}

func intPtr(i int) *int {
	return &i
}

func assertCappedFinalSnippet(t *testing.T, got, wantHead, wantTail string) {
	t.Helper()

	if !utf8.ValidString(got) {
		t.Fatalf("capped text is not valid UTF-8: %q", got)
	}
	if gotRunes := len([]rune(got)); gotRunes != FinalSnippetRuneLimit {
		t.Fatalf("capped text rune length = %d, want %d", gotRunes, FinalSnippetRuneLimit)
	}
	if !strings.Contains(got, textutil.HeadTailTruncationMarker) {
		t.Fatalf("capped text is missing marker %q", textutil.HeadTailTruncationMarker)
	}
	if !strings.HasPrefix(got, wantHead) {
		t.Fatalf("capped text does not preserve head %q", wantHead)
	}
	if !strings.HasSuffix(got, wantTail) {
		t.Fatalf("capped text does not preserve tail %q", wantTail)
	}
}
