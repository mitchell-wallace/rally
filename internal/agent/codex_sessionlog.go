package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/reliability"
)

// codexSessionLogDir resolves the directory codex writes rollout session logs
// under: $CODEX_HOME/sessions (default ~/.codex/sessions). Returns ("", nil)
// when neither CODEX_HOME nor a home directory can be resolved, so callers can
// treat a missing session store as a non-error.
func codexSessionLogDir() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "sessions"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", nil
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// codexSessionMeta captures only the structural scalars from the first
// session_meta line of a rollout-*.jsonl. It deliberately omits the large
// base_instructions payload — that is PII/verbosity and must never reach the
// RawSignal. Fields are read straight off session_meta.
type codexSessionMeta struct {
	Type     string `json:"type"`
	Cwd      string `json:"cwd"`
	TS       string `json:"timestamp"`
	CliVer   string `json:"cli_version"`
	Provider string `json:"model_provider"`
	Git      struct {
		CommitHash string `json:"commit_hash"`
		Branch     string `json:"branch"`
	} `json:"git"`
}

// codexEventMsg captures an event_msg line. Only the subtype is used as the
// diagnostic; the payload (which for token_count / response_item / turn_context
// can be large or contain message bodies) is never copied into RawSignal.
type codexEventMsg struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

// codexSessionLogEvidence implements the codex session-log fallback (OpenSpec
// change improve-harness-consistency, design Decision 2 + 9, tasks.md §4).
//
// When codex exits non-zero with no parser-matchable in-band signal (the silent
// exit-1 burst observed on codex 0.11.2, which wrote nothing to either stream),
// codex's own rollout-*.jsonl session log is the authoritative diagnostic. This
// locates the most recent matching log and builds a FailureEvidence:
//
//   - Source = "codex_session_log"
//   - Message = the subtype of the last event_msg line (task_started /
//     task_complete / turn_aborted) — the terminal diagnostic
//   - RawSignal = a 256-rune-bounded string built from the session_meta scalars
//     plus the last event_msg line, never including base_instructions,
//     token_count, response_item, or turn_context payloads
//
// A matching log requires first-line session_meta.cwd == workspaceDir and the
// session_meta.timestamp within the try window [startedAt, endedAt].
//
// Return contract:
//   - matching log found  -> (*Evidence{"codex_session_log"}, nil)
//   - sessions dir scannable but no matching log -> (noSessionLogEvidence, nil)
//     so the executor can attach the codex_no_session_log harness_launch marker
//   - sessions dir missing/unreadable -> (nil, err) so the caller treats it as
//     a non-error and falls through to the existing safe_exec_error path
func codexSessionLogEvidence(workspaceDir string, startedAt, endedAt time.Time) (*reliability.FailureEvidence, error) {
	root, err := codexSessionLogDir()
	if err != nil {
		return nil, err
	}
	if root == "" {
		return nil, fmt.Errorf("codex session log dir not resolvable")
	}

	candidates, scanned, scanErr := codexMatchingRolloutFiles(root, workspaceDir, startedAt, endedAt)
	if scanErr != nil {
		// Missing or unreadable sessions dir is not an error: signal the
		// caller to fall through to the runner's safe_exec_error path.
		return nil, scanErr
	}
	if len(candidates) == 0 {
		// Sessions dir was scannable but no rollout matched. Codex never got
		// far enough to record a session for this workspace: surface the
		// codex_no_session_log harness_launch marker so ClassifyError Priority
		// 1 keeps retrying the launch within budget (FreshRestart/FailureInfra),
		// NOT Rotate/FailureAgent.
		if scanned {
			return codexNoSessionLogEvidence(), nil
		}
		return nil, fmt.Errorf("codex session log dir not scannable")
	}

	// candidates are newest-first by mtime; the most recent match wins.
	for _, f := range candidates {
		if ev := parseCodexRolloutEvidence(f); ev != nil {
			return ev, nil
		}
	}
	// A matched file existed but could not be parsed: treat as no usable log.
	return codexNoSessionLogEvidence(), nil
}

// codexMatchingRolloutFiles walks the sessions dir for rollout-*.jsonl files
// whose first-line session_meta.cwd == workspaceDir and whose
// session_meta.timestamp falls within [startedAt, endedAt]. It returns the
// matches newest-first by file mtime. scanned reports whether the sessions
// directory existed and was enumerable (false + nil err means missing dir, a
// non-error). An os.IsNotExist error from the root is reported as (nil, true,
// nil) — scannable-but-empty.
func codexMatchingRolloutFiles(root, workspaceDir string, startedAt, endedAt time.Time) (matches []string, scanned bool, err error) {
	st, statErr := os.Stat(root)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, false, statErr
		}
		return nil, false, statErr
	}
	if !st.IsDir() {
		return nil, false, fmt.Errorf("codex session log root %q is not a directory", root)
	}

	type entry struct {
		path  string
		mtime time.Time
	}
	var entries []entry
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		meta, ok := readCodexRolloutMeta(path)
		if !ok || meta.Cwd != workspaceDir {
			return nil
		}
		if !codexSessionTimestampInWindow(meta.TS, startedAt, endedAt) {
			return nil
		}
		entries = append(entries, entry{path: path, mtime: info.ModTime()})
		return nil
	})
	if walkErr != nil {
		return nil, false, walkErr
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].mtime.After(entries[j].mtime)
	})
	for _, e := range entries {
		matches = append(matches, e.path)
	}
	return matches, true, nil
}

// readCodexRolloutMeta reads only the first line of a rollout file and decodes
// its session_meta scalars. Returns ok=false when the file cannot be opened or
// the first line is not a parseable session_meta event.
func readCodexRolloutMeta(path string) (codexSessionMeta, bool) {
	f, err := os.Open(path)
	if err != nil {
		return codexSessionMeta{}, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	if !scanner.Scan() {
		return codexSessionMeta{}, false
	}
	var meta codexSessionMeta
	if err := json.Unmarshal([]byte(strings.TrimSpace(scanner.Text())), &meta); err != nil {
		return codexSessionMeta{}, false
	}
	if meta.Type != "session_meta" {
		return codexSessionMeta{}, false
	}
	return meta, true
}

// codexSessionTimestampInWindow reports whether an RFC3339-ish session_meta
// timestamp falls within [startedAt, endedAt]. A small slack is allowed on
// either side: codex writes session_meta.timestamp at session creation, which
// may slightly predate the executor's tryStart (process spawn latency) or, on a
// resumed/aborted session, slightly postdate tryEnd. Unparseable timestamps
// match (never reject a cwd match on a bad timestamp).
func codexSessionTimestampInWindow(ts string, startedAt, endedAt time.Time) bool {
	if strings.TrimSpace(ts) == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			return true
		}
	}
	const slack = 5 * time.Minute
	return !t.Before(startedAt.Add(-slack)) && !t.After(endedAt.Add(slack))
}

// parseCodexRolloutEvidence reads the first (session_meta) and last (event_msg)
// structural lines of a matched rollout file and builds the
// codex_session_log FailureEvidence. It scans once, tracking the most recent
// event_msg subtype, and explicitly ignores token_count / response_item /
// turn_context payloads and any base_instructions content.
func parseCodexRolloutEvidence(path string) *reliability.FailureEvidence {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var meta codexSessionMeta
	metaOK := false
	var lastEventSubtype string
	var lastEventLine string

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !metaOK {
			if err := json.Unmarshal([]byte(line), &meta); err == nil && meta.Type == "session_meta" {
				metaOK = true
			}
			continue
		}
		var msg codexEventMsg
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Type != "event_msg" {
			continue
		}
		// Skip verbose / sensitive subtypes entirely: only task_started,
		// task_complete, and turn_aborted carry a useful terminal diagnostic,
		// and token_count / response_item / turn_context are the documented
		// verbosity/PII hazards.
		switch msg.Subtype {
		case "task_started", "task_complete", "turn_aborted":
			lastEventSubtype = msg.Subtype
			lastEventLine = line
		}
	}
	if !metaOK {
		return nil
	}

	message := lastEventSubtype
	if message == "" {
		message = "no terminal event"
	}

	raw := buildCodexSessionRawSignal(meta, lastEventLine)
	return &reliability.FailureEvidence{
		Category:  reliability.CategoryUnidentifiedIssue,
		Harness:   "codex",
		Provider:  reliability.ProviderOpenAI,
		Message:   message,
		Source:    "codex_session_log",
		RawSignal: raw,
	}
}

// buildCodexSessionRawSignal assembles a bounded (256-rune) RawSignal from the
// session_meta scalars and the last event_msg line. Only the structural fields
// are used; base_instructions, token_count, response_item, and turn_context
// are structurally excluded (they are never passed in here).
func buildCodexSessionRawSignal(meta codexSessionMeta, lastEventLine string) string {
	var parts []string
	if meta.Cwd != "" {
		parts = append(parts, "cwd="+meta.Cwd)
	}
	if meta.Git.Branch != "" {
		parts = append(parts, "branch="+meta.Git.Branch)
	}
	if meta.Git.CommitHash != "" {
		parts = append(parts, "commit="+meta.Git.CommitHash)
	}
	if meta.Provider != "" {
		parts = append(parts, "provider="+meta.Provider)
	}
	if meta.CliVer != "" {
		parts = append(parts, "cli="+meta.CliVer)
	}
	if lastEventLine != "" {
		parts = append(parts, "last="+lastEventLine)
	}
	if len(parts) == 0 {
		return "codex session log"
	}
	return reliability.TruncateSignal(strings.Join(parts, " "), 256)
}

// codexNoSessionLogEvidence builds the executor-level evidence for the case
// where codex launched but wrote no session log for this workspace. It uses
// CategoryHarnessLaunch (NOT CategoryAuthOrProxy) so ClassifyError Priority 1
// maps it to StrategyFreshRestart + FailureInfra — retry within budget, then
// freeze pressure after 2+ infra failures — rather than rotating away on the
// first launch failure.
func codexNoSessionLogEvidence() *reliability.FailureEvidence {
	return &reliability.FailureEvidence{
		Category:  reliability.CategoryHarnessLaunch,
		Harness:   "codex",
		Provider:  reliability.ProviderOpenAI,
		Message:   "codex launched but wrote no session log",
		Source:    "codex_no_session_log",
		RawSignal: "codex exit-1: no session log",
	}
}
