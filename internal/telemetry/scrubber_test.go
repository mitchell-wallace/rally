package telemetry

import (
	"strings"
	"testing"

	"github.com/getsentry/sentry-go"
)

func TestScrubEvent_DropsSensitiveKeys(t *testing.T) {
	taskBody := strings.Repeat("a", 120_000) // ~120KB current_task.md
	event := &sentry.Event{
		Contexts: map[string]sentry.Context{
			"try": {
				"current_task": taskBody,
				"prompt":       "the full assembled prompt",
				"transcript":   "the full agent transcript",
				"role":         "senior",
				"prompt_bytes": 120000,
			},
		},
		Breadcrumbs: []*sentry.Breadcrumb{
			{Data: map[string]interface{}{"task_prompt": taskBody, "try_id": 7}},
		},
		Spans: []*sentry.Span{
			{Data: map[string]interface{}{"output": taskBody, "completed": false}},
		},
	}

	got := scrubEvent(event)

	ctx := got.Contexts["try"]
	for _, k := range []string{"current_task", "prompt", "transcript"} {
		if ctx[k] != scrubbedPlaceholder {
			t.Errorf("context key %q = %v, want %q", k, ctx[k], scrubbedPlaceholder)
		}
	}
	if ctx["role"] != "senior" {
		t.Errorf("non-sensitive role tag was altered: %v", ctx["role"])
	}
	if ctx["prompt_bytes"] != 120000 {
		t.Errorf("size field prompt_bytes was altered: %v", ctx["prompt_bytes"])
	}
	if got.Breadcrumbs[0].Data["task_prompt"] != scrubbedPlaceholder {
		t.Errorf("breadcrumb task_prompt not scrubbed: %v", got.Breadcrumbs[0].Data["task_prompt"])
	}
	if got.Breadcrumbs[0].Data["try_id"] != 7 {
		t.Errorf("breadcrumb try_id was altered: %v", got.Breadcrumbs[0].Data["try_id"])
	}
	if got.Spans[0].Data["output"] != scrubbedPlaceholder {
		t.Errorf("span output not scrubbed: %v", got.Spans[0].Data["output"])
	}
}

func TestScrubEvent_NeverShipsTaskBodyBytes(t *testing.T) {
	taskBody := strings.Repeat("SECRET", 30_000)
	event := &sentry.Event{
		Message: taskBody, // oversized string that slipped into the message
		Contexts: map[string]sentry.Context{
			"data": {"some_field": taskBody},
		},
	}

	got := scrubEvent(event)

	// The message is truncated well below the original size.
	if len(got.Message) > maxValueBytes+32 {
		t.Errorf("message length %d exceeds truncation ceiling", len(got.Message))
	}
	// A non-sensitive oversized string value is truncated, not shipped whole.
	if v := got.Contexts["data"]["some_field"].(string); len(v) > maxValueBytes+32 {
		t.Errorf("oversized value length %d exceeds truncation ceiling", len(v))
	}
}

func TestScrubEvent_NilSafe(t *testing.T) {
	if scrubEvent(nil) != nil {
		t.Fatal("scrubEvent(nil) should return nil")
	}
}

func TestTags_OmitsEmptyAndFormatsRunner(t *testing.T) {
	tags := Tags(EventInfo{
		RelayID: 3,
		RunID:   2,
		TryID:   9,
		Role:    "senior",
		Harness: "claude",
		Model:   "sonnet-4",
		Repo:    "rally",
		LapID:   "lap-12",
	})

	want := map[string]string{
		"relay_id": "3",
		"run_id":   "2",
		"try_id":   "9",
		"role":     "senior",
		"runner":   "claude:sonnet-4",
		"repo":     "rally",
		"lap_id":   "lap-12",
	}
	for k, v := range want {
		if tags[k] != v {
			t.Errorf("tag %q = %q, want %q", k, tags[k], v)
		}
	}

	// Empty/zero fields are omitted entirely.
	sparse := Tags(EventInfo{RelayID: 1, Harness: "codex"})
	if _, ok := sparse["lap_id"]; ok {
		t.Error("empty lap_id should be omitted")
	}
	if _, ok := sparse["run_id"]; ok {
		t.Error("zero run_id should be omitted")
	}
	if sparse["runner"] != "codex" {
		t.Errorf("runner with no model = %q, want %q", sparse["runner"], "codex")
	}
}
