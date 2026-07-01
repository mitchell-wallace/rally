package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/harness/process"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
)

// Executor is the concrete opencode adapter. It shells out to the opencode CLI
// in `run --format json` mode (client/server model), parses the streamed JSON
// event lines into a TryResult, and recovers failure evidence from the in-band
// stream plus opencode's server log on disk (see opencode_evidence.go).
type Executor struct {
	Model string
}

// New constructs an opencode adapter over the concrete Executor, returning the
// harnessapi.Executor contract.
func New(model string) harnessapi.Executor {
	return &Executor{Model: model}
}

const (
	// These are parser-local safety bounds for failure indicators. Persisted
	// final snippets have a separate cap at the storage boundary.
	openCodeFailureSummaryLimit = 512
	openCodeErrorRefLimit       = 96
)

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

func (o *Executor) ResumeSupported() bool        { return true }
func (o *Executor) RotateSupported() bool        { return true }
func (o *Executor) LivenessProbeSupported() bool { return false }
func (o *Executor) RotateModel(newModel string) error {
	o.Model = newModel
	return nil
}
func (o *Executor) ProbeLiveness(_ context.Context) (bool, error) {
	return false, fmt.Errorf("liveness probe not supported by opencode adapter")
}

func (o *Executor) Execute(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
	prompt := harnessapi.BuildPrompt(opts)

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
		args, warning = harnessapi.ApplyReasoningEffort(args, "opencode", opts.ReasoningEffort)
		defer harnessapi.EmitReasoningWarning(opts.LogPath, warning)
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
	process.SetProcessGroup(cmd)
	startedAt := time.Now()
	out, runErr := process.RunLoggedCommand(cmd, opts.LogPath, true, opts.OnStart)
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

func parseOpenCodeOutput(out []byte, processSucceeded bool) (*harnessapi.TryResult, error) {
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
		return &harnessapi.TryResult{
			Completed: false,
			Summary:   formatOpenCodeError(eventError),
			ToolCalls: toolCalls,
			SessionID: sessionID,
		}, nil
	}
	if combined == "" {
		return &harnessapi.TryResult{
			Completed: cleanCompletion,
			Summary:   openCodeNoTextSummary(out, sawJSONEvent, sawStepFinish, scanFailed, processSucceeded),
			ToolCalls: toolCalls,
			SessionID: sessionID,
		}, nil
	}

	var tr harnessapi.TryResult
	if err := json.Unmarshal([]byte(combined), &tr); err != nil {
		return &harnessapi.TryResult{
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
