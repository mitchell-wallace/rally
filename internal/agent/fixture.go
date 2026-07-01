package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type FixtureExecutor struct {
	DiffPath   string
	OutputPath string
	Delay      time.Duration
	Dir        string
}

func (f *FixtureExecutor) ResumeSupported() bool        { return false }
func (f *FixtureExecutor) RotateSupported() bool        { return false }
func (f *FixtureExecutor) LivenessProbeSupported() bool { return false }
func (f *FixtureExecutor) RotateModel(string) error {
	return fmt.Errorf("rotate not supported by fixture adapter")
}
func (f *FixtureExecutor) ProbeLiveness(_ context.Context) (bool, error) {
	return false, fmt.Errorf("liveness probe not supported by fixture adapter")
}

func (f *FixtureExecutor) Execute(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
	dir := f.Dir
	if dir == "" && f.DiffPath != "" {
		dir = filepath.Dir(f.DiffPath)
	}

	if f.DiffPath != "" {
		alreadyApplied := false
		reverseCheck := exec.CommandContext(ctx, "git", "apply", "--reverse", "--check", f.DiffPath)
		reverseCheck.Dir = dir
		if err := reverseCheck.Run(); err == nil {
			alreadyApplied = true
		}

		if !alreadyApplied {
			apply := exec.CommandContext(ctx, "git", "apply", f.DiffPath)
			apply.Dir = dir
			if out, err := apply.CombinedOutput(); err != nil {
				return nil, fmt.Errorf("git apply failed: %w\noutput: %s", err, string(out))
			}

			add := exec.CommandContext(ctx, "git", "add", "-A")
			add.Dir = dir
			if out, err := add.CombinedOutput(); err != nil {
				return nil, fmt.Errorf("git add failed: %w\noutput: %s", err, string(out))
			}

			commit := exec.CommandContext(ctx, "git", "commit", "-m", "fixture apply", "--no-verify")
			commit.Dir = dir
			if out, err := commit.CombinedOutput(); err != nil {
				return nil, fmt.Errorf("git commit failed: %w\noutput: %s", err, string(out))
			}
		}
	}

	if f.Delay > 0 {
		select {
		case <-time.After(f.Delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if f.OutputPath != "" {
		data, err := os.ReadFile(f.OutputPath)
		if err != nil {
			return nil, fmt.Errorf("fixture output read failed: %w", err)
		}
		var tr harnessapi.TryResult
		if err := json.Unmarshal(data, &tr); err != nil {
			return nil, fmt.Errorf("fixture output parse failed: %w", err)
		}
		return &tr, nil
	}

	return &harnessapi.TryResult{Completed: true}, nil
}
