package runner

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

func applyTags(span telemetry.Span, tags map[string]string) {
	for k, v := range tags {
		span.SetTag(k, v)
	}
}

// rallyContext builds the telemetry RallyContext for a relay: the anonymous
// machine identity, relay start, repo key, and home-collapsed cwd attached to
// the relay span and every captured failure.
func (r *Runner) rallyContext(relay *store.RelayRecord) telemetry.RallyContext {
	return telemetry.RallyContext{
		RelayID:        relay.ID,
		RelayStartedAt: relay.StartedAt,
		Repo:           repoKey(r.cfg.WorkspaceDir),
		RepoName:       repoDisplayName(r.cfg.WorkspaceDir),
		MachineID:      r.cfg.MachineID,
		Cwd:            r.cfg.WorkspaceDir,
	}
}

// applyRallyContext attaches the base correlation tags, the machine-identity /
// relay-guid tags, and the `rally` context block to a span. It is the span-side
// twin of rallyFailure so the relay span and every captured failure carry the
// same identity. The full machine id rides only in the `rally` data block, never
// as a tag.
func applyRallyContext(span telemetry.Span, baseTags map[string]string, rc telemetry.RallyContext) {
	applyTags(span, baseTags)
	applyTags(span, telemetry.MachineTags(rc))
	span.SetData("rally", telemetry.RallyContextBlock(rc))
}

// rallyFailure assembles a FailureEvent that carries the correlation tags, the
// machine-identity / relay-guid tags, and the `rally` context block. The base
// tags are copied so callers that also applied them to a span are unaffected.
func rallyFailure(tags map[string]string, rc telemetry.RallyContext) telemetry.FailureEvent {
	merged := make(map[string]string, len(tags)+3)
	for k, v := range tags {
		merged[k] = v
	}
	for k, v := range telemetry.MachineTags(rc) {
		merged[k] = v
	}
	return telemetry.FailureEvent{
		Tags:     merged,
		Contexts: map[string]map[string]interface{}{"rally": telemetry.RallyContextBlock(rc)},
	}
}

// failureStateEvent layers a failure-state snapshot onto a rally failure event:
// the attempt/max_attempts/failure_category/agent_state tags (plus
// quota_scope/reset for limit categories), and the bounded failure_evidence
// context block for limit categories. It is the one place the runner folds the
// structured failure state onto a captured failure, so the terminal-try,
// unfinalized, and relay-stall sites attach consistent fields. Telemetry never
// re-classifies here — callers pass the category/evidence already resolved in
// runOne. Sites that lack a field (e.g. the relay-level all-frozen capture has
// no attempt or reset evidence) simply leave it zero, and FailureStateTags omits
// it.
func failureStateEvent(baseTags map[string]string, rc telemetry.RallyContext, fs telemetry.FailureState) telemetry.FailureEvent {
	evt := rallyFailure(baseTags, rc)
	for k, v := range telemetry.FailureStateTags(fs) {
		evt.Tags[k] = v
	}
	if ec := telemetry.FailureEvidenceContext(fs); ec != nil {
		evt.Contexts["failure_evidence"] = ec
	}
	evt.Fingerprint = telemetry.FailureFingerprint(evt.Tags)
	return evt
}

func limitSignalEvent(baseTags map[string]string, rc telemetry.RallyContext, fs telemetry.FailureState) (telemetry.Event, bool) {
	if !runnerLimitCategory(fs.Category) {
		return telemetry.Event{}, false
	}
	evt := failureStateEvent(baseTags, rc, fs)
	if _, ok := evt.Contexts["failure_evidence"]; !ok {
		return telemetry.Event{}, false
	}
	evt.Tags["event_kind"] = "limit_signal"
	return telemetry.Event{
		Level:    telemetry.LevelInfo,
		Tags:     evt.Tags,
		Contexts: evt.Contexts,
	}, true
}

func runnerLimitCategory(category string) bool {
	switch reliability.FailureCategory(category) {
	case reliability.CategoryUsageLimit, reliability.CategoryShortRateLimit, reliability.CategoryProviderOverloaded:
		return true
	default:
		return false
	}
}

func applyEvidenceToFailureState(fs *telemetry.FailureState, ev *reliability.FailureEvidence, source string) {
	if fs == nil || ev == nil {
		return
	}
	if ev.QuotaScope != "" {
		fs.QuotaScope = ev.QuotaScope
	}
	fs.ResetAt = ev.ResetAt
	fs.ResetAfter = ev.ResetAfter
	if runnerLimitCategory(string(ev.Category)) {
		fs.RawSignal = ev.RawSignal
		fs.Message = ev.Message
		return
	}
	fs.EvidenceRawSignal = ev.RawSignal
	fs.EvidenceMessage = ev.Message
	// Prefer the evidence's own Source tag (set by the classification path)
	// over the caller-supplied default — this is how dirty_tree / text_pattern
	// / unmatched evidence propagates its source through to telemetry.
	if ev.Source != "" {
		fs.EvidenceSource = ev.Source
	} else {
		fs.EvidenceSource = source
	}
}

func applySafeExecErrorEvidence(fs *telemetry.FailureState, err error) {
	if fs == nil || err == nil {
		return
	}
	if fs.RawSignal != "" || fs.Message != "" || fs.EvidenceRawSignal != "" || fs.EvidenceMessage != "" {
		return
	}
	fs.EvidenceRawSignal = err.Error()
	fs.EvidenceSource = "safe_exec_error"
}

func addFailureEvidenceTelemetry(span telemetry.Span, fields map[string]interface{}, fs telemetry.FailureState) {
	evidence := telemetry.FailureEvidenceContext(fs)
	if len(evidence) == 0 {
		return
	}
	if span != nil {
		span.SetData("failure_evidence", evidence)
	}
	for k, v := range telemetry.FailureEvidenceFields(fs) {
		fields[k] = v
	}
}

func lapPinMismatchDiagnosticEvent(baseTags map[string]string, rc telemetry.RallyContext, fs telemetry.FailureState, reason string, expectedLapID string, consumedLapIDs []string) telemetry.Event {
	evt := failureStateEvent(baseTags, rc, fs)
	delete(evt.Tags, "failure_category")
	evt.Tags["event_kind"] = "lap_pin_mismatch"
	evt.Tags["mismatch_reason"] = reason
	if expectedLapID != "" {
		evt.Tags["expected_lap_id"] = expectedLapID
	}
	evt.Tags["consumed_lap_count"] = fmt.Sprintf("%d", len(consumedLapIDs))
	if len(consumedLapIDs) > 0 {
		evt.Tags["consumed_lap_ids"] = strings.Join(consumedLapIDs, ",")
	}
	return telemetry.Event{
		Level:    telemetry.LevelWarning,
		Tags:     evt.Tags,
		Contexts: evt.Contexts,
	}
}

// agentStateName reports the failing runner's current resilience standing using
// the verbatim active/probation/frozen/benched vocabulary, for the agent_state
// tag on captured failures. It reads persisted state from the store so it
// reflects the runner's standing at capture time.
func (r *Runner) agentStateName(picked harnessapi.ResolvedAgent) string {
	res := r.resilience
	if res == nil {
		res = relaycore.NewResilience(r.store)
	}
	state, _ := res.GetState(relaycore.KeyFromAgent(picked))
	return string(state)
}

func lastOutputAge(path string, at time.Time) (time.Duration, bool) {
	if path == "" || at.IsZero() {
		return 0, false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return 0, false
	}
	age := at.Sub(info.ModTime())
	if age < 0 {
		age = 0
	}
	return age, true
}

// firstNonEmpty returns the first argument whose trimmed value is non-empty,
// or "" when none qualify. It resolves the model used in the runner telemetry
// tag: the executor's ResolvedModel (authoritative for bare-alias routes) wins
// over the route-resolved picked.Model, but the route-resolved model remains
// the fallback when the executor did not populate ResolvedModel.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func resolvedRunnerModel(result *harnessapi.TryResult, picked harnessapi.ResolvedAgent) string {
	if result == nil {
		return firstNonEmpty(picked.Model)
	}
	return firstNonEmpty(result.ResolvedModel, picked.Model)
}
