package reliability

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Tasks.md §3.10: every ClassifyError priority must populate a FailureEvidence
// whose Source / Category / Message / RawSignal match the path that produced
// it, the RawSignal must be non-empty for every source, and the 256-rune bound
// must hold. These are the same evidence objects the runner plumbs onto the
// RallyFailure telemetry (decision.Evidence -> resetEvidence ->
// applyEvidenceToFailureState), so this test doubles as the review of the
// classification plumbing.

const evidenceRuneBound = 256

func assertRuneBounded(t *testing.T, label, signal string) {
	t.Helper()
	if n := utf8.RuneCountInString(signal); n > evidenceRuneBound+1 {
		// +1 tolerance for the single "…" ellipsis rune truncateSignal appends.
		t.Fatalf("%s raw_signal is %d runes, exceeds the %d-rune bound: %q", label, n, evidenceRuneBound, signal)
	}
}

// Priority 1: typed executor evidence. Source defaults to "executor_evidence"
// when the executor left it unset, and the executor-supplied message and raw
// signal are preserved verbatim.
func TestEvidencePriority1_ExecutorEvidence(t *testing.T) {
	executor := &FailureEvidence{
		Category:  CategoryUsageLimit,
		Message:   "monthly usage limit reached",
		RawSignal: "429 You have hit your usage limit",
	}
	// Provide log lines that would otherwise match a text pattern to prove
	// Priority 1 wins outright.
	logLines := []string{"fork/exec /bin/agent: connection refused"}
	decision := ClassifyError(logLines, "", &ClassifyContext{HasFileChanges: true}, executor)

	ev := decision.Evidence
	if ev == nil {
		t.Fatal("Priority 1 produced nil Evidence")
	}
	if ev.Source != "executor_evidence" {
		t.Errorf("source = %q, want executor_evidence", ev.Source)
	}
	if ev.Category != CategoryUsageLimit {
		t.Errorf("category = %q, want usage_limit", ev.Category)
	}
	if ev.Message != "monthly usage limit reached" {
		t.Errorf("message = %q, want executor-supplied message preserved", ev.Message)
	}
	if ev.RawSignal == "" {
		t.Error("raw_signal must be non-empty")
	}
	// The classifier must not mutate the caller's struct in place.
	if executor.Source != "" {
		t.Errorf("executor evidence Source was mutated to %q; must stay empty", executor.Source)
	}
}

// Priority 1, evidence-supplied source is preserved (not overwritten by the
// executor_evidence default).
func TestEvidencePriority1_PreservesExplicitSource(t *testing.T) {
	executor := &FailureEvidence{
		Category:  CategoryHarnessLaunch,
		Source:    "codex_no_session_log",
		Message:   "codex launched but wrote no session log",
		RawSignal: "exit status 1",
	}
	decision := ClassifyError(nil, "codex", nil, executor)
	if got := decision.Evidence.Source; got != "codex_no_session_log" {
		t.Errorf("source = %q, want explicit codex_no_session_log preserved", got)
	}
}

// Priority 3: dirty-tree incomplete finalization. Source "dirty_tree",
// category incomplete_finalization, fixed message, raw signal carries the
// changed paths and respects the 256-rune bound.
func TestEvidencePriority3_DirtyTree(t *testing.T) {
	ctx := &ClassifyContext{
		HasFileChanges: true,
		Finalized:      false,
		ChangedPaths:   []string{"internal/relay/runner.go", "internal/reliability/patterns.go"},
	}
	decision := ClassifyError(nil, "claude", ctx, nil)

	ev := decision.Evidence
	if ev == nil {
		t.Fatal("Priority 3 produced nil Evidence")
	}
	if ev.Source != "dirty_tree" {
		t.Errorf("source = %q, want dirty_tree", ev.Source)
	}
	if ev.Category != CategoryIncompleteFinalization {
		t.Errorf("category = %q, want incomplete_finalization", ev.Category)
	}
	if decision.Category != CategoryIncompleteFinalization {
		t.Errorf("decision category = %q, want incomplete_finalization", decision.Category)
	}
	if ev.Message != "agent exited without finalizing" {
		t.Errorf("message = %q, want agent exited without finalizing", ev.Message)
	}
	if ev.RawSignal == "" {
		t.Error("raw_signal must be non-empty")
	}
	if !strings.Contains(ev.RawSignal, "internal/relay/runner.go") {
		t.Errorf("raw_signal = %q, want it to carry the changed paths", ev.RawSignal)
	}
}

func TestEvidencePriority3_RawSignalBounded(t *testing.T) {
	// A pathologically large changed-path set must be truncated to the bound.
	paths := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		paths = append(paths, "internal/very/long/package/path/to/some/changed/file.go")
	}
	ctx := &ClassifyContext{HasFileChanges: true, ChangedPaths: paths}
	decision := ClassifyError(nil, "claude", ctx, nil)
	assertRuneBounded(t, "dirty_tree", decision.Evidence.RawSignal)
}

// Priority 4: harness-scoped text pattern. Source "text_pattern", the pattern's
// category, message equal to the pattern name, and the matched line present in
// raw_signal.
func TestEvidencePriority4_TextPattern(t *testing.T) {
	matchedLine := "Error: connection refused while contacting api.anthropic.com"
	logLines := []string{"starting agent", matchedLine, "agent exited"}
	decision := ClassifyError(logLines, "", nil, nil)

	ev := decision.Evidence
	if ev == nil {
		t.Fatal("Priority 4 produced nil Evidence")
	}
	if ev.Source != "text_pattern" {
		t.Errorf("source = %q, want text_pattern", ev.Source)
	}
	// "connection refused" is the transient_infra pattern.
	if ev.Category != CategoryTransientInfra {
		t.Errorf("category = %q, want transient_infra", ev.Category)
	}
	if ev.Category != decision.Category {
		t.Errorf("evidence category %q != decision category %q", ev.Category, decision.Category)
	}
	if ev.Message != "connection refused" {
		t.Errorf("message = %q, want the pattern name", ev.Message)
	}
	if !strings.Contains(ev.RawSignal, "connection refused") {
		t.Errorf("raw_signal = %q, want the matched line present", ev.RawSignal)
	}
	if ev.RawSignal == "" {
		t.Error("raw_signal must be non-empty")
	}
}

// Priority 4 with a harness-scoped pattern: the surviving antigravity
// gemini-cli exit-1 pattern carries the pattern's agent_error category.
func TestEvidencePriority4_HarnessScopedCategory(t *testing.T) {
	logLines := []string{"agy: gemini-cli failed with exit status 1"}
	decision := ClassifyError(logLines, "antigravity", nil, nil)

	ev := decision.Evidence
	if ev == nil {
		t.Fatal("expected text-pattern Evidence for antigravity exit 1")
	}
	if ev.Source != "text_pattern" {
		t.Errorf("source = %q, want text_pattern", ev.Source)
	}
	if ev.Category != CategoryAgentError {
		t.Errorf("category = %q, want agent_error (pattern category)", ev.Category)
	}
	if ev.Message != "gemini-cli exit 1" {
		t.Errorf("message = %q, want the pattern name", ev.Message)
	}
	if !strings.Contains(ev.RawSignal, "exit status 1") {
		t.Errorf("raw_signal = %q, want the matched line present", ev.RawSignal)
	}
}

func TestEvidencePriority4_RawSignalBounded(t *testing.T) {
	longLine := "connection refused " + strings.Repeat("x", 1000)
	decision := ClassifyError([]string{longLine}, "", nil, nil)
	assertRuneBounded(t, "text_pattern", decision.Evidence.RawSignal)
	if decision.Evidence.Source != "text_pattern" {
		t.Fatalf("source = %q, want text_pattern", decision.Evidence.Source)
	}
}

// Priority 5: unmatched default. Source "unmatched", category
// unidentified_issue, fixed message, raw signal carries a bounded log tail.
func TestEvidencePriority5_Unmatched(t *testing.T) {
	logLines := []string{"some unremarkable agent chatter", "nothing classifiable here"}
	decision := ClassifyError(logLines, "claude", nil, nil)

	ev := decision.Evidence
	if ev == nil {
		t.Fatal("Priority 5 produced nil Evidence")
	}
	if ev.Source != "unmatched" {
		t.Errorf("source = %q, want unmatched", ev.Source)
	}
	if ev.Category != CategoryUnidentifiedIssue {
		t.Errorf("category = %q, want unidentified_issue", ev.Category)
	}
	if decision.Category != CategoryUnidentifiedIssue {
		t.Errorf("decision category = %q, want unidentified_issue", decision.Category)
	}
	if ev.Message != "no recognised provider signal" {
		t.Errorf("message = %q, want no recognised provider signal", ev.Message)
	}
	if ev.RawSignal == "" {
		t.Error("raw_signal must be non-empty")
	}
	if !strings.Contains(ev.RawSignal, "nothing classifiable here") {
		t.Errorf("raw_signal = %q, want the log tail present", ev.RawSignal)
	}
}

// Priority 5 with empty log output still yields a non-empty raw signal marker.
func TestEvidencePriority5_EmptyLogMarker(t *testing.T) {
	decision := ClassifyError(nil, "", nil, nil)
	if got := decision.Evidence.RawSignal; got != "no log output" {
		t.Errorf("raw_signal = %q, want the empty-log marker", got)
	}
}

func TestEvidencePriority5_RawSignalBounded(t *testing.T) {
	logLines := []string{strings.Repeat("y", 5000)}
	decision := ClassifyError(logLines, "", nil, nil)
	assertRuneBounded(t, "unmatched", decision.Evidence.RawSignal)
}

// Cross-priority guarantee: every classification path emits non-nil Evidence
// with a non-empty Source and a non-empty RawSignal.
func TestEvidence_EverySourceNonEmpty(t *testing.T) {
	cases := []struct {
		name       string
		logLines   []string
		harness    string
		ctx        *ClassifyContext
		evidence   *FailureEvidence
		wantSource string
	}{
		{
			name:       "priority 1 executor",
			evidence:   &FailureEvidence{Category: CategoryAuthOrProxy, RawSignal: "401 unauthorized"},
			wantSource: "executor_evidence",
		},
		{
			name:       "priority 3 dirty tree",
			ctx:        &ClassifyContext{HasFileChanges: true, ChangedPaths: []string{"a.go"}},
			wantSource: "dirty_tree",
		},
		{
			name:       "priority 4 text pattern",
			logLines:   []string{"503 service unavailable"},
			wantSource: "text_pattern",
		},
		{
			name:       "priority 5 unmatched",
			logLines:   []string{"plain old failure"},
			wantSource: "unmatched",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			decision := ClassifyError(c.logLines, c.harness, c.ctx, c.evidence)
			ev := decision.Evidence
			if ev == nil {
				t.Fatal("Evidence is nil")
			}
			if ev.Source != c.wantSource {
				t.Errorf("source = %q, want %q", ev.Source, c.wantSource)
			}
			if ev.RawSignal == "" {
				t.Errorf("raw_signal must be non-empty for source %q", c.wantSource)
			}
			assertRuneBounded(t, c.wantSource, ev.RawSignal)
		})
	}
}
