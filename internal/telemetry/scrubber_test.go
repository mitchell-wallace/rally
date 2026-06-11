package telemetry

import (
	"context"
	"strings"
	"testing"

	"github.com/getsentry/sentry-go"
)

func TestCollapseHomePaths_DirectPaths(t *testing.T) {
	orig := homeDir
	homeDir = "/home/testuser"
	defer func() { homeDir = orig }()

	tests := []struct {
		name, in, want string
	}{
		{"exact home", "/home/testuser", "~"},
		{"cwd style", "/home/testuser/projects/rally", "~/projects/rally"},
		{"dotfile", "/home/testuser/.config/rally/config.toml", "~/.config/rally/config.toml"},
		{"trailing slash", "/home/testuser/", "~/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collapseHomePaths(tt.in)
			if got != tt.want {
				t.Errorf("collapseHomePaths(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCollapseHomePaths_EmbeddedInText(t *testing.T) {
	orig := homeDir
	homeDir = "/home/alice"
	defer func() { homeDir = orig }()

	in := "error at /home/alice/.config/rally/config.toml: permission denied"
	want := "error at ~/.config/rally/config.toml: permission denied"
	if got := collapseHomePaths(in); got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	in2 := "cwd=/home/alice/repos/rally, prev=/home/alice/repos/other"
	want2 := "cwd=~/repos/rally, prev=~/repos/other"
	if got := collapseHomePaths(in2); got != want2 {
		t.Errorf("got %q, want %q", got, want2)
	}
}

func TestCollapseHomePaths_UsernameStripped(t *testing.T) {
	orig := homeDir
	homeDir = "/home/sensitiveuser"
	defer func() { homeDir = orig }()

	got := collapseHomePaths("/home/sensitiveuser/project/file.go")
	if strings.Contains(got, "sensitiveuser") {
		t.Errorf("username still present after collapse: %q", got)
	}
	if got != "~/project/file.go" {
		t.Errorf("got %q, want ~/project/file.go", got)
	}
}

func TestCollapseHomePaths_NonHomePathsUntouched(t *testing.T) {
	orig := homeDir
	homeDir = "/home/testuser"
	defer func() { homeDir = orig }()

	tests := []struct {
		name, in, want string
	}{
		{"system path", "/usr/local/bin/go", "/usr/local/bin/go"},
		{"other user", "/home/otheruser/file.txt", "/home/otheruser/file.txt"},
		{"tmp", "/tmp/build-output", "/tmp/build-output"},
		{"empty string", "", ""},
		{"relative path", "relative/path/to/file", "relative/path/to/file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collapseHomePaths(tt.in)
			if got != tt.want {
				t.Errorf("collapseHomePaths(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCollapseHomePaths_EmptyHomeDir(t *testing.T) {
	orig := homeDir
	homeDir = ""
	defer func() { homeDir = orig }()

	in := "/home/whoever/file.go"
	if got := collapseHomePaths(in); got != in {
		t.Errorf("with empty homeDir, got %q, want %q unchanged", got, in)
	}
}

func TestScrubEvent_HomePathCollapseInMessage(t *testing.T) {
	orig := homeDir
	homeDir = "/home/testuser"
	defer func() { homeDir = orig }()

	event := &sentry.Event{
		Message: "failure in /home/testuser/project/main.go:42",
		Contexts: map[string]sentry.Context{
			"work": {
				"cwd":      "/home/testuser/project",
				"config":   "/etc/rally/config.toml",
				"log_path": "/home/testuser/.local/share/rally/log.txt",
			},
		},
	}

	got := scrubEvent(event)

	if strings.Contains(got.Message, "testuser") {
		t.Errorf("message still contains username: %q", got.Message)
	}
	if got.Message != "failure in ~/project/main.go:42" {
		t.Errorf("message = %q", got.Message)
	}

	ctx := got.Contexts["work"]
	if ctx["cwd"] != "~/project" {
		t.Errorf("cwd = %v, want ~/project", ctx["cwd"])
	}
	if ctx["config"] != "/etc/rally/config.toml" {
		t.Errorf("config = %v, want unchanged", ctx["config"])
	}
	if ctx["log_path"] != "~/.local/share/rally/log.txt" {
		t.Errorf("log_path = %v, want collapsed", ctx["log_path"])
	}
}

func TestScrubEvent_HomePathCollapseInBreadcrumbsAndSpans(t *testing.T) {
	orig := homeDir
	homeDir = "/home/testuser"
	defer func() { homeDir = orig }()

	event := &sentry.Event{
		Breadcrumbs: []*sentry.Breadcrumb{
			{Data: map[string]interface{}{
				"path":  "/home/testuser/.cache/rally/state.json",
				"other": "/opt/rally/bin",
			}},
		},
		Spans: []*sentry.Span{
			{Data: map[string]interface{}{
				"signal": "provider responded: /home/testuser/project/output",
				"count":  42,
			}},
		},
	}

	got := scrubEvent(event)

	if got.Breadcrumbs[0].Data["path"] != "~/.cache/rally/state.json" {
		t.Errorf("breadcrumb path = %v", got.Breadcrumbs[0].Data["path"])
	}
	if got.Breadcrumbs[0].Data["other"] != "/opt/rally/bin" {
		t.Errorf("breadcrumb other = %v", got.Breadcrumbs[0].Data["other"])
	}
	spanData := got.Spans[0].Data
	if spanData["signal"] != "provider responded: ~/project/output" {
		t.Errorf("span signal = %v", spanData["signal"])
	}
	if spanData["count"] != 42 {
		t.Errorf("span count altered: %v", spanData["count"])
	}
}

func TestScrubEvent_RecursivelyScrubsNestedValues(t *testing.T) {
	orig := homeDir
	homeDir = "/home/testuser"
	defer func() { homeDir = orig }()

	event := &sentry.Event{
		Contexts: map[string]sentry.Context{
			"nested": {
				"outer": map[string]interface{}{
					"path":   "/home/testuser/project/file.go",
					"prompt": "full prompt text",
					"items": []interface{}{
						"/home/testuser/.cache/rally/a.log",
						map[string]interface{}{"transcript": "full transcript", "cwd": "/home/testuser/repo"},
					},
				},
			},
		},
	}

	got := scrubEvent(event)
	outer := got.Contexts["nested"]["outer"].(map[string]interface{})
	if outer["path"] != "~/project/file.go" {
		t.Errorf("nested path = %v", outer["path"])
	}
	if outer["prompt"] != scrubbedPlaceholder {
		t.Errorf("nested prompt = %v, want scrubbed", outer["prompt"])
	}
	items := outer["items"].([]interface{})
	if items[0] != "~/.cache/rally/a.log" {
		t.Errorf("nested slice path = %v", items[0])
	}
	itemMap := items[1].(map[string]interface{})
	if itemMap["transcript"] != scrubbedPlaceholder {
		t.Errorf("nested transcript = %v, want scrubbed", itemMap["transcript"])
	}
	if itemMap["cwd"] != "~/repo" {
		t.Errorf("nested cwd = %v", itemMap["cwd"])
	}
}

func TestScrubEvent_SensitiveKeysStillScrubbedWithHomePaths(t *testing.T) {
	orig := homeDir
	homeDir = "/home/testuser"
	defer func() { homeDir = orig }()

	event := &sentry.Event{
		Contexts: map[string]sentry.Context{
			"try": {
				"prompt": "/home/testuser/secret/prompt.md",
				"cwd":    "/home/testuser/project",
				"role":   "senior",
			},
		},
	}

	got := scrubEvent(event)

	ctx := got.Contexts["try"]
	if ctx["prompt"] != scrubbedPlaceholder {
		t.Errorf("sensitive key prompt = %v, want scrubbed", ctx["prompt"])
	}
	if ctx["cwd"] != "~/project" {
		t.Errorf("cwd = %v, want ~/project", ctx["cwd"])
	}
	if ctx["role"] != "senior" {
		t.Errorf("role = %v, want senior", ctx["role"])
	}
}

func TestScrubEvent_ScrubsHostIdentityKeys(t *testing.T) {
	event := &sentry.Event{
		Contexts: map[string]sentry.Context{
			"env": {
				"hostname":    "workstation.local",
				"username":    "alice",
				"ip_address":  "192.0.2.10",
				"server_name": "workstation.local",
				"go_os":       "linux",
			},
		},
		Breadcrumbs: []*sentry.Breadcrumb{
			{Data: map[string]interface{}{"host": "workstation.local", "ip": "192.0.2.10"}},
		},
	}

	got := scrubEvent(event)
	ctx := got.Contexts["env"]
	for _, key := range []string{"hostname", "username", "ip_address", "server_name"} {
		if ctx[key] != scrubbedPlaceholder {
			t.Errorf("context identity key %q = %v, want scrubbed", key, ctx[key])
		}
	}
	if ctx["go_os"] != "linux" {
		t.Errorf("non-sensitive context go_os = %v", ctx["go_os"])
	}
	for _, key := range []string{"host", "ip"} {
		if got.Breadcrumbs[0].Data[key] != scrubbedPlaceholder {
			t.Errorf("breadcrumb identity key %q = %v, want scrubbed", key, got.Breadcrumbs[0].Data[key])
		}
	}
}

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

func TestScrubEvent_ServerNameNeutralized(t *testing.T) {
	// Simulate an event that has a host-derived server_name set by the SDK.
	event := &sentry.Event{
		ServerName: "my-actual-hostname.local",
		Message:    "test event",
	}

	got := scrubEvent(event)

	if got.ServerName == "my-actual-hostname.local" {
		t.Error("scrubEvent must not transmit the host-derived server_name")
	}
	if got.ServerName != anonymousServerName {
		t.Errorf("ServerName = %q, want %q", got.ServerName, anonymousServerName)
	}
}

func TestScrubEvent_ServerNameNotHostDerived(t *testing.T) {
	// Even with an empty ServerName, scrubEvent should set the static value.
	event := &sentry.Event{
		ServerName: "",
		Message:    "test",
	}

	got := scrubEvent(event)

	if got.ServerName != anonymousServerName {
		t.Errorf("ServerName = %q, want %q", got.ServerName, anonymousServerName)
	}
	// The value must not look like a hostname.
	if strings.Contains(got.ServerName, ".") {
		t.Errorf("ServerName %q looks like a hostname", got.ServerName)
	}
}

func TestFailureEvent_TagContextSeparation(t *testing.T) {
	// Verify that FailureEvent keeps tags and contexts separate.
	evt := FailureEvent{
		Tags: map[string]string{
			"relay_id": "5",
			"role":     "senior",
		},
		Contexts: map[string]map[string]interface{}{
			"rally": {
				"version": "1.0.0",
				"go_os":   "linux",
				"go_arch": "amd64",
				"term":    "xterm-256color",
			},
		},
	}

	// Tags should contain filterable scalars.
	if evt.Tags["relay_id"] != "5" {
		t.Errorf("tag relay_id = %q, want %q", evt.Tags["relay_id"], "5")
	}

	// Contexts should contain the rally block with nested data.
	rally, ok := evt.Contexts["rally"]
	if !ok {
		t.Fatal("expected rally context block")
	}
	if rally["version"] != "1.0.0" {
		t.Errorf("context version = %v, want %q", rally["version"], "1.0.0")
	}
	if rally["go_os"] != "linux" {
		t.Errorf("context go_os = %v, want %q", rally["go_os"], "linux")
	}

	// Tags must not contain context-only fields.
	for _, key := range []string{"version", "go_os", "go_arch", "term"} {
		if _, found := evt.Tags[key]; found {
			t.Errorf("tag %q should not exist — it belongs in context only", key)
		}
	}
}

func TestNoopSink_CaptureFailureWithContexts(t *testing.T) {
	var sink NoopSink
	ctx := context.Background()

	// Calling CaptureFailure with contexts should not panic.
	evt := FailureEvent{
		Tags: map[string]string{"k": "v"},
		Contexts: map[string]map[string]interface{}{
			"rally": {"version": "1.0.0"},
		},
	}
	sink.CaptureFailure(ctx, "test failure", evt)
}
