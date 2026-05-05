package routing

import (
	"fmt"
	"sync"
)

type EntryState struct {
	Entry           ParsedEntry
	ConsecutiveRuns int
	Exhausted       bool
	RangeQuotaUsed  int
}

type Scheduler struct {
	mu      sync.Mutex
	entries []*EntryState
	pos     int
	cycle   int
}

func NewScheduler(entries []ParsedEntry) *Scheduler {
	states := make([]*EntryState, len(entries))
	for i, e := range entries {
		states[i] = &EntryState{Entry: e}
	}
	return &Scheduler{entries: states, pos: 0, cycle: 0}
}

func (s *Scheduler) quotaMet(st *EntryState, idx int) bool {
	if !st.Entry.HasQuota {
		return false
	}
	if st.Entry.QuotaRange() {
		if st.ConsecutiveRuns >= st.Entry.QuotaMax {
			return true
		}
		if st.ConsecutiveRuns >= st.Entry.QuotaMin && s.anyOtherAvailable(idx) {
			return true
		}
		return false
	}
	return st.ConsecutiveRuns >= st.Entry.QuotaMin
}

func (s *Scheduler) Next() (*EntryState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.entries) == 0 {
		return nil, fmt.Errorf("scheduler: no entries in route")
	}

	st, idx := s.findValid()
	if st != nil {
		wrapped := idx < s.pos
		if wrapped {
			s.cycle++
			s.resetCycle()
		}
		st.ConsecutiveRuns++
		if st.Entry.QuotaRange() {
			st.RangeQuotaUsed++
		}
		s.pos = idx
		return st, nil
	}

	allExhausted := true
	for _, e := range s.entries {
		if !e.Exhausted {
			allExhausted = false
			break
		}
	}
	if allExhausted {
		return nil, fmt.Errorf("scheduler: all entries exhausted")
	}

	s.cycle++
	s.resetCycle()
	s.pos = 0

	st, idx = s.findValid()
	if st != nil {
		st.ConsecutiveRuns++
		if st.Entry.QuotaRange() {
			st.RangeQuotaUsed++
		}
		s.pos = idx
		return st, nil
	}

	return nil, fmt.Errorf("scheduler: all entries exhausted")
}

func (s *Scheduler) findValid() (*EntryState, int) {
	for i := 0; i < len(s.entries); i++ {
		idx := (s.pos + i) % len(s.entries)
		st := s.entries[idx]
		if st.Exhausted {
			continue
		}
		if s.quotaMet(st, idx) {
			continue
		}
		return st, idx
	}
	return nil, -1
}

func (s *Scheduler) resetCycle() {
	for _, st := range s.entries {
		st.ConsecutiveRuns = 0
		st.RangeQuotaUsed = 0
		st.Exhausted = false
	}
}

func (s *Scheduler) anyOtherAvailable(exclude int) bool {
	for i := 0; i < len(s.entries); i++ {
		if i == exclude {
			continue
		}
		if s.entries[i].Exhausted {
			continue
		}
		if s.entries[i].Entry.QuotaRange() && s.entries[i].ConsecutiveRuns >= s.entries[i].Entry.QuotaMax {
			continue
		}
		return true
	}
	return false
}

func (s *Scheduler) OnAgentFailed(entry *EntryState, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.Exhausted = true
}

func (s *Scheduler) OnAgentRecovered(entry *EntryState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.Exhausted = false
}

func (s *Scheduler) AllExhausted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.entries {
		if !st.Exhausted {
			if st.Entry.QuotaRange() && st.ConsecutiveRuns >= st.Entry.QuotaMax {
				continue
			}
			return false
		}
	}
	return true
}

func (s *Scheduler) Pos() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pos
}

func (s *Scheduler) Cycle() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cycle
}

func (s *Scheduler) EntryStates() []*EntryState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*EntryState, len(s.entries))
	copy(out, s.entries)
	return out
}
