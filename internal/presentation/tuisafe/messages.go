package tuisafe

import "github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"

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
