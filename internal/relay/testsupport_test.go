package relay

import (
	"os"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/store"
)

func newResilienceTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testResilience(s *store.Store, now time.Time) *Resilience {
	return &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		HourlyRetriesBeforeFreeze: 5,
		NowFunc:                   func() time.Time { return now },
	}
}

func key(harness, model string) ResilienceKey {
	return ResilienceKey{Harness: harness, Model: model}
}
