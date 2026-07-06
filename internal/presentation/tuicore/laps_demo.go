package tuicore

// DemoLapsSnapshot returns a compact queue fixture for TUI demo mode.
func DemoLapsSnapshot() LapsSnapshot {
	stintLaps := []LapsEntry{
		{Kind: "lap", ID: "laps-004", Title: "Load active try log into the terminal tab", Assignee: "senior"},
		{Kind: "lap", ID: "laps-005", Title: "Add operator action menu", Assignee: "architect"},
	}
	stint := &LapsStint{
		Name:   "operator-controls",
		Scope:  "tui",
		File:   "stints/operator-controls.laps",
		Todo:   2,
		Done:   1,
		Total:  3,
		Queued: true,
		Active: true,
		Laps:   cloneLapsEntries(stintLaps),
	}
	return LapsSnapshot{
		State:       "held",
		ActiveStint: "operator-controls",
		Counts:      LapsCounts{Todo: 4, Done: 3, Total: 7},
		Claim:       LapsClaim{Valid: true, Lap: "laps-003", AgeSeconds: 86},
		Gate:        &LapsGate{State: "held", Stint: "operator-controls", Message: "verification pass required before the next stint opens"},
		Entries: []LapsEntry{
			{Kind: "lap", ID: "laps-001", Title: "Collapse prototype commands into rally tui", Assignee: "senior", IsDone: true},
			{Kind: "lap", ID: "laps-002", Title: "Render the dashboard tab from runtime events", Assignee: "junior", IsDone: true},
			{Kind: "lap", ID: "laps-003", Title: "Add a Laps tab showing queue and stints", Assignee: "senior"},
			{Kind: "stint", ID: "stint-operator-controls", Ref: "operator-controls", Title: "Operator controls", Stint: stint, Laps: stintLaps},
			{Kind: "lap", ID: "laps-006", Title: "Write selection notes for the accepted TUI", Assignee: "review"},
		},
	}
}
