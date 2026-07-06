package tuicore

// LapsSnapshot is the presentation-owned read model for the queue tab.
type LapsSnapshot struct {
	Missing     bool
	State       string
	Counts      LapsCounts
	Claim       LapsClaim
	ActiveStint string
	Gate        *LapsGate
	Entries     []LapsEntry
}

type LapsCounts struct {
	Todo  int
	Done  int
	Total int
}

type LapsClaim struct {
	Valid      bool
	Lap        string
	File       string
	ClaimedAt  string
	AgeSeconds int
}

type LapsGate struct {
	State   string
	Stint   string
	Scope   string
	File    string
	Message string
}

type LapsEntry struct {
	Kind     string
	ID       string
	Ref      string
	Title    string
	Assignee string
	IsDone   bool
	Order    int
	Stint    *LapsStint
	Laps     []LapsEntry
}

type LapsStint struct {
	Name     string
	Scope    string
	File     string
	Todo     int
	Done     int
	Total    int
	Queued   bool
	Archived bool
	Active   bool
	Laps     []LapsEntry
}

func CloneLapsSnapshot(snapshot LapsSnapshot) LapsSnapshot {
	out := snapshot
	if snapshot.Gate != nil {
		gate := *snapshot.Gate
		out.Gate = &gate
	}
	out.Entries = cloneLapsEntries(snapshot.Entries)
	return out
}

func cloneLapsEntries(entries []LapsEntry) []LapsEntry {
	out := make([]LapsEntry, len(entries))
	for i, entry := range entries {
		out[i] = entry
		out[i].Laps = cloneLapsEntries(entry.Laps)
		if entry.Stint != nil {
			stint := *entry.Stint
			stint.Laps = cloneLapsEntries(entry.Stint.Laps)
			out[i].Stint = &stint
		}
	}
	return out
}
