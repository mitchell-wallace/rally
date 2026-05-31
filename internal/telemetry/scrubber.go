package telemetry

import (
	"strings"

	"github.com/getsentry/sentry-go"
)

// maxValueBytes caps the size of any single string value transmitted in an
// event payload. Telemetry only ships summaries and metadata, so this is a
// defensive ceiling that guards against an unexpectedly large value (a stray
// transcript, a giant error string) leaking through.
const maxValueBytes = 4096

// scrubbedPlaceholder replaces the value of a sensitive key.
const scrubbedPlaceholder = "[scrubbed]"

// sensitiveKeys names payload fields that must NEVER be transmitted: the
// assembled task prompt (current_task.md) and full agent transcripts. We never
// place these into an event payload in the first place — only summaries, byte
// sizes, and metadata — so this denylist is defense-in-depth. Keys are matched
// case-insensitively against an exact lowercased key, so size fields like
// "task_prompt_bytes" are preserved while "task_prompt" is dropped.
var sensitiveKeys = map[string]struct{}{
	"current_task":     {},
	"current_task.md":  {},
	"prompt":           {},
	"task_prompt":      {},
	"assembled_prompt": {},
	"transcript":       {},
	"full_transcript":  {},
	"full_log":         {},
	"log":              {},
	"logs":             {},
	"output":           {},
}

func isSensitiveKey(key string) bool {
	_, ok := sensitiveKeys[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

// scrubEvent strips sensitive payloads and truncates oversized string values
// from an outgoing event. It is wired as Sentry's BeforeSend/BeforeSendTransaction
// hook. It is a pure transformation over the event, safe to unit test directly.
func scrubEvent(event *sentry.Event) *sentry.Event {
	if event == nil {
		return nil
	}

	event.Message = truncateValue(event.Message)

	for _, ctx := range event.Contexts {
		scrubMap(ctx)
	}
	for _, b := range event.Breadcrumbs {
		if b != nil {
			scrubMap(b.Data)
		}
	}
	for _, s := range event.Spans {
		if s != nil {
			scrubMap(s.Data)
		}
	}
	return event
}

// scrubMap drops sensitive keys and truncates oversized string values in place.
func scrubMap(m map[string]interface{}) {
	for k, v := range m {
		if isSensitiveKey(k) {
			m[k] = scrubbedPlaceholder
			continue
		}
		if s, ok := v.(string); ok {
			m[k] = truncateValue(s)
		}
	}
}

func truncateValue(s string) string {
	if len(s) <= maxValueBytes {
		return s
	}
	return s[:maxValueBytes] + "…[truncated]"
}
