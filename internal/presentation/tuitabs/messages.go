package tuitabs

import (
	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

type eventMsg struct {
	event runtimeevent.Event
}

type statusFrameMsg struct {
	line string
}

type transcriptLineMsg struct {
	line string
}

type doneMsg struct {
	err error
}

type enrichMsg struct {
	runIndex       int
	summary        string
	classification string
	followups      []string
}

type seedMessagesMsg struct {
	items []tuicore.MessageItem
}

type seedAgentsMsg struct {
	items []tuicore.AgentStatusItem
}
