package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/reliability"
)

type ClaudeExecutor struct {
	Model string
}

const (
	claudeNoResultSummary        = "claude produced no structured result"
	claudeMalformedResultSummary = "claude produced an unparseable structured result"
	claudeMissingSummary         = "claude structured result contained no summary"
	claudeSessionLogSource       = "claude_session_log"
	claudeSessionLogTailLines    = 50
)

type claudeJSONEvent struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	Result    json.RawMessage `json:"result"`
	Message   *claudeMessage  `json:"message,omitempty"`
}

type claudeMessage struct {
	Content []claudeContentBlock `json:"content"`
}

type claudeContentBlock struct {
	Type string `json:"type"`
}

func (c *ClaudeExecutor) Execute(ctx context.Context, opts RunOptions) (*TryResult, error) {
	prompt := BuildPrompt(opts)

	model := c.Model
	if opts.Model != "" {
		model = opts.Model
	}

	args := []string{"-p", prompt, "--dangerously-skip-permissions", "--output-format", "stream-json", "--verbose"}
	if model != "" {
		args = append(args, "--model", model)
	}
	if opts.ReasoningEffort != "" {
		var warning string
		args, warning = applyReasoningEffort(args, "claude", opts.ReasoningEffort)
		defer emitReasoningWarning(opts.LogPath, warning)
	}
	if opts.ResumeSessionID != "" {
		args = append(args, "--resume", opts.ResumeSessionID)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	if opts.WorkspaceDir != "" {
		cmd.Dir = opts.WorkspaceDir
	}
	SetProcessGroup(cmd)
	out, err := runLoggedCommand(cmd, opts.LogPath, true, opts.OnStart)
	if err != nil {
		execErr := fmt.Errorf("claude exec failed: %w\noutput: %s", err, string(out))
		if ev := reliability.ParseClaudeError(string(out)); ev != nil {
			return &TryResult{Evidence: ev, ResolvedModel: model}, execErr
		}
		if ev := claudeSessionLogFailureEvidence(opts.WorkspaceDir, opts.ResumeSessionID); ev != nil {
			return &TryResult{Evidence: ev, ResolvedModel: model}, execErr
		}
		return nil, execErr
	}

	resultRaw, sessionID, toolCalls := scanClaudeOutput(out)
	tr, parseErr := parseClaudeResult(out, resultRaw)
	if parseErr != nil {
		return nil, parseErr
	}
	tr.SessionID = sessionID
	tr.ToolCalls = toolCalls
	tr.ResolvedModel = model
	return tr, nil
}

func (c *ClaudeExecutor) ResumeSupported() bool        { return true }
func (c *ClaudeExecutor) RotateSupported() bool        { return false }
func (c *ClaudeExecutor) LivenessProbeSupported() bool { return false }
func (c *ClaudeExecutor) RotateModel(string) error {
	return fmt.Errorf("rotate not supported by claude adapter")
}
func (c *ClaudeExecutor) ProbeLiveness(_ context.Context) (bool, error) {
	return false, fmt.Errorf("liveness probe not supported by claude adapter")
}

func scanClaudeOutput(out []byte) (resultRaw []byte, sessionID string, toolCalls int) {
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev claudeJSONEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.SessionID != "" {
			sessionID = ev.SessionID
		}
		if ev.Type == "assistant" && ev.Message != nil {
			for _, block := range ev.Message.Content {
				if block.Type == "tool_use" {
					toolCalls++
				}
			}
		}
		if ev.Type == "result" {
			resultRaw = ev.Result
			break
		}
	}
	return resultRaw, sessionID, toolCalls
}

func parseClaudeResult(_ []byte, resultRaw []byte) (*TryResult, error) {
	if strings.TrimSpace(string(resultRaw)) == "" {
		return &TryResult{Completed: false, Summary: claudeNoResultSummary}, nil
	}

	var tr TryResult
	if err := json.Unmarshal(resultRaw, &tr); err == nil {
		if strings.TrimSpace(tr.Summary) == "" {
			tr.Completed = false
			tr.Summary = claudeMissingSummary
		}
		return &tr, nil
	}

	// Claude may return the final assistant message as a JSON string instead
	// of the requested TryResult object. That string is final text, not the
	// stream-json transcript, so retain a bounded version as a useful fallback.
	var finalText string
	if err := json.Unmarshal(resultRaw, &finalText); err == nil {
		var nested TryResult
		if err := json.Unmarshal([]byte(finalText), &nested); err == nil {
			if strings.TrimSpace(nested.Summary) == "" {
				nested.Completed = false
				nested.Summary = claudeMissingSummary
			}
			return &nested, nil
		}
		if summary := boundedExecutorFinalText(finalText); summary != "" {
			return &TryResult{Completed: true, Summary: summary}, nil
		}
	}

	return &TryResult{Completed: false, Summary: claudeMalformedResultSummary}, nil
}

func claudeSessionLogFailureEvidence(workspaceDir, sessionID string) *reliability.FailureEvidence {
	path := claudeSessionLogPath(workspaceDir, sessionID)
	if path == "" {
		return nil
	}
	lines, err := readClaudeSessionLogTail(path, claudeSessionLogTailLines)
	if err != nil {
		return nil
	}
	return claudeSessionLogEvidenceFromLines(lines)
}

func claudeSessionLogPath(workspaceDir, sessionID string) string {
	if strings.TrimSpace(workspaceDir) == "" || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	project := claudeProjectPathName(workspaceDir)
	if project == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "projects", project, sessionID+".jsonl")
}

func claudeProjectPathName(workspaceDir string) string {
	clean := filepath.Clean(workspaceDir)
	if clean == "." {
		clean = workspaceDir
	}
	clean = filepath.ToSlash(clean)
	return strings.ReplaceAll(clean, "/", "-")
}

func readClaudeSessionLogTail(path string, maxLines int) ([]string, error) {
	if maxLines <= 0 {
		maxLines = claudeSessionLogTailLines
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var lines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if len(lines) == maxLines {
			copy(lines, lines[1:])
			lines[len(lines)-1] = line
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

type claudeSessionLogEvent struct {
	Type       string          `json:"type"`
	Subtype    string          `json:"subtype,omitempty"`
	StopReason string          `json:"stop_reason,omitempty"`
	Message    json.RawMessage `json:"message,omitempty"`
	Error      json.RawMessage `json:"error,omitempty"`
	Content    json.RawMessage `json:"content,omitempty"`
}

type claudeSessionLogMessage struct {
	StopReason string          `json:"stop_reason"`
	Content    json.RawMessage `json:"content"`
}

type claudeSessionContentBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
	Name    string          `json:"name,omitempty"`
}

type claudeSessionCandidate struct {
	subtype           string
	stopReason        string
	text              string
	message           string
	endTurnWithoutErr bool
}

func claudeSessionLogEvidenceFromLines(lines []string) *reliability.FailureEvidence {
	var rawTail []string
	var lastCandidate *claudeSessionCandidate
	for _, line := range lines {
		summary, candidate, keep := summarizeClaudeSessionLogEvent(line)
		if !keep {
			continue
		}
		if summary != "" {
			rawTail = append(rawTail, summary)
		}
		if candidate != nil {
			lastCandidate = candidate
		}
	}
	if lastCandidate == nil || lastCandidate.endTurnWithoutErr {
		return nil
	}
	rawSignal := reliability.TruncateSignal(strings.Join(rawTail, "\n"), 256)
	if rawSignal == "" {
		rawSignal = reliability.TruncateSignal(lastCandidate.message, 256)
	}
	return claudeSessionCandidateEvidence(lastCandidate, rawSignal)
}

func summarizeClaudeSessionLogEvent(line string) (summary string, candidate *claudeSessionCandidate, keep bool) {
	var ev claudeSessionLogEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Type == "" {
		return "", nil, false
	}
	switch ev.Type {
	case "user", "thinking":
		return "", nil, false
	}

	stopReason, snippets := claudeSessionMessageParts(ev.Message)
	if ev.StopReason != "" {
		stopReason = ev.StopReason
	}
	snippets = append(snippets, claudeSessionContentSnippets(ev.Content)...)
	snippets = append(snippets, claudeSessionRawStringSnippets(ev.Error, 4)...)
	text := strings.Join(snippets, " | ")

	switch ev.Type {
	case "assistant":
		if stopReason != "error" && stopReason != "end_turn" {
			return "", nil, false
		}
		summary := "type=assistant stop_reason=" + stopReason
		if text != "" {
			summary += " text=" + claudeSessionFirstLine(text)
		}
		candidate := &claudeSessionCandidate{
			stopReason:        stopReason,
			text:              text,
			message:           "assistant stop_reason=" + stopReason,
			endTurnWithoutErr: stopReason == "end_turn" && !claudeSessionHasErrorSignal(text),
		}
		return summary, candidate, true
	case "system":
		if ev.Subtype != "init" {
			return "", nil, false
		}
		summary := "type=system subtype=init"
		if text != "" {
			summary += " text=" + claudeSessionFirstLine(text)
		}
		return summary, &claudeSessionCandidate{
			subtype: "init",
			text:    text,
			message: "system subtype=init",
		}, true
	case "tool_result":
		snippet := claudeSessionFirstLine(text)
		if snippet == "" {
			return "", nil, false
		}
		return "type=tool_result content=" + snippet, nil, true
	default:
		return "", nil, false
	}
}

func claudeSessionCandidateEvidence(candidate *claudeSessionCandidate, rawSignal string) *reliability.FailureEvidence {
	text := strings.Join([]string{candidate.stopReason, candidate.subtype, candidate.text, candidate.message, rawSignal}, "\n")
	ev := &reliability.FailureEvidence{
		Category:  reliability.CategoryUnidentifiedIssue,
		Harness:   "claude",
		Provider:  reliability.ProviderAnthropic,
		Message:   candidate.message,
		Source:    claudeSessionLogSource,
		RawSignal: rawSignal,
	}
	if parsed := reliability.ParseClaudeError(text); parsed != nil {
		ev.Category = parsed.Category
		ev.StatusCode = parsed.StatusCode
		ev.ResetAfter = parsed.ResetAfter
		ev.ResetAt = parsed.ResetAt
		ev.RetryAfter = parsed.RetryAfter
	}

	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "authentication_error"):
		ev.Category = reliability.CategoryAuthOrProxy
		ev.StatusCode = 401
	case strings.Contains(lower, "overloaded_error"):
		ev.Category = reliability.CategoryProviderOverloaded
		ev.StatusCode = 529
		ev.RetryAfter = 30 * time.Second
	}
	return ev
}

func claudeSessionMessageParts(raw json.RawMessage) (stopReason string, snippets []string) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", nil
	}
	var msg claudeSessionLogMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return "", claudeSessionRawStringSnippets(raw, 4)
	}
	snippets = append(snippets, claudeSessionContentSnippets(msg.Content)...)
	if len(snippets) == 0 {
		snippets = append(snippets, claudeSessionRawStringSnippets(raw, 4)...)
	}
	return msg.StopReason, snippets
}

func claudeSessionContentSnippets(raw json.RawMessage) []string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil
		}
		if first := claudeSessionFirstLine(s); first != "" {
			return []string{first}
		}
		return nil
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil
		}
		var snippets []string
		for _, item := range items {
			snippets = append(snippets, claudeSessionContentSnippets(item)...)
			if len(snippets) >= 4 {
				return snippets[:4]
			}
		}
		return snippets
	case '{':
		var block claudeSessionContentBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			return claudeSessionRawStringSnippets(raw, 1)
		}
		if block.Type == "thinking" {
			return nil
		}
		var snippets []string
		if block.Text != "" {
			snippets = append(snippets, claudeSessionFirstLine(block.Text))
		}
		if len(block.Content) > 0 {
			if block.Type == "tool_result" {
				if first := claudeSessionFirstLineFromRaw(block.Content); first != "" {
					snippets = append(snippets, first)
				}
			} else {
				snippets = append(snippets, claudeSessionContentSnippets(block.Content)...)
			}
		}
		if len(snippets) == 0 && block.Name != "" {
			snippets = append(snippets, "name="+block.Name)
		}
		return compactClaudeSessionSnippets(snippets, 4)
	default:
		return nil
	}
}

func claudeSessionRawStringSnippets(raw json.RawMessage, max int) []string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || max <= 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var out []string
	collectClaudeSessionStrings(v, "", &out, max)
	return out
}

func collectClaudeSessionStrings(v any, key string, out *[]string, max int) {
	if len(*out) >= max || claudeSessionSkipKey(key) {
		return
	}
	switch x := v.(type) {
	case string:
		if first := claudeSessionFirstLine(x); first != "" {
			*out = append(*out, first)
		}
	case []any:
		for _, item := range x {
			collectClaudeSessionStrings(item, key, out, max)
			if len(*out) >= max {
				return
			}
		}
	case map[string]any:
		for k, item := range x {
			collectClaudeSessionStrings(item, k, out, max)
			if len(*out) >= max {
				return
			}
		}
	}
}

func claudeSessionSkipKey(key string) bool {
	switch strings.ToLower(key) {
	case "display", "prompt", "base_instructions", "transcript":
		return true
	default:
		return false
	}
}

func claudeSessionFirstLineFromRaw(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return claudeSessionFirstLine(s)
	}
	snippets := claudeSessionRawStringSnippets(raw, 1)
	if len(snippets) == 0 {
		return ""
	}
	return snippets[0]
}

func claudeSessionFirstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	return reliability.TruncateSignal(s, 200)
}

func claudeSessionHasErrorSignal(s string) bool {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "no error") || strings.Contains(lower, "no errors") {
		return false
	}
	for _, marker := range []string{
		"error",
		"failed",
		"failure",
		"exception",
		"overloaded",
		"authentication",
		"unauthorized",
		"invalid api key",
		"permission denied",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func compactClaudeSessionSnippets(snippets []string, max int) []string {
	var out []string
	for _, snippet := range snippets {
		snippet = strings.TrimSpace(snippet)
		if snippet == "" {
			continue
		}
		out = append(out, snippet)
		if len(out) >= max {
			return out
		}
	}
	return out
}
