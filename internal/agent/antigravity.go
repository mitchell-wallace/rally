package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mitchell-wallace/rally/internal/reliability"
)

const DefaultAntigravityModel = "Gemini 3.5 Flash (High)"

const defaultAntigravityPrintTimeout = 30 * time.Minute

const (
	antigravityGlogSource    = "antigravity_glog"
	antigravityGlogTailBytes = int64(1 << 20)
)

var antigravityConversationRe = regexp.MustCompile(`Print mode: conversation=([0-9a-fA-F-]+)`)

var antigravityGlogPrefixRe = regexp.MustCompile(`^([IWEF])\d{4}\s+\d{2}:\d{2}:\d{2}\.\d{6}\b`)

var antigravitySettingsMu sync.Mutex

type AntigravityExecutor struct {
	Model        string
	PrintTimeout time.Duration
}

func (a *AntigravityExecutor) ResumeSupported() bool        { return true }
func (a *AntigravityExecutor) RotateSupported() bool        { return false }
func (a *AntigravityExecutor) LivenessProbeSupported() bool { return false }
func (a *AntigravityExecutor) RotateModel(string) error {
	return fmt.Errorf("rotate not supported by antigravity adapter")
}
func (a *AntigravityExecutor) ProbeLiveness(_ context.Context) (bool, error) {
	return false, fmt.Errorf("liveness probe not supported by antigravity adapter")
}

func (a *AntigravityExecutor) Execute(ctx context.Context, opts RunOptions) (*TryResult, error) {
	prompt := BuildPrompt(opts)

	model := a.Model
	if opts.Model != "" {
		model = opts.Model
	}

	unlock := func() {}
	restore := func() error { return nil }
	if model != "" {
		antigravitySettingsMu.Lock()
		unlock = antigravitySettingsMu.Unlock
		var err error
		restore, err = applyAntigravityModel(model)
		if err != nil {
			unlock()
			return nil, err
		}
		defer unlock()
		defer func() { _ = restore() }()
	}

	timeout := a.PrintTimeout
	if timeout <= 0 {
		timeout = defaultAntigravityPrintTimeout
	}

	agyLog, err := os.CreateTemp("", "agy-rally-*.log")
	if err != nil {
		return nil, fmt.Errorf("antigravity log temp file: %w", err)
	}
	agyLogPath := agyLog.Name()
	agyLog.Close()
	defer os.Remove(agyLogPath)

	args := []string{
		"--log-file=" + agyLogPath,
		"--print-timeout=" + timeout.String(),
		"--dangerously-skip-permissions",
	}
	if opts.ResumeSessionID != "" {
		args = append(args, "--conversation="+opts.ResumeSessionID)
	}
	if opts.ReasoningEffort != "" {
		var warning string
		args, warning = applyReasoningEffort(args, "antigravity", opts.ReasoningEffort)
		defer emitReasoningWarning(opts.LogPath, warning)
	}
	args = append(args, "--print", prompt)

	cmd := exec.CommandContext(ctx, "agy", args...)
	if opts.WorkspaceDir != "" {
		cmd.Dir = opts.WorkspaceDir
	}
	SetProcessGroup(cmd)
	out, runErr := runLoggedCommand(cmd, opts.LogPath, true, opts.OnStart)

	agyLogData, _ := os.ReadFile(agyLogPath)
	_ = appendAntigravityLog(opts.LogPath, agyLogData)
	sessionID := scanAntigravityConversationID(agyLogData)

	if runErr != nil {
		execErr := fmt.Errorf("antigravity exec failed: %w\noutput: %s\nlog: %s", runErr, string(out), tailString(string(agyLogData), 4096))
		errorText := string(out) + "\n" + string(agyLogData)
		if ev := reliability.ParseGeminiError(errorText); ev != nil {
			return &TryResult{Evidence: ev, ResolvedModel: model}, execErr
		}
		if ev := antigravityGlogFailureEvidence(); ev != nil {
			return &TryResult{Evidence: ev, ResolvedModel: model}, execErr
		}
		return nil, execErr
	}

	tr, err := parseAntigravityOutput(out, sessionID)
	if err != nil {
		return nil, err
	}
	tr.ResolvedModel = model
	return tr, nil
}

func parseAntigravityOutput(out []byte, sessionID string) (*TryResult, error) {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return &TryResult{Completed: false, SessionID: sessionID}, nil
	}

	if tr, ok := parseTryResultJSON(text); ok {
		tr.SessionID = sessionID
		return tr, nil
	}

	if last := lastNonEmptyLine(text); last != "" && last != text {
		if tr, ok := parseTryResultJSON(last); ok {
			tr.SessionID = sessionID
			return tr, nil
		}
	}

	return &TryResult{Completed: true, Summary: text, SessionID: sessionID}, nil
}

func parseTryResultJSON(text string) (*TryResult, bool) {
	var tr TryResult
	if err := json.Unmarshal([]byte(text), &tr); err != nil {
		return nil, false
	}
	return &tr, true
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func scanAntigravityConversationID(logData []byte) string {
	matches := antigravityConversationRe.FindAllSubmatch(logData, -1)
	if len(matches) == 0 {
		return ""
	}
	last := matches[len(matches)-1]
	if len(last) < 2 {
		return ""
	}
	return string(last[1])
}

func appendAntigravityLog(path string, data []byte) error {
	if path == "" || len(data) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n--- agy log ---\n%s\n", tailString(string(data), 65536))
	return err
}

func antigravityGlogFailureEvidence() *reliability.FailureEvidence {
	path, err := latestAntigravityGlogPath()
	if err != nil || path == "" {
		return nil
	}
	data, err := readAntigravityGlogTail(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	return antigravityGlogEvidenceFromData(data)
}

func latestAntigravityGlogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", err
	}
	dir := filepath.Join(home, ".gemini", "antigravity-cli", "log")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	var latestPath string
	var latestMod time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if ok, err := filepath.Match("cli-*.log", entry.Name()); err != nil || !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if latestPath == "" || info.ModTime().After(latestMod) {
			latestPath = filepath.Join(dir, entry.Name())
			latestMod = info.ModTime()
		}
	}
	return latestPath, nil
}

func readAntigravityGlogTail(path string) ([]byte, error) {
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
	if st.Size() > antigravityGlogTailBytes {
		offset = st.Size() - antigravityGlogTailBytes
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

func antigravityGlogEvidenceFromData(data []byte) *reliability.FailureEvidence {
	lines := strings.Split(string(data), "\n")
	var errorIndices []int
	var lastErrorBody string
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if !antigravityGlogLineSafe(line) {
			continue
		}
		if antigravityGlogLineLevel(line) != 'E' {
			continue
		}
		errorIndices = append(errorIndices, i)
		if body := antigravityGlogBody(line); body != "" {
			lastErrorBody = body
		}
	}
	if len(errorIndices) == 0 {
		return nil
	}

	keep := map[int]struct{}{}
	for _, idx := range errorIndices {
		for i := idx - 1; i <= idx+2; i++ {
			if i >= 0 && i < len(lines) {
				keep[i] = struct{}{}
			}
		}
	}

	var rawParts []string
	for i, raw := range lines {
		if _, ok := keep[i]; !ok {
			continue
		}
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" || !antigravityGlogLineSafe(line) {
			continue
		}
		if level := antigravityGlogLineLevel(line); level != 0 && level != 'E' {
			continue
		}
		rawParts = append(rawParts, line)
	}
	if len(rawParts) == 0 {
		return nil
	}
	if lastErrorBody == "" {
		lastErrorBody = rawParts[len(rawParts)-1]
	}

	rawSignal := antigravityTruncateSignal(strings.Join(rawParts, "\n"), 256)
	return antigravityGlogEvidence(strings.Join(rawParts, "\n"), lastErrorBody, rawSignal)
}

func antigravityGlogEvidence(text, message, rawSignal string) *reliability.FailureEvidence {
	ev := &reliability.FailureEvidence{
		Category:  reliability.CategoryUnidentifiedIssue,
		Harness:   "antigravity",
		Provider:  reliability.ProviderGemini,
		Message:   antigravityTruncateSignal(message, 200),
		Source:    antigravityGlogSource,
		RawSignal: rawSignal,
	}

	lower := strings.ToLower(text)
	if strings.Contains(lower, "not logged into antigravity") || strings.Contains(lower, "error getting token source") {
		ev.Category = reliability.CategoryAuthOrProxy
		return ev
	}

	if parsed := reliability.ParseGeminiError(text); parsed != nil {
		ev.Category = parsed.Category
		ev.StatusCode = parsed.StatusCode
		ev.ResetAfter = parsed.ResetAfter
		ev.ResetAt = parsed.ResetAt
		ev.RetryAfter = parsed.RetryAfter
		if parsed.Provider != "" {
			ev.Provider = parsed.Provider
		}
	}
	return ev
}

func antigravityGlogLineLevel(line string) byte {
	m := antigravityGlogPrefixRe.FindStringSubmatch(strings.TrimSpace(line))
	if len(m) < 2 || len(m[1]) == 0 {
		return 0
	}
	return m[1][0]
}

func antigravityGlogLineSafe(line string) bool {
	for _, forbidden := range []string{"oauth_creds.json", "antigravity-oauth-token", "settings.json"} {
		if strings.Contains(line, forbidden) {
			return false
		}
	}
	return true
}

func antigravityGlogBody(line string) string {
	line = strings.TrimSpace(line)
	if idx := strings.Index(line, "]"); idx >= 0 && idx+1 < len(line) {
		return strings.TrimSpace(line[idx+1:])
	}
	return line
}

func antigravityTruncateSignal(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
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

func applyAntigravityModel(model string) (func() error, error) {
	path, err := antigravitySettingsPath()
	if err != nil {
		return nil, err
	}

	var original []byte
	var mode os.FileMode = 0o600
	existed := false
	if info, err := os.Stat(path); err == nil {
		existed = true
		mode = info.Mode().Perm()
		original, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("antigravity settings read: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("antigravity settings stat: %w", err)
	}

	settings := map[string]any{}
	if len(strings.TrimSpace(string(original))) > 0 {
		if err := json.Unmarshal(original, &settings); err != nil {
			return nil, fmt.Errorf("antigravity settings parse: %w", err)
		}
		if settings == nil {
			settings = map[string]any{}
		}
	}
	if current, ok := settings["model"].(string); ok && current == model {
		return func() error { return nil }, nil
	}
	settings["model"] = model

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("antigravity settings marshal: %w", err)
	}
	data = append(data, '\n')

	if err := writeFileAtomic(path, data, mode); err != nil {
		return nil, fmt.Errorf("antigravity settings write: %w", err)
	}

	return func() error {
		if existed {
			return writeFileAtomic(path, original, mode)
		}
		current, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		currentSettings := map[string]any{}
		if len(strings.TrimSpace(string(current))) > 0 {
			if err := json.Unmarshal(current, &currentSettings); err != nil {
				return err
			}
		}
		delete(currentSettings, "model")
		if len(currentSettings) == 0 {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}
		currentData, err := json.MarshalIndent(currentSettings, "", "  ")
		if err != nil {
			return err
		}
		currentData = append(currentData, '\n')
		return writeFileAtomic(path, currentData, mode)
	}, nil
}

func antigravitySettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("antigravity settings: resolve home: %w", err)
	}
	return filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"), nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
