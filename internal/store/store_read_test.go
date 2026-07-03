package store

import (
	"testing"
)

func TestRecentTriesAndRelays(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	for i := 1; i <= 5; i++ {
		_ = store.AppendTry(TryRecord{ID: i})
		_ = store.AppendRelay(RelayRecord{ID: i})
	}

	recent := store.RecentTries(3)
	if len(recent) != 3 {
		t.Fatalf("expected 3 recent tries, got %d", len(recent))
	}
	if recent[0].ID != 3 || recent[2].ID != 5 {
		t.Fatalf("unexpected recent tries: %v", recent)
	}

	recentRelays := store.RecentRelays(2)
	if len(recentRelays) != 2 || recentRelays[0].ID != 4 {
		t.Fatalf("unexpected recent relays: %v", recentRelays)
	}

	// Reload and verify
	store2, err := NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(store2.RecentTries(10)) != 5 {
		t.Fatal("recent tries not correct after reload")
	}
}
