package tuicore

import (
	"sort"
	"time"
)

type MessageItem struct {
	ID        int
	Body      string
	Status    string
	Position  int
	Scope     string
	CreatedAt time.Time
}

type MessageList struct {
	items []MessageItem
}

func (l *MessageList) Seed(items []MessageItem) {
	l.items = append(l.items[:0], cloneMessageItems(items)...)
	sort.SliceStable(l.items, func(i, j int) bool {
		leftPending := l.items[i].Status == "pending"
		rightPending := l.items[j].Status == "pending"
		if leftPending != rightPending {
			return leftPending
		}
		if l.items[i].Position != l.items[j].Position {
			return l.items[i].Position < l.items[j].Position
		}
		return l.items[i].ID < l.items[j].ID
	})
}

func (l MessageList) Items() []MessageItem {
	return cloneMessageItems(l.items)
}

func cloneMessageItems(items []MessageItem) []MessageItem {
	out := make([]MessageItem, len(items))
	copy(out, items)
	return out
}

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
