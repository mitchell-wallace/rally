package routing

import (
	"fmt"
	"sync"
)

type EntryState struct {
	Position        int
	Entry           ParsedEntry
	ConsecutiveRuns int
	Exhausted       bool
	Frozen          bool
	RangeQuotaUsed  int
}

type Selection struct {
	Prev    *EntryState
	Current *EntryState
}

type Scheduler struct {
	mu      sync.Mutex
	entries []*EntryState
	pos     int
	cycle   int
}

func NewScheduler(entries []ParsedEntry) *Scheduler {
	states := make([]*EntryState, len(entries))
	for i, entry := range entries {
		states[i] = &EntryState{
			Position: i,
			Entry:    entry,
		}
	}

	return &Scheduler{
		entries: states,
		pos:     -1,
	}
}

func (s *Scheduler) Next() (*Selection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.entries) == 0 {
		return nil, fmt.Errorf("scheduler: no entries in route")
	}

	if s.pos == -1 {
		current, err := s.selectFromLocked(0)
		if err != nil {
			return nil, err
		}
		return &Selection{Current: current}, nil
	}

	current := s.entries[s.pos]
	prev := cloneEntryState(current)
	if s.shouldStayOnCurrentLocked(current) {
		return &Selection{
			Prev:    prev,
			Current: s.recordSelectionLocked(current),
		}, nil
	}

	if current.Entry.QuotaRange() && current.ConsecutiveRuns >= current.Entry.QuotaMax && !s.hasAlternativeLocked(current.Position) {
		return nil, fmt.Errorf("scheduler: all entries exhausted; waiting for recovery")
	}

	next, err := s.advanceLocked()
	if err != nil {
		return nil, err
	}
	return &Selection{
		Prev:    prev,
		Current: next,
	}, nil
}

func (s *Scheduler) shouldStayOnCurrentLocked(current *EntryState) bool {
	if current.Frozen || current.Exhausted {
		return false
	}

	if !current.Entry.HasQuota {
		return true
	}

	if current.Entry.QuotaSingle() {
		return current.ConsecutiveRuns < current.Entry.QuotaMin
	}

	if current.ConsecutiveRuns < current.Entry.QuotaMin {
		return true
	}

	if current.ConsecutiveRuns >= current.Entry.QuotaMax {
		return false
	}

	return !s.hasAlternativeLocked(current.Position)
}

func (s *Scheduler) advanceLocked() (*EntryState, error) {
	next, ok := s.findSelectableLocked(s.pos + 1)
	if ok {
		return s.selectAtLocked(next), nil
	}

	s.cycle++
	s.resetCycleLocked()

	next, ok = s.findSelectableLocked(0)
	if ok {
		return s.selectAtLocked(next), nil
	}

	return nil, fmt.Errorf("scheduler: all entries exhausted; waiting for recovery")
}

func (s *Scheduler) findSelectableLocked(start int) (int, bool) {
	if start < 0 {
		start = 0
	}

	for idx := start; idx < len(s.entries); idx++ {
		if s.isSelectableLocked(s.entries[idx]) {
			return idx, true
		}
	}

	return -1, false
}

func (s *Scheduler) isSelectableLocked(entry *EntryState) bool {
	return !entry.Frozen && !entry.Exhausted
}

func (s *Scheduler) hasAlternativeLocked(exclude int) bool {
	for _, entry := range s.entries {
		if entry.Position == exclude {
			continue
		}
		if s.isSelectableLocked(entry) {
			return true
		}
	}

	return false
}

func (s *Scheduler) selectFromLocked(start int) (*EntryState, error) {
	next, ok := s.findSelectableLocked(start)
	if !ok {
		return nil, fmt.Errorf("scheduler: all entries exhausted; waiting for recovery")
	}

	return s.selectAtLocked(next), nil
}

func (s *Scheduler) selectAtLocked(idx int) *EntryState {
	entry := s.entries[idx]
	if s.pos != idx {
		entry.ConsecutiveRuns = 0
		entry.RangeQuotaUsed = 0
	}

	s.pos = idx
	return s.recordSelectionLocked(entry)
}

func (s *Scheduler) recordSelectionLocked(entry *EntryState) *EntryState {
	entry.Exhausted = false
	entry.ConsecutiveRuns++
	if entry.Entry.QuotaRange() {
		entry.RangeQuotaUsed++
	}
	return entry
}

func (s *Scheduler) resetCycleLocked() {
	for _, entry := range s.entries {
		entry.ConsecutiveRuns = 0
		entry.RangeQuotaUsed = 0
		entry.Exhausted = false
	}
}

func (s *Scheduler) OnAgentFailed(entry *EntryState, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = reason
	entry.Exhausted = true
	entry.Frozen = true
}

func (s *Scheduler) OnAgentRecovered(entry *EntryState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry.Exhausted = false
	entry.Frozen = false
	entry.ConsecutiveRuns = 0
	entry.RangeQuotaUsed = 0
}

func (s *Scheduler) AllExhausted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.entries) == 0 {
		return true
	}

	if s.pos != -1 {
		current := s.entries[s.pos]
		if s.shouldStayOnCurrentLocked(current) {
			return false
		}
		if current.Entry.QuotaRange() && current.ConsecutiveRuns >= current.Entry.QuotaMax && !s.hasAlternativeLocked(current.Position) {
			return true
		}
	}

	for _, entry := range s.entries {
		if !entry.Frozen {
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

func cloneEntryState(entry *EntryState) *EntryState {
	if entry == nil {
		return nil
	}
	clone := *entry
	return &clone
}
