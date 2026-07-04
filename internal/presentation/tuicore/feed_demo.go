package tuicore

import (
	"time"
)

// DemoFeedSeed returns historical rows that predate DemoScript's live events.
func DemoFeedSeed() []FeedItem {
	base := time.Date(2026, 7, 4, 12, 45, 0, 0, time.Local)
	return []FeedItem{
		{
			RunIndex:       -3,
			Agent:          "opencode",
			Model:          "glm-5.2",
			RoleLabel:      "junior",
			Title:          "Inventory current relay state files and invariants",
			StartedAt:      base,
			Outcome:        OutcomePassed,
			Duration:       8*time.Minute + 11*time.Second,
			Files:          4,
			CommitHash:     "9e7b111",
			CommitTitle:    "inventory relay state",
			Summary:        "Mapped the persisted relay files and confirmed the queue state is committed with each lap boundary.",
			Classification: "implementation",
		},
		{
			RunIndex:       -2,
			Agent:          "codex",
			Model:          "gpt-5.5",
			RoleLabel:      "senior",
			Title:          "Sketch presentation boundary options",
			StartedAt:      base.Add(10 * time.Minute),
			Outcome:        OutcomeHandoff,
			Duration:       11*time.Minute + 7*time.Second,
			Files:          2,
			CommitHash:     "2d4a870",
			CommitTitle:    "draft tui boundary notes",
			Summary:        "Separated terminal sink parity from richer TUI layout work and left follow-up implementation slices.",
			Classification: "handoff",
			Followups: []string{
				"Build the safe alternate-screen adapter first.",
				"Keep store/progress loading in the CLI composition layer.",
			},
		},
		{
			RunIndex:       -1,
			Agent:          "codex",
			Model:          "gpt-5.5",
			RoleLabel:      "verify",
			Title:          "Verify prototype 1 adapter mechanics",
			StartedAt:      base.Add(25 * time.Minute),
			Outcome:        OutcomePassed,
			Duration:       5*time.Minute + 42*time.Second,
			Files:          1,
			CommitHash:     "efb4baa",
			CommitTitle:    "add tuisafe prototype",
			Summary:        "Confirmed the FIFO drainer, status frame capture, and double-press control bridge work under demo playback.",
			Classification: "verification",
		},
	}
}
