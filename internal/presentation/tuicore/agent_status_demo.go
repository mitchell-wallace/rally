package tuicore

import "time"

func DemoAgentStatuses() []AgentStatusItem {
	base := time.Date(2026, 7, 4, 14, 0, 0, 0, time.Local)
	return []AgentStatusItem{
		{Agent: "codex", Model: "gpt-5.5", State: "active", Reason: "latest run completed", Since: base.Add(18 * time.Minute)},
		{Agent: "claude", Model: "sonnet-4", State: "benched", Reason: "usage limit", Since: base.Add(-42 * time.Minute), ResetAt: base.Add(2 * time.Hour)},
		{Agent: "opencode", Model: "glm-5.2", State: "probation", Reason: "retry after transient harness error", Since: base.Add(-9 * time.Minute)},
	}
}
