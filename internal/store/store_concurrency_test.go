package store

import (
	"fmt"
	"sync"
	"testing"
)

// TestStoreConcurrentAccessRaceFree guards the Store mutex: one goroutine
// appends tries and agent-status events while several goroutines snapshot the
// store, mirroring the relay-writes-while-a-test-polls pattern that previously
// tripped the race detector. Run under -race, an unguarded cache access fails
// here directly in the store package rather than only in a relay E2E test.
func TestStoreConcurrentAccessRaceFree(t *testing.T) {
	_, s := setupTempStore(t)

	const writes = 200
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 1; i <= writes; i++ {
			if err := s.AppendTry(TryRecord{ID: i, RelayID: 1, AgentType: "opencode", Summary: "x"}); err != nil {
				t.Errorf("AppendTry: %v", err)
				return
			}
			if err := s.AppendAgentStatus(AgentStatusEvent{
				AgentType: "opencode", Model: "m", EventType: "active", Timestamp: fmt.Sprintf("%d", i),
			}); err != nil {
				t.Errorf("AppendAgentStatus: %v", err)
				return
			}
		}
	}()

	reader := func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = s.AllTries()
			_ = s.RecentTries(5, 1)
			_ = s.NextTryID()
			_ = s.AllAgentStatus()
			_, _ = s.GetAgentStatus("opencode", "m")
		}
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go reader()
	}

	wg.Wait()

	if got := len(s.AllTries()); got != writes {
		t.Fatalf("AllTries len = %d, want %d", got, writes)
	}
}
