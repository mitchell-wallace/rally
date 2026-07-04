package tuicore

import "time"

func DemoMessages() []MessageItem {
	base := time.Date(2026, 7, 4, 13, 15, 0, 0, time.Local)
	return []MessageItem{
		{ID: 21, Body: "Please verify the new tab chrome still preserves Ctrl+C double-press quit semantics before handing off.", Status: "pending", Position: 1, Scope: "relay", CreatedAt: base},
		{ID: 22, Body: "The dashboard tab should reuse prototype 2 directly so tui-2 remains a regression reference.", Status: "pending", Position: 2, Scope: "run", CreatedAt: base.Add(4 * time.Minute)},
		{ID: 18, Body: "Earlier concern about summary.jsonl ordering was addressed by seeding recent tries in store order.", Status: "addressed", Position: 3, Scope: "relay", CreatedAt: base.Add(-25 * time.Minute)},
		{ID: 17, Body: "Cancelled stale request to wire message compose controls into this prototype.", Status: "cancelled", Position: 4, Scope: "run", CreatedAt: base.Add(-35 * time.Minute)},
	}
}

func DemoAgentStatuses() []AgentStatusItem {
	base := time.Date(2026, 7, 4, 14, 0, 0, 0, time.Local)
	return []AgentStatusItem{
		{Agent: "codex", Model: "gpt-5.5", State: "active", Reason: "latest run completed", Since: base.Add(18 * time.Minute)},
		{Agent: "claude", Model: "sonnet-4", State: "benched", Reason: "usage limit", Since: base.Add(-42 * time.Minute), ResetAt: base.Add(2 * time.Hour)},
		{Agent: "opencode", Model: "glm-5.2", State: "probation", Reason: "retry after transient harness error", Since: base.Add(-9 * time.Minute)},
	}
}
