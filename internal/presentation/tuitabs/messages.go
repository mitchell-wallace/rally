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
	// hint, when non-empty, replaces the static done banner with an
	// end-state-aware line (e.g. stopped-with-work-remaining vs complete).
	hint string
}

type enrichMsg struct {
	outingIndex    int
	summary        string
	classification string
	followups      []string
}

type seedFeedMsg struct {
	items []tuicore.FeedItem
}

type seedAgentsMsg struct {
	items []tuicore.AgentStatusItem
}

type lapsSnapshotMsg struct {
	snapshot tuicore.LapsSnapshot
	err      error
}
