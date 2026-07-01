package agent

import (
	"bytes"
	"context"
	"fmt"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"io"
	"os/exec"
	"strings"
)

type GenericExecutor struct {
	Command        []string
	ModelFlag      *string
	OutputStrategy string
	OutputLines    int
	TailStream     string
	Model          string
}

func (g *GenericExecutor) ResumeSupported() bool        { return false }
func (g *GenericExecutor) RotateSupported() bool        { return false }
func (g *GenericExecutor) LivenessProbeSupported() bool { return false }
func (g *GenericExecutor) RotateModel(string) error {
	return fmt.Errorf("rotate not supported by generic adapter")
}
func (g *GenericExecutor) ProbeLiveness(_ context.Context) (bool, error) {
	return false, fmt.Errorf("liveness probe not supported by generic adapter")
}

func (g *GenericExecutor) Execute(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
	if g.OutputStrategy != "" && g.OutputStrategy != "tail" {
		return nil, fmt.Errorf("generic harness: unsupported output_strategy %q", g.OutputStrategy)
	}

	prompt := harnessapi.BuildPrompt(opts)
	outputLines := g.OutputLines
	if outputLines <= 0 {
		outputLines = 40
	}
	tailStream := g.TailStream
	if tailStream == "" {
		tailStream = "combined"
	}

	args := make([]string, len(g.Command))
	copy(args, g.Command)

	hasPrompt := false
	for i, elem := range args {
		if strings.Contains(elem, "$PROMPT") {
			args[i] = strings.ReplaceAll(elem, "$PROMPT", prompt)
			hasPrompt = true
		}
	}

	model := g.Model
	if opts.Model != "" {
		model = opts.Model
	}

	if g.ModelFlag != nil && model != "" {
		if *g.ModelFlag != "" {
			args = append(args, *g.ModelFlag, model)
		} else {
			args = append(args, model)
		}
	} else if g.ModelFlag == nil && model != "" {
		fmt.Printf("info: model %q resolved but harness has no model_flag configured — passing model not supported, harness default will be used\n", model)
	}

	baseCmd := args[0]
	cmdArgs := args[1:]
	cmd := exec.CommandContext(ctx, baseCmd, cmdArgs...)
	if opts.WorkspaceDir != "" {
		cmd.Dir = opts.WorkspaceDir
	}
	SetProcessGroup(cmd)

	return g.runGenericCommand(cmd, prompt, hasPrompt, tailStream, outputLines, opts)
}

func (g *GenericExecutor) runGenericCommand(
	cmd *exec.Cmd,
	prompt string,
	promptInArgs bool, // true when $PROMPT was substituted; stdin not used
	tailStream string,
	outputLines int,
	opts harnessapi.RunOptions,
) (*harnessapi.TryResult, error) {
	logFile, err := openTryLog(opts.LogPath)
	if err != nil {
		return nil, err
	}
	if logFile != nil {
		defer logFile.Close()
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	var stdoutDst io.Writer = &stdoutBuf
	if logFile != nil {
		stdoutDst = io.MultiWriter(&stdoutBuf, &ansiFilterWriter{w: logFile})
	}
	cmd.Stdout = stdoutDst
	cmd.Stderr = &stderrBuf

	if !promptInArgs {
		cmd.Stdin = strings.NewReader(prompt)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("generic harness: start: %w", err)
	}
	if opts.OnStart != nil && cmd.Process != nil {
		opts.OnStart(cmd.Process.Pid)
	}

	waitErr := cmd.Wait()

	var selected []byte
	switch tailStream {
	case "stdout":
		selected = stdoutBuf.Bytes()
	case "stderr":
		selected = stderrBuf.Bytes()
	default:
		selected = append(stdoutBuf.Bytes(), stderrBuf.Bytes()...)
	}

	summary := tailLines(string(selected), outputLines)
	completed := waitErr == nil

	return &harnessapi.TryResult{Completed: completed, Summary: summary}, nil
}

func tailLines(s string, n int) string {
	if n <= 0 {
		return s
	}
	// Strip ANSI/VT escape sequences so TUI output from opencode (and similar
	// agents that enter interactive mode after completing their task) doesn't
	// corrupt the summary stored in the try record.
	s = stripANSI(s)
	s = strings.TrimRight(s, " \t\n\r")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// stripANSI removes ANSI/VT100 escape sequences from s.
// This handles both CSI sequences (ESC [) and OSC sequences (ESC ]) that
// opencode emits when it enters interactive TUI mode after completing a task.
func stripANSI(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) {
			i++
			switch s[i] {
			case '[': // CSI sequence: ESC [ ... <terminator>
				i++
				for i < len(s) && !isFinalByte(s[i]) {
					i++
				}
				if i < len(s) {
					i++ // consume final byte
				}
			case ']': // OSC sequence: ESC ] ... BEL or ST
				i++
				for i < len(s) {
					if s[i] == '\x07' { // BEL
						i++
						break
					}
					if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' { // ST
						i += 2
						break
					}
					i++
				}
			default:
				i++ // skip single-char escape
			}
		} else {
			out.WriteByte(s[i])
			i++
		}
	}
	return out.String()
}

func isFinalByte(b byte) bool {
	return b >= 0x40 && b <= 0x7e
}

// ansiFilterWriter strips ANSI/VT escape sequences before writing to the
// underlying writer. This lets the log file's modification time track real
// content activity rather than TUI redraw cycles, which is critical for the
// stall detector's log-silence signal.
type ansiFilterWriter struct {
	w io.Writer
}

func (f *ansiFilterWriter) Write(p []byte) (int, error) {
	stripped := stripANSI(string(p))
	if strings.TrimSpace(stripped) == "" {
		return len(p), nil // discard pure whitespace/escape frames
	}
	_, err := f.w.Write([]byte(stripped))
	return len(p), err
}
