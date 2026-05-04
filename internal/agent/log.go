package agent

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

	waitErr := cmd.Wait()
	streamErr := <-copyErr
	if streamErr != nil {
		return buf.Bytes(), streamErr
	}
	return buf.Bytes(), waitErr
}
