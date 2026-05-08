package agent

import (
	"bytes"
	"context"
	"fmt"
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

func (g *GenericExecutor) ResumeSupported() bool                { return false }
func (g *GenericExecutor) RotateSupported() bool                { return false }
func (g *GenericExecutor) LivenessProbeSupported() bool         { return false }
func (g *GenericExecutor) RotateModel(string) error {
	return fmt.Errorf("rotate not supported by generic adapter")
}
func (g *GenericExecutor) ProbeLiveness(_ context.Context) (bool, error) {
	return false, fmt.Errorf("liveness probe not supported by generic adapter")
}

func (g *GenericExecutor) Execute(ctx context.Context, opts RunOptions) (*TryResult, error) {
	if g.OutputStrategy != "" && g.OutputStrategy != "tail" {
		return nil, fmt.Errorf("generic harness: unsupported output_strategy %q", g.OutputStrategy)
	}

	prompt := BuildPrompt(opts)
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
	opts RunOptions,
) (*TryResult, error) {
	logFile, err := openTryLog(opts.LogPath)
	if err != nil {
		return nil, err
	}
	if logFile != nil {
		defer logFile.Close()
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("generic harness: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("generic harness: stderr pipe: %w", err)
	}

	if !promptInArgs {
		cmd.Stdin = strings.NewReader(prompt)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("generic harness: start: %w", err)
	}
	if opts.OnStart != nil && cmd.Process != nil {
		opts.OnStart(cmd.Process.Pid)
	}

	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		dst := io.MultiWriter(&stdoutBuf)
		if logFile != nil {
			dst = io.MultiWriter(&stdoutBuf, logFile)
		}
		io.Copy(dst, stdoutPipe)
	}()
	go func() {
		defer close(stderrDone)
		io.Copy(&stderrBuf, stderrPipe)
	}()

	waitErr := cmd.Wait()
	<-stdoutDone
	<-stderrDone

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

	return &TryResult{Completed: completed, Summary: summary}, nil
}

func tailLines(s string, n int) string {
	if n <= 0 {
		return s
	}
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
