package agent

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func openTryLog(path string) (*os.File, error) {
	if path == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir for log: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	return f, nil
}

// WriteTryLog writes captured output to the try's log file.
// It creates parent directories if needed.
func WriteTryLog(path string, data []byte) error {
	f, err := openTryLog(path)
	if err != nil || f == nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// tailString returns the last n bytes of s, prefixed with "…" if truncated.
func tailString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func runLoggedCommand(cmd *exec.Cmd, logPath string, mergeStderr bool, onStart func(pid int)) ([]byte, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if mergeStderr {
		cmd.Stderr = cmd.Stdout
	}

	logFile, err := openTryLog(logPath)
	if err != nil {
		return nil, err
	}
	if logFile != nil {
		defer logFile.Close()
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if onStart != nil && cmd.Process != nil {
		onStart(cmd.Process.Pid)
	}

	var buf bytes.Buffer
	dst := io.Writer(&buf)
	if logFile != nil {
		dst = io.MultiWriter(&buf, logFile)
	}

	copyErr := make(chan error, 1)
	go func() {
		_, err := io.Copy(dst, stdout)
		copyErr <- err
	}()

	// Drain stdout before Wait; Wait closes the pipe, which would cause a
	// "file already closed" error if the goroutine is still reading.
	streamErr := <-copyErr
	waitErr := cmd.Wait()
	if streamErr != nil {
		return buf.Bytes(), streamErr
	}
	return buf.Bytes(), waitErr
}
