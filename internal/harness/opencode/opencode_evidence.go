package opencode

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/reliability"
)

const (
	openCodeServerLogTailBytes = int64(1 << 20)
	openCodeServerLogWindowPad = 30 * time.Second
	// openCodeDiskLogSource is the Source value for evidence derived from the
	// opencode server log when no in-band evidence is available.
	openCodeDiskLogSource = "opencode_disk_log"

	// openCodeDiskLogMaxLines caps the number of noteworthy lines kept from
	// the server log, bounding the structural tail.
	openCodeDiskLogMaxLines = 16
)

var openCodeServerLogPath = defaultOpenCodeServerLogPath

func defaultOpenCodeServerLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "share", "opencode", "log", "opencode.log")
}

func attachOpenCodeFailureEvidence(tr *harnessapi.TryResult, out []byte, runErr error, opts harnessapi.RunOptions, model string, startedAt, endedAt time.Time) {
	if tr == nil || !openCodeNeedsFailureEvidence(tr, runErr) {
		return
	}
	if ev := reliability.ParseOpencodeError(string(out), model); ev != nil {
		tr.Evidence = ev
	}
	if ev := openCodeServerLogFailureEvidence(opts, tr, model, startedAt, endedAt); ev != nil {
		if tr.Evidence == nil || tr.Evidence.Category == "" {
			tr.Evidence = ev
		}
	}
}

func openCodeNeedsFailureEvidence(tr *harnessapi.TryResult, runErr error) bool {
	if runErr != nil || tr.Completed {
		return runErr != nil
	}
	return strings.HasPrefix(tr.Summary, "opencode error:") ||
		strings.HasPrefix(tr.Summary, "opencode process exited") ||
		strings.HasPrefix(tr.Summary, "opencode output could not") ||
		strings.HasPrefix(tr.Summary, "opencode completed without") ||
		strings.HasPrefix(tr.Summary, "opencode produced no")
}

type openCodeServerLogEntry struct {
	raw    string
	fields map[string]string
}

func openCodeServerLogFailureEvidence(opts harnessapi.RunOptions, tr *harnessapi.TryResult, model string, startedAt, endedAt time.Time) *reliability.FailureEvidence {
	data, err := readOpenCodeServerLogTail()
	if err != nil || len(data) == 0 {
		return nil
	}

	knownSessionID := opts.ResumeSessionID
	if knownSessionID == "" && tr != nil {
		knownSessionID = tr.SessionID
	}
	providerID := openCodeProviderID(model)

	sessionIDs := map[string]struct{}{}
	if knownSessionID != "" {
		sessionIDs[knownSessionID] = struct{}{}
	}

	var streamErrors []openCodeServerLogEntry
	var noteworthyLines []openCodeServerLogEntry
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		fields := parseOpenCodeLogFields(line)
		if !openCodeLogLineInWindow(parseOpenCodeLogTimestamp(fields), startedAt, endedAt) {
			continue
		}
		if sid := openCodeCreatedSessionID(fields, opts.WorkspaceDir); sid != "" {
			sessionIDs[sid] = struct{}{}
		}
		if openCodeIsStreamError(fields) {
			streamErrors = append(streamErrors, openCodeServerLogEntry{raw: line, fields: fields})
		}
		if openCodeIsNoteworthyLogLine(fields) && !openCodeIsNoisyLogLine(fields) {
			noteworthyLines = append(noteworthyLines, openCodeServerLogEntry{raw: line, fields: fields})
		}
	}

	// First try the existing usage-limit extraction path (unchanged).
	if len(sessionIDs) > 0 {
		matchSession := func(fields map[string]string) bool {
			_, ok := sessionIDs[openCodeLogEntrySessionID(fields)]
			return ok
		}
		if ev := openCodeEvidenceFromServerLog(streamErrors, model, matchSession); ev != nil {
			openCodeAnnotateServerLogEvidence(ev, openCodeFilterServerLogEntries(noteworthyLines, matchSession))
			return ev
		}
	}
	if providerID != "" {
		matchProvider := func(fields map[string]string) bool {
			if knownSessionID != "" && openCodeLogEntrySessionID(fields) != knownSessionID {
				return false
			}
			return fields["providerID"] == providerID
		}
		if ev := openCodeEvidenceFromServerLog(streamErrors, model, matchProvider); ev != nil {
			openCodeAnnotateServerLogEvidence(ev, openCodeFilterServerLogEntries(noteworthyLines, matchProvider))
			return ev
		}
	}

	// Fall back to the broader noteworthy-line evidence for budget-killed
	// tries where the stream-error path found nothing.
	if ev := openCodeDiskLogFailureEvidence(noteworthyLines, model, sessionIDs, providerID, knownSessionID); ev != nil {
		return ev
	}
	return nil
}

func openCodeAnnotateServerLogEvidence(ev *reliability.FailureEvidence, entries []openCodeServerLogEntry) {
	if ev == nil {
		return
	}
	ev.Source = openCodeDiskLogSource
	if rawSignal := openCodeServerLogRawSignal(entries); rawSignal != "" {
		ev.RawSignal = rawSignal
	}
}

func openCodeFilterServerLogEntries(entries []openCodeServerLogEntry, match func(map[string]string) bool) []openCodeServerLogEntry {
	if len(entries) == 0 {
		return nil
	}
	var matched []openCodeServerLogEntry
	for _, entry := range entries {
		if match(entry.fields) {
			matched = append(matched, entry)
		}
	}
	return matched
}

func openCodeServerLogRawSignal(entries []openCodeServerLogEntry) string {
	if len(entries) == 0 {
		return ""
	}
	if len(entries) > openCodeDiskLogMaxLines {
		entries = entries[len(entries)-openCodeDiskLogMaxLines:]
	}
	var rawParts []string
	for _, entry := range entries {
		rawParts = append(rawParts, entry.raw)
	}
	return openCodeTruncateTailSignal(strings.Join(rawParts, "\n"), 256)
}

func readOpenCodeServerLogTail() ([]byte, error) {
	path := openCodeServerLogPath()
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	offset := int64(0)
	if st.Size() > openCodeServerLogTailBytes {
		offset = st.Size() - openCodeServerLogTailBytes
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
			data = data[idx+1:]
		} else {
			data = nil
		}
	}
	return data, nil
}

func openCodeEvidenceFromServerLog(entries []openCodeServerLogEntry, model string, match func(map[string]string) bool) *reliability.FailureEvidence {
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if !match(entry.fields) {
			continue
		}
		ev := reliability.ParseOpencodeError(entry.raw, model)
		if ev == nil || ev.Category != reliability.CategoryUsageLimit {
			continue
		}
		if ev.Provider == "" {
			ev.Provider = entry.fields["providerID"]
		}
		return ev
	}
	return nil
}

func openCodeCreatedSessionID(fields map[string]string, workspaceDir string) string {
	if fields["message"] != "created" || !sameOpenCodeDirectory(fields["directory"], workspaceDir) {
		return ""
	}
	return openCodeSessionID(fields)
}

func openCodeSessionID(fields map[string]string) string {
	if sid := fields["session.id"]; sid != "" {
		return sid
	}
	return fields["id"]
}

func openCodeIsStreamError(fields map[string]string) bool {
	if !strings.EqualFold(fields["level"], "ERROR") || fields["message"] != "stream error" {
		return false
	}
	agent := fields["agent"]
	return agent == "build" || agent == "title"
}

// openCodeIsNoteworthyLogLine returns true for log lines worth capturing in
// the budget-killed disk-log fallback: WARN/ERROR level lines, and structural
// markers (message=created, message containing "loop session.id",
// message=stream) that indicate opencode lifecycle events.
func openCodeIsNoteworthyLogLine(fields map[string]string) bool {
	level := strings.ToUpper(fields["level"])
	if level == "WARN" || level == "ERROR" {
		return true
	}
	msg := fields["message"]
	switch {
	case msg == "created":
		return true
	case msg == "stream":
		return true
	case strings.HasPrefix(msg, "loop session.id=") || strings.Contains(msg, "loop session.id"):
		return true
	}
	return false
}

// openCodeIsNoisyLogLine returns true for high-volume log lines that must
// never appear in RawSignal: per-token streaming, per-tool-call, and
// per-permission log lines.
func openCodeIsNoisyLogLine(fields map[string]string) bool {
	msg := strings.ToLower(fields["message"])
	switch {
	case strings.Contains(msg, "token") && (strings.Contains(msg, "stream") || strings.Contains(msg, "chunk")):
		return true
	case msg == "tool_call" || msg == "tool call" || strings.HasPrefix(msg, "tool_call ") || strings.HasPrefix(msg, "tool call "):
		return true
	case msg == "permission" || strings.HasPrefix(msg, "permission "):
		return true
	}
	return false
}

func openCodeDiskLogFailureEvidence(entries []openCodeServerLogEntry, model string, sessionIDs map[string]struct{}, providerID, knownSessionID string) *reliability.FailureEvidence {
	matched := openCodeMatchedDiskLogEntries(entries, sessionIDs, providerID, knownSessionID)
	if len(matched) == 0 {
		return nil
	}
	return openCodeDiskLogEvidence(matched, model)
}

func openCodeMatchedDiskLogEntries(entries []openCodeServerLogEntry, sessionIDs map[string]struct{}, providerID, knownSessionID string) []openCodeServerLogEntry {
	if len(entries) == 0 {
		return nil
	}
	if len(sessionIDs) > 0 {
		var matched []openCodeServerLogEntry
		for _, entry := range entries {
			if _, ok := sessionIDs[openCodeLogEntrySessionID(entry.fields)]; ok {
				matched = append(matched, entry)
			}
		}
		if len(matched) > 0 {
			return matched
		}
	}
	if providerID == "" {
		return nil
	}
	var matched []openCodeServerLogEntry
	for _, entry := range entries {
		if knownSessionID != "" && openCodeLogEntrySessionID(entry.fields) != knownSessionID {
			continue
		}
		if entry.fields["providerID"] == providerID {
			matched = append(matched, entry)
		}
	}
	return matched
}

func openCodeLogEntrySessionID(fields map[string]string) string {
	if sid := openCodeSessionID(fields); sid != "" {
		return sid
	}
	return openCodeMessageSessionID(fields["message"])
}

func openCodeMessageSessionID(message string) string {
	const marker = "session.id="
	idx := strings.Index(message, marker)
	if idx < 0 {
		return ""
	}
	rest := message[idx+len(marker):]
	end := strings.IndexFunc(rest, func(r rune) bool {
		return r == ' ' || r == '\t' || r == ',' || r == '"' || r == '\''
	})
	if end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// openCodeDiskLogEvidence builds FailureEvidence from the collected noteworthy
// server-log lines for budget-killed tries with no in-band evidence. It
// applies the 16-line cap, determines the content-appropriate Category, and
// bounds RawSignal to 256 runes.
func openCodeDiskLogEvidence(entries []openCodeServerLogEntry, model string) *reliability.FailureEvidence {
	// Apply the line cap, keeping the tail (most recent lines).
	if len(entries) > openCodeDiskLogMaxLines {
		entries = entries[len(entries)-openCodeDiskLogMaxLines:]
	}

	// Separate WARN/ERROR lines from structural-only markers.
	var errorLines []openCodeServerLogEntry
	for _, e := range entries {
		level := strings.ToUpper(e.fields["level"])
		if level == "WARN" || level == "ERROR" {
			errorLines = append(errorLines, e)
		}
	}

	// Build bounded RawSignal from all noteworthy lines.
	var rawParts []string
	for _, e := range entries {
		rawParts = append(rawParts, e.raw)
	}
	rawSignal := openCodeTruncateTailSignal(strings.Join(rawParts, "\n"), 256)

	// Try to recognise a specific error shape from the error lines.
	if len(errorLines) > 0 {
		lastError := errorLines[len(errorLines)-1]
		for i := len(errorLines) - 1; i >= 0; i-- {
			if ev := openCodeRecognizedDiskLogEvidence(errorLines[i], model); ev != nil {
				// Recognised specific category from an error line.
				ev.Source = openCodeDiskLogSource
				ev.RawSignal = rawSignal
				if ev.Provider == "" {
					ev.Provider = errorLines[i].fields["providerID"]
				}
				return ev
			}
		}

		// WARN/ERROR lines present but no recognisable error shape.
		// Extract message from last error line for the Message field.
		msg := lastError.fields["message"]
		if flatErr := openCodeFlatErrorValue(lastError.raw); flatErr != "" {
			msg = flatErr
		}
		if msg == "" {
			msg = lastError.raw
		}
		return &reliability.FailureEvidence{
			Category:  reliability.CategoryAgentError,
			Source:    openCodeDiskLogSource,
			Provider:  openCodeProviderID(model),
			Message:   reliability.TruncateSignal(msg, 200),
			RawSignal: rawSignal,
		}
	}

	// Only structural markers, no error lines.
	return &reliability.FailureEvidence{
		Category:  reliability.CategoryUnidentifiedIssue,
		Source:    openCodeDiskLogSource,
		Provider:  openCodeProviderID(model),
		Message:   "try budget exhausted; no parseable output",
		RawSignal: rawSignal,
	}
}

func openCodeRecognizedDiskLogEvidence(entry openCodeServerLogEntry, model string) *reliability.FailureEvidence {
	candidates := []string{entry.raw}
	if msg := strings.TrimSpace(entry.fields["message"]); msg != "" {
		candidates = append(candidates, openCodeSyntheticFlatError(msg))
		if strings.Contains(strings.ToLower(msg), "api key invalid") {
			candidates = append(candidates, openCodeSyntheticFlatError("invalid api key"))
		}
	}
	for _, candidate := range candidates {
		ev := reliability.ParseOpencodeError(candidate, model)
		if ev != nil && openCodeIsRecognizedDiskLogCategory(ev.Category) {
			return ev
		}
	}
	return nil
}

func openCodeIsRecognizedDiskLogCategory(category reliability.FailureCategory) bool {
	switch category {
	case "", reliability.CategoryAgentError, reliability.CategoryUnidentifiedIssue:
		return false
	default:
		return true
	}
}

func openCodeSyntheticFlatError(message string) string {
	message = strings.ReplaceAll(message, `"`, "'")
	return `error.error="` + message + `"`
}

// openCodeFlatErrorValue extracts the value from the error.error="..." logfmt
// field in a server-log line, if present.
func openCodeFlatErrorValue(line string) string {
	const prefix = `error.error="`
	idx := strings.Index(line, prefix)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(prefix):]
	if end := strings.Index(rest, `"`); end >= 0 {
		return rest[:end]
	}
	return rest
}

func openCodeTruncateTailSignal(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return "…" + string(runes[len(runes)-maxRunes:])
}

func openCodeLogLineInWindow(ts time.Time, startedAt, endedAt time.Time) bool {
	if ts.IsZero() || startedAt.IsZero() {
		return false
	}
	if endedAt.IsZero() || endedAt.Before(startedAt) {
		endedAt = startedAt
	}
	return !ts.Before(startedAt.Add(-openCodeServerLogWindowPad)) && !ts.After(endedAt.Add(openCodeServerLogWindowPad))
}

func openCodeProviderID(model string) string {
	if model == "" {
		return ""
	}
	provider, _, ok := strings.Cut(model, "/")
	if !ok {
		return model
	}
	return provider
}

func parseOpenCodeLogTimestamp(fields map[string]string) time.Time {
	if fields["timestamp"] == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, fields["timestamp"])
	if err != nil {
		return time.Time{}
	}
	return ts
}

func sameOpenCodeDirectory(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ar, aerr := filepath.EvalSymlinks(a)
	br, berr := filepath.EvalSymlinks(b)
	return aerr == nil && berr == nil && filepath.Clean(ar) == filepath.Clean(br)
}

func parseOpenCodeLogFields(line string) map[string]string {
	fields := make(map[string]string)
	for i := 0; i < len(line); {
		for i < len(line) && line[i] == ' ' {
			i++
		}
		keyStart := i
		for i < len(line) && line[i] != '=' && line[i] != ' ' {
			i++
		}
		if keyStart == i || i >= len(line) || line[i] != '=' {
			for i < len(line) && line[i] != ' ' {
				i++
			}
			continue
		}
		key := line[keyStart:i]
		i++
		if i < len(line) && line[i] == '"' {
			value, next := parseOpenCodeQuotedLogValue(line, i+1)
			fields[key] = value
			i = next
			continue
		}
		valueStart := i
		for i < len(line) && line[i] != ' ' {
			i++
		}
		fields[key] = line[valueStart:i]
	}
	return fields
}

func parseOpenCodeQuotedLogValue(line string, i int) (string, int) {
	var b strings.Builder
	for i < len(line) {
		switch line[i] {
		case '\\':
			if i+1 < len(line) {
				i++
				b.WriteByte(line[i])
			}
		case '"':
			return b.String(), i + 1
		default:
			b.WriteByte(line[i])
		}
		i++
	}
	return b.String(), i
}
