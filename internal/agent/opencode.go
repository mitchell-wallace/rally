package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/reliability"
)

type OpenCodeExecutor struct {
	Model string
}

const (
	// These are parser-local safety bounds for failure indicators. Persisted
	// final snippets have a separate cap at the storage boundary.
	openCodeFailureSummaryLimit = 512
	openCodeErrorRefLimit       = 96
	openCodeServerLogTailBytes  = int64(1 << 20)
	openCodeServerLogWindowPad  = 30 * time.Second
)

var openCodeServerLogPath = defaultOpenCodeServerLogPath

type opencodeJSONEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Part      struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"part"`
	Error *opencodeJSONError `json:"error,omitempty"`
}

type opencodeJSONError struct {
	Name string `json:"name"`
	Data struct {
		Message string `json:"message"`
		Ref     string `json:"ref"`
	} `json:"data"`
}

func (o *OpenCodeExecutor) ResumeSupported() bool        { return true }
func (o *OpenCodeExecutor) RotateSupported() bool        { return true }
func (o *OpenCodeExecutor) LivenessProbeSupported() bool { return false }
func (o *OpenCodeExecutor) RotateModel(newModel string) error {
	o.Model = newModel
	return nil
}
func (o *OpenCodeExecutor) ProbeLiveness(_ context.Context) (bool, error) {
	return false, fmt.Errorf("liveness probe not supported by opencode adapter")
}

func (o *OpenCodeExecutor) Execute(ctx context.Context, opts RunOptions) (*TryResult, error) {
	prompt := BuildPrompt(opts)

	model := o.Model
	if opts.Model != "" {
		model = opts.Model
	}

	args := []string{"run", prompt, "--format", "json"}
	if model != "" {
		args = append(args, "--model", model)
	}
	if opts.ReasoningEffort != "" {
		var warning string
		args, warning = applyReasoningEffort(args, "opencode", opts.ReasoningEffort)
		defer emitReasoningWarning(opts.LogPath, warning)
	}
	// opencode uses a client/server model: `opencode run` connects to a server
	// process that resolves relative file paths against ITS cwd, not the client's
	// cmd.Dir. Setting cmd.Dir alone leaks files into the launching process's
	// working directory. Pass --dir so the server operates in the workspace.
	if opts.WorkspaceDir != "" {
		args = append(args, "--dir", opts.WorkspaceDir)
	}
	if opts.ResumeSessionID != "" {
		args = append(args, "--session", opts.ResumeSessionID)
	}

	cmd := exec.CommandContext(ctx, "opencode", args...)
	if opts.WorkspaceDir != "" {
		cmd.Dir = opts.WorkspaceDir
	}
	cmd.Env = append(os.Environ(), `OPENCODE_PERMISSION={"*":"allow"}`)
	SetProcessGroup(cmd)
	startedAt := time.Now()
	out, runErr := runLoggedCommand(cmd, opts.LogPath, true, opts.OnStart)
	endedAt := time.Now()

	tr, err := parseOpenCodeOutput(out, runErr == nil)
	if err != nil {
		return nil, err
	}
	tr.ResolvedModel = model
	attachOpenCodeFailureEvidence(tr, out, runErr, opts, model, startedAt, endedAt)
	if runErr != nil {
		return tr, fmt.Errorf("opencode exec failed: %w", runErr)
	}
	return tr, nil
}

func defaultOpenCodeServerLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "share", "opencode", "log", "opencode.log")
}

func attachOpenCodeFailureEvidence(tr *TryResult, out []byte, runErr error, opts RunOptions, model string, startedAt, endedAt time.Time) {
	if tr == nil || !openCodeNeedsFailureEvidence(tr, runErr) {
		return
	}
	if ev := reliability.ParseOpencodeError(string(out), model); ev != nil {
		tr.Evidence = ev
	}
	if ev := openCodeServerLogFailureEvidence(opts, tr, model, startedAt, endedAt); ev != nil {
		// The server log is the authoritative carrier for opencode's internally
		// retried subscription-provider usage limits. Let it replace generic
		// UnknownError/agent_error evidence and enrich incomplete silent stalls.
		if tr.Evidence == nil || tr.Evidence.Category == "" || tr.Evidence.Category == reliability.CategoryAgentError || ev.Category == reliability.CategoryUsageLimit {
			tr.Evidence = ev
		}
	}
}

func openCodeNeedsFailureEvidence(tr *TryResult, runErr error) bool {
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

func openCodeServerLogFailureEvidence(opts RunOptions, tr *TryResult, model string, startedAt, endedAt time.Time) *reliability.FailureEvidence {
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
	}

	if len(sessionIDs) > 0 {
		if ev := openCodeEvidenceFromServerLog(streamErrors, model, func(fields map[string]string) bool {
			_, ok := sessionIDs[openCodeSessionID(fields)]
			return ok
		}); ev != nil {
			return ev
		}
	}

	if providerID == "" {
		return nil
	}
	return openCodeEvidenceFromServerLog(streamErrors, model, func(fields map[string]string) bool {
		if knownSessionID != "" && openCodeSessionID(fields) != knownSessionID {
			return false
		}
		return fields["providerID"] == providerID
	})
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

func parseOpenCodeOutput(out []byte, processSucceeded bool) (*TryResult, error) {
	var textParts []string
	toolCalls := 0
	sawJSONEvent := false
	sawStepFinish := false
	sawErrorEvent := false
	var eventError *opencodeJSONError
	var sessionID string

	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev opencodeJSONEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		sawJSONEvent = true
		if sessionID == "" && ev.SessionID != "" {
			sessionID = ev.SessionID
		}
		if ev.Type == "text" && ev.Part.Text != "" {
			textParts = append(textParts, ev.Part.Text)
		}
		if ev.Type == "tool_use" || ev.Part.Type == "tool" {
			toolCalls++
		}
		if ev.Type == "step_finish" {
			sawStepFinish = true
		}
		if ev.Type == "error" {
			sawErrorEvent = true
			if eventError == nil && ev.Error != nil {
				eventError = ev.Error
			}
		}
	}

	scanFailed := scanner.Err() != nil
	combined := strings.TrimSpace(strings.Join(textParts, ""))
	cleanCompletion := processSucceeded && !scanFailed && !sawErrorEvent && (combined != "" || sawStepFinish)

	if sawErrorEvent {
		return &TryResult{
			Completed: false,
			Summary:   formatOpenCodeError(eventError),
			ToolCalls: toolCalls,
			SessionID: sessionID,
		}, nil
	}
	if combined == "" {
		return &TryResult{
			Completed: cleanCompletion,
			Summary:   openCodeNoTextSummary(out, sawJSONEvent, sawStepFinish, scanFailed, processSucceeded),
			ToolCalls: toolCalls,
			SessionID: sessionID,
		}, nil
	}

	var tr TryResult
	if err := json.Unmarshal([]byte(combined), &tr); err != nil {
		return &TryResult{
			Completed: cleanCompletion,
			Summary:   combined,
			ToolCalls: toolCalls,
			SessionID: sessionID,
		}, nil
	}
	tr.Completed = tr.Completed && cleanCompletion
	tr.ToolCalls = toolCalls
	tr.SessionID = sessionID
	return &tr, nil
}

func openCodeNoTextSummary(out []byte, sawJSONEvent, sawStepFinish, scanFailed, processSucceeded bool) string {
	switch {
	case !processSucceeded:
		return "opencode process exited unsuccessfully without a parseable result"
	case scanFailed:
		return "opencode output could not be fully parsed"
	case sawStepFinish:
		return "opencode completed without assistant text"
	case strings.TrimSpace(string(out)) == "":
		return "opencode produced no output"
	case !sawJSONEvent:
		return "opencode produced no parseable JSON events"
	default:
		return "opencode produced no parseable result"
	}
}

func formatOpenCodeError(eventError *opencodeJSONError) string {
	if eventError == nil {
		return "opencode error: unknown error"
	}

	detail := compactOpenCodeIndicator(eventError.Data.Message)
	if detail == "" {
		detail = compactOpenCodeIndicator(eventError.Name)
	}
	if detail == "" {
		detail = "unknown error"
	}

	const prefix = "opencode error: "
	ref := truncateOpenCodeIndicator(compactOpenCodeIndicator(eventError.Data.Ref), openCodeErrorRefLimit)
	if ref == "" {
		return truncateOpenCodeIndicator(prefix+detail, openCodeFailureSummaryLimit)
	}

	suffix := " (" + ref + ")"
	detailLimit := openCodeFailureSummaryLimit - len([]rune(prefix)) - len([]rune(suffix))
	detail = truncateOpenCodeIndicator(detail, detailLimit)
	return prefix + detail + suffix
}

func compactOpenCodeIndicator(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateOpenCodeIndicator(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 0 {
		return ""
	}

	marker := []rune("...")
	if maxRunes <= len(marker) {
		return string(marker[:maxRunes])
	}
	return string(runes[:maxRunes-len(marker)]) + string(marker)
}
