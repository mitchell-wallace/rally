package tuicore

import (
	"sort"
	"time"
)

type AgentStatusItem struct {
	Agent   string
	Model   string
	State   string
	Reason  string
	Since   time.Time
	ResetAt time.Time
}

type AgentStatusList struct {
	items []AgentStatusItem
}

func (l *AgentStatusList) Seed(items []AgentStatusItem) {
	l.items = append(l.items[:0], items...)
	sort.SliceStable(l.items, func(i, j int) bool {
		if l.items[i].Agent != l.items[j].Agent {
			return l.items[i].Agent < l.items[j].Agent
		}
		return l.items[i].Model < l.items[j].Model
	})
}

func (l AgentStatusList) Items() []AgentStatusItem {
	out := make([]AgentStatusItem, len(l.items))
	copy(out, l.items)
	return out
}
