package telemetry

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestNewRelicCustomEventNamesFixed(t *testing.T) {
	for _, name := range []string{
		newRelicEventRallyTry,
		newRelicEventRallyDiagnostic,
		newRelicEventRallyFailure,
	} {
		if !isNewRelicCustomEventName(name) {
			t.Fatalf("%q should be an allowed New Relic custom event name", name)
		}
	}
	for _, name := range []string{"", "Try", "RallyLog", "rally_failure"} {
		if isNewRelicCustomEventName(name) {
			t.Fatalf("%q should not be an allowed New Relic custom event name", name)
		}
	}
}

func TestBuildAttributes_FlattensScalarOnlyContexts(t *testing.T) {
	attrs := buildAttributes(
		map[string]string{
			"relay_id": "9",
			"role":     "senior",
		},
		map[string]map[string]interface{}{
			"rally": {
				"version": "0.9.1",
				"go_os":   "linux",
				"nested": map[string]interface{}{
					"count":   int32(3),
					"enabled": true,
					"ratio":   float32(1.25),
				},
				"items": []interface{}{"drop", "this"},
				"obj":   struct{ Name string }{Name: "drop"},
			},
		},
	)

	want := map[string]interface{}{
		"relay_id":             "9",
		"role":                 "senior",
		"rally.version":        "0.9.1",
		"rally.go_os":          "linux",
		"rally.nested.count":   int64(3),
		"rally.nested.enabled": true,
		"rally.nested.ratio":   float64(1.25),
	}
	for key, wantValue := range want {
		if got := attrs[key]; got != wantValue {
			t.Fatalf("attrs[%q] = %#v (%T), want %#v (%T)", key, got, got, wantValue, wantValue)
		}
	}
	for _, key := range []string{"rally", "rally.nested", "rally.items", "rally.obj"} {
		if _, ok := attrs[key]; ok {
			t.Fatalf("non-scalar attribute %q should not be emitted", key)
		}
	}
	for key, value := range attrs {
		if !isTestScalar(value) {
			t.Fatalf("attribute %q has non-scalar value %#v (%T)", key, value, value)
		}
	}
}

func TestBuildEventAttributes_IncludesLevel(t *testing.T) {
	attrs := buildEventAttributes(Event{
		Level: LevelWarning,
		Tags:  map[string]string{"event_kind": "lap_pin_mismatch"},
	})

	if attrs["level"] != "warning" {
		t.Fatalf("level = %#v, want warning", attrs["level"])
	}
	if attrs["event_kind"] != "lap_pin_mismatch" {
		t.Fatalf("event_kind = %#v, want lap_pin_mismatch", attrs["event_kind"])
	}
}

func TestBuildAttributes_EnforcesAttributeAndKeyBudget(t *testing.T) {
	context := make(map[string]interface{})
	for i := 0; i < maxNewRelicAttributes+20; i++ {
		context[fmt.Sprintf("field_%03d", i)] = i
	}
	context[strings.Repeat("k", newRelicAttributeKeyByteLimit)] = "drop"

	attrs := buildAttributes(nil, map[string]map[string]interface{}{"ctx": context})

	if len(attrs) > maxNewRelicAttributes {
		t.Fatalf("attribute count = %d, want <= %d", len(attrs), maxNewRelicAttributes)
	}
	for key := range attrs {
		if len(key) >= newRelicAttributeKeyByteLimit {
			t.Fatalf("attribute key %q has %d bytes, want < %d", key, len(key), newRelicAttributeKeyByteLimit)
		}
	}
	for key, value := range attrs {
		if key == "ctx."+strings.Repeat("k", newRelicAttributeKeyByteLimit) || value == "drop" {
			t.Fatalf("oversized key was emitted as %q=%#v", key, value)
		}
	}
}

func TestBuildAttributes_PrioritizesCorrelationAndFailureFieldsOverBudget(t *testing.T) {
	tags := map[string]string{
		"relay_id":                "100",
		"run_id":                  "8",
		"try_id":                  "3",
		"repo":                    "rally",
		"lap_id":                  "rall-b45c",
		"runner":                  "codex:gpt-5",
		"role":                    "senior",
		"outcome":                 "failed",
		"failure_category":        "usage_limit",
		"recovery_classification": "needs_user",
		"agent_state":             "active",
	}
	context := make(map[string]interface{})
	for i := 0; i < 100; i++ {
		context[fmt.Sprintf("field_%03d", i)] = i
	}

	attrs := buildFailureAttributes(FailureEvent{
		Tags:     tags,
		Contexts: map[string]map[string]interface{}{"ctx": context},
	})

	if len(attrs) != maxNewRelicAttributes {
		t.Fatalf("attribute count = %d, want %d", len(attrs), maxNewRelicAttributes)
	}
	retainedPriority := 0
	for _, key := range priorityAttributeKeys {
		wantValue, present := tags[key]
		if !present {
			continue
		}
		retainedPriority++
		if attrs[key] != wantValue {
			t.Fatalf("priority attribute %q = %#v, want %q", key, attrs[key], wantValue)
		}
	}

	remainingBudget := maxNewRelicAttributes - retainedPriority
	for i := 0; i < remainingBudget; i++ {
		key := fmt.Sprintf("ctx.field_%03d", i)
		if _, ok := attrs[key]; !ok {
			t.Fatalf("deterministic lower-priority key %q should be retained", key)
		}
	}
	droppedKey := fmt.Sprintf("ctx.field_%03d", remainingBudget)
	if _, ok := attrs[droppedKey]; ok {
		t.Fatalf("lower-priority key %q should be dropped after budget is exhausted", droppedKey)
	}
}

func TestBuildAttributes_DropsOversizedContextPayloadDeterministically(t *testing.T) {
	payload := make(map[string]interface{})
	for i := 0; i < 90; i++ {
		payload[fmt.Sprintf("part_%03d", i)] = strings.Repeat("x", 200)
	}

	attrs := buildAttributes(
		map[string]string{"relay_id": "1"},
		map[string]map[string]interface{}{
			"failure_evidence": {
				"message": "provider overloaded",
				"payload": payload,
			},
		},
	)

	if len(attrs) > maxNewRelicAttributes {
		t.Fatalf("attribute count = %d, want <= %d", len(attrs), maxNewRelicAttributes)
	}
	if attrs["relay_id"] != "1" {
		t.Fatalf("relay_id = %#v, want 1", attrs["relay_id"])
	}
	if attrs["failure_evidence.message"] != "provider overloaded" {
		t.Fatalf("message = %#v, want provider overloaded", attrs["failure_evidence.message"])
	}
	for _, key := range []string{"failure_evidence", "failure_evidence.payload"} {
		if _, ok := attrs[key]; ok {
			t.Fatalf("nested payload should not be JSON-encoded into %q", key)
		}
	}
	for key, value := range attrs {
		if s, ok := value.(string); ok && strings.Contains(s, `"part_`) {
			t.Fatalf("attribute %q looks like JSON-encoded nested payload: %.80q", key, s)
		}
	}

	for i := 0; i < 62; i++ {
		key := fmt.Sprintf("failure_evidence.payload.part_%03d", i)
		if _, ok := attrs[key]; !ok {
			t.Fatalf("expected deterministic payload leaf %q to survive", key)
		}
	}
	if _, ok := attrs["failure_evidence.payload.part_062"]; ok {
		t.Fatalf("payload leaf part_062 should be dropped after budget is exhausted")
	}
}

func TestBuildAttributes_ScrubsSensitiveKeysHomePathsAndLongStrings(t *testing.T) {
	prev := SetHomeDir("/home/alice")
	defer SetHomeDir(prev)

	placeholder := strings.Join([]string{"[", "scrubbed", "]"}, "")
	longValue := strings.Repeat("x", maxValueBytes+500)
	attrs := buildAttributes(
		map[string]string{
			"relay_id":        "2",
			"prompt":          "full prompt",
			"current_task.md": "full task",
			"transcript":      "full transcript",
			"log":             "raw log",
			"hostname":        "workstation.local",
			"user":            "alice",
			"username":        "alice",
			"role":            "senior",
			"failure.message": "path /home/alice/.config/rally/config.toml",
		},
		map[string]map[string]interface{}{
			"try": {
				"current_task": "/home/alice/secret/current_task.md",
				"prompt":       "full prompt",
				"transcript":   "full transcript",
				"log":          "raw log",
				"host":         "workstation.local",
				"username":     "alice",
				"user":         "alice",
				"cwd":          "/home/alice/work/rally",
				"raw_signal":   "config at /home/alice/.config/rally/config.toml",
				"long":         longValue,
			},
		},
	)

	for _, key := range []string{
		"prompt",
		"current_task.md",
		"transcript",
		"log",
		"hostname",
		"user",
		"username",
		"try.current_task",
		"try.prompt",
		"try.transcript",
		"try.log",
		"try.host",
		"try.username",
		"try.user",
	} {
		if _, ok := attrs[key]; ok {
			t.Fatalf("sensitive attribute %q should be dropped", key)
		}
	}
	for key, value := range attrs {
		if value == placeholder {
			t.Fatalf("attribute %q retained scrubbed placeholder", key)
		}
		if s, ok := value.(string); ok && strings.Contains(s, "/home/alice") {
			t.Fatalf("attribute %q still contains home path: %q", key, s)
		}
	}
	if attrs["try.cwd"] != "~/work/rally" {
		t.Fatalf("try.cwd = %#v, want ~/work/rally", attrs["try.cwd"])
	}
	if attrs["try.raw_signal"] != "config at ~/.config/rally/config.toml" {
		t.Fatalf("try.raw_signal = %#v, want home-collapsed path", attrs["try.raw_signal"])
	}
	gotLong, ok := attrs["try.long"].(string)
	if !ok {
		t.Fatalf("try.long = %#v, want string", attrs["try.long"])
	}
	if len(gotLong) >= len(longValue) {
		t.Fatalf("try.long length = %d, want truncated below %d", len(gotLong), len(longValue))
	}
	if !strings.HasSuffix(gotLong, "[truncated]") {
		t.Fatalf("try.long should have truncation marker")
	}
	if attrs["role"] != "senior" {
		t.Fatalf("role = %#v, want senior", attrs["role"])
	}
	if attrs["failure.message"] != "path ~/.config/rally/config.toml" {
		t.Fatalf("failure.message = %#v, want collapsed home path", attrs["failure.message"])
	}
}

func TestBuildAttributes_DropsInvalidNumericValues(t *testing.T) {
	attrs := buildAttributes(nil, map[string]map[string]interface{}{
		"ctx": {
			"nan": math.NaN(),
			"inf": math.Inf(1),
			"ok":  3.5,
		},
	})

	if _, ok := attrs["ctx.nan"]; ok {
		t.Fatalf("NaN attribute should be dropped")
	}
	if _, ok := attrs["ctx.inf"]; ok {
		t.Fatalf("Inf attribute should be dropped")
	}
	if attrs["ctx.ok"] != 3.5 {
		t.Fatalf("ctx.ok = %#v, want 3.5", attrs["ctx.ok"])
	}
}

func isTestScalar(value interface{}) bool {
	switch value.(type) {
	case string, bool, int64, uint64, float64:
		return true
	default:
		return false
	}
}
