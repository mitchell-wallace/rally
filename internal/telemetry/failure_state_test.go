package telemetry

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFailureStateTags_ScalarFields(t *testing.T) {
	reset := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	fs := FailureState{
		Attempt:     2,
		MaxAttempts: 5,
		Category:    categoryUsageLimit,
		AgentState:  "benched",
		QuotaScope:  "anthropic",
		ResetAt:     &reset,
		ResetAfter:  90 * time.Minute,
	}
	tags := FailureStateTags(fs)

	want := map[string]string{
		"attempt":          "2",
		"max_attempts":     "5",
		"failure_category": "usage_limit",
		"agent_state":      "benched",
		"quota_scope":      "anthropic",
		"reset_at":         "2026-06-11T12:00:00Z",
		"reset_after":      "1h30m0s",
	}
	for k, v := range want {
		if tags[k] != v {
			t.Errorf("tag %q = %q, want %q", k, tags[k], v)
		}
	}
}

func TestFailureStateTags_OmitsZeroValues(t *testing.T) {
	tags := FailureStateTags(FailureState{Category: "agent_error"})

	for _, k := range []string{"attempt", "max_attempts", "agent_state"} {
		if _, found := tags[k]; found {
			t.Errorf("tag %q must be omitted when unset", k)
		}
	}
	if tags["failure_category"] != "agent_error" {
		t.Errorf("failure_category = %q, want agent_error", tags["failure_category"])
	}
}

func TestFailureStateTags_ResetAndQuotaOnlyForLimitCategories(t *testing.T) {
	reset := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	// A non-limit category that nonetheless carries reset/quota data must not
	// surface those as tags — they are meaningful only for limit categories.
	fs := FailureState{
		Category:   "agent_error",
		QuotaScope: "anthropic",
		ResetAt:    &reset,
		ResetAfter: 30 * time.Minute,
	}
	tags := FailureStateTags(fs)

	for _, k := range []string{"quota_scope", "reset_at", "reset_after"} {
		if _, found := tags[k]; found {
			t.Errorf("tag %q must be omitted for non-limit category", k)
		}
	}
}

func TestFailureStateTags_LimitCategoryOmitsAbsentResetFields(t *testing.T) {
	// A limit category with no reset evidence attaches no reset tags.
	tags := FailureStateTags(FailureState{Category: categoryShortRateLimit})
	for _, k := range []string{"quota_scope", "reset_at", "reset_after"} {
		if _, found := tags[k]; found {
			t.Errorf("tag %q must be omitted when not present", k)
		}
	}
	if tags["failure_category"] != "short_rate_limit" {
		t.Errorf("failure_category = %q", tags["failure_category"])
	}
}

func TestFailureStateTags_NeverIncludesRawSignalOrMessage(t *testing.T) {
	tags := FailureStateTags(FailureState{
		Category:  categoryUsageLimit,
		RawSignal: "hit your usage limit",
		Message:   "quota exhausted",
	})
	for _, k := range []string{"raw_signal", "message"} {
		if _, found := tags[k]; found {
			t.Errorf("free-text field %q must never be a tag", k)
		}
	}
}

func TestFailureEvidenceContext_PresentForLimitCategories(t *testing.T) {
	for _, cat := range []string{categoryUsageLimit, categoryShortRateLimit, categoryProviderOverloaded} {
		ctx := FailureEvidenceContext(FailureState{
			Category:  cat,
			RawSignal: "429 Too Many Requests",
			Message:   "rate limited",
		})
		if ctx == nil {
			t.Fatalf("category %q: expected failure_evidence context, got nil", cat)
		}
		if ctx["raw_signal"] != "429 Too Many Requests" {
			t.Errorf("category %q: raw_signal = %v", cat, ctx["raw_signal"])
		}
		if ctx["message"] != "rate limited" {
			t.Errorf("category %q: message = %v", cat, ctx["message"])
		}
	}
}

func TestFailureEvidenceContext_AbsentForNonLimitCategories(t *testing.T) {
	for _, cat := range []string{"agent_error", "invalid_model", "incomplete_finalization", "transient_infra", ""} {
		ctx := FailureEvidenceContext(FailureState{
			Category:  cat,
			RawSignal: "some provider text",
			Message:   "a message",
		})
		if ctx != nil {
			t.Errorf("category %q: expected no failure_evidence context, got %v", cat, ctx)
		}
	}
}

func TestFailureEvidenceContext_EmptyWhenNoSignalOrMessage(t *testing.T) {
	if ctx := FailureEvidenceContext(FailureState{Category: categoryUsageLimit}); ctx != nil {
		t.Errorf("expected nil context when no raw signal or message, got %v", ctx)
	}
}

func TestFailureEvidenceContext_ScrubsHomePaths(t *testing.T) {
	prev := SetHomeDir(filepath.FromSlash("/home/alice"))
	defer SetHomeDir(prev)

	rawPath := filepath.FromSlash("/home/alice/.rally/state failed: usage limit")
	msgPath := filepath.FromSlash("see /home/alice/logs/run.log")
	ctx := FailureEvidenceContext(FailureState{
		Category:  categoryUsageLimit,
		RawSignal: rawPath,
		Message:   msgPath,
	})

	rawSignal, _ := ctx["raw_signal"].(string)
	message, _ := ctx["message"].(string)
	if want := "~" + filepath.FromSlash("/.rally/state failed: usage limit"); rawSignal != want {
		t.Errorf("raw_signal = %q, want home-collapsed %q", rawSignal, want)
	}
	if want := "see ~" + filepath.FromSlash("/logs/run.log"); message != want {
		t.Errorf("message = %q, want home-collapsed %q", message, want)
	}
	for _, v := range []string{rawSignal, message} {
		if strings.Contains(v, "alice") {
			t.Errorf("username leaked into context value %q", v)
		}
	}
}

func TestFailureEvidenceContext_TruncatesOversizedSignal(t *testing.T) {
	big := make([]byte, maxValueBytes+500)
	for i := range big {
		big[i] = 'x'
	}
	ctx := FailureEvidenceContext(FailureState{
		Category:  categoryUsageLimit,
		RawSignal: string(big),
	})
	got, _ := ctx["raw_signal"].(string)
	if !strings.HasSuffix(got, "…[truncated]") {
		t.Errorf("oversized raw_signal not truncated: len=%d", len(got))
	}
}

func TestFailureEvidenceContext_ScrubsRealisticProviderPayload(t *testing.T) {
	prev := SetHomeDir(filepath.FromSlash("/home/developer"))
	defer SetHomeDir(prev)

	raw := `error: plan /home/developer/.config/rally/cache/model-4.json: usage limit reached. ` +
		`prompt="analyze /home/developer/projects/repo/src/main.go" transcript=full output=log`
	msg := `provider rejected request for /home/developer/.rally/state: quota exhausted. ` +
		`see /home/developer/logs/agent-trace.log`

	ctx := FailureEvidenceContext(FailureState{
		Category:  categoryUsageLimit,
		RawSignal: raw,
		Message:   msg,
	})

	rawSignal, _ := ctx["raw_signal"].(string)
	message, _ := ctx["message"].(string)

	for _, v := range []string{rawSignal, message} {
		if strings.Contains(v, "developer") {
			t.Errorf("username leaked into context value %q", v)
		}
		if strings.Contains(v, "/home/developer") {
			t.Errorf("unresolved home path in context value %q", v)
		}
	}

	allowed := map[string]struct{}{"raw_signal": {}, "message": {}}
	for k := range ctx {
		if _, ok := allowed[k]; !ok {
			t.Errorf("unexpected key %q in failure_evidence context", k)
		}
		if isSensitiveKey(k) {
			t.Errorf("sensitive key %q must never appear in failure_evidence", k)
		}
	}

	if !strings.Contains(rawSignal, "~") {
		t.Errorf("raw_signal should contain collapsed home path: %q", rawSignal)
	}
}

func TestFailureStateTags_AllLimitCategoriesGetQuotaFields(t *testing.T) {
	reset := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	for _, cat := range []string{categoryUsageLimit, categoryShortRateLimit, categoryProviderOverloaded} {
		t.Run(cat, func(t *testing.T) {
			fs := FailureState{
				Attempt:     1,
				MaxAttempts: 3,
				Category:    cat,
				AgentState:  "active",
				QuotaScope:  "provider-a",
				ResetAt:     &reset,
				ResetAfter:  30 * time.Minute,
			}
			tags := FailureStateTags(fs)
			if tags["quota_scope"] != "provider-a" {
				t.Errorf("quota_scope = %q, want %q", tags["quota_scope"], "provider-a")
			}
			if tags["reset_at"] == "" {
				t.Error("reset_at tag missing for limit category")
			}
			if tags["reset_after"] == "" {
				t.Error("reset_after tag missing for limit category")
			}
		})
	}
}

func TestFailureStateTags_NonLimitCategoriesNeverGetQuotaFields(t *testing.T) {
	reset := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	for _, cat := range []string{"agent_error", "invalid_model", "transient_infra", "auth_or_proxy", "harness_launch", "incomplete_finalization", ""} {
		t.Run(cat, func(t *testing.T) {
			fs := FailureState{
				Attempt:     1,
				MaxAttempts: 3,
				Category:    cat,
				AgentState:  "active",
				QuotaScope:  "provider-a",
				ResetAt:     &reset,
				ResetAfter:  30 * time.Minute,
				RawSignal:   "some signal text",
				Message:     "some message",
			}
			tags := FailureStateTags(fs)
			for _, k := range []string{"quota_scope", "reset_at", "reset_after"} {
				if _, found := tags[k]; found {
					t.Errorf("tag %q must not appear for non-limit category %q", k, cat)
				}
			}
		})
	}
}

func TestFailureEvidenceContext_NonLimitCategory_WithEvidenceFields(t *testing.T) {
	for _, cat := range []string{"agent_error", "invalid_model", "transient_infra", "harness_launch", "incomplete_finalization", ""} {
		t.Run(cat, func(t *testing.T) {
			ctx := FailureEvidenceContext(FailureState{
				Category:  cat,
				RawSignal: "provider returned error: you exceeded your usage limit",
				Message:   "the agent could not complete",
			})
			if ctx != nil {
				t.Errorf("category %q: expected nil failure_evidence context even with raw_signal/message, got %v", cat, ctx)
			}
		})
	}
}

func TestFailureEvidenceContext_NoPromptOrTranscriptFields(t *testing.T) {
	ctx := FailureEvidenceContext(FailureState{
		Category:  categoryUsageLimit,
		RawSignal: "usage limit",
		Message:   "quota exhausted",
	})
	// Only raw_signal and message are ever attached; no sensitive payload keys.
	allowed := map[string]struct{}{"raw_signal": {}, "message": {}}
	for k := range ctx {
		if _, ok := allowed[k]; !ok {
			t.Errorf("unexpected key %q in failure_evidence context", k)
		}
		if isSensitiveKey(k) {
			t.Errorf("sensitive key %q must never appear in failure_evidence", k)
		}
	}
	for _, k := range []string{"prompt", "transcript", "current_task", "task_prompt", "output", "log"} {
		if _, found := ctx[k]; found {
			t.Errorf("prompt/transcript-looking key %q must not be attached", k)
		}
	}
}
