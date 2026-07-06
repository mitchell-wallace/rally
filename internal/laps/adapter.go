package laps

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Lap represents a single task from laps.
type Lap struct {
	ID          string // optional for read-only get; set from .laps/claim after claim
	Title       string
	Description string
	Assignee    string // optional, may be empty
}

// NoLap is the sentinel value returned when no head task is available.
var NoLap = Lap{}

// QueueState describes the laps queue state returned by head get/claim.
type QueueState string

const (
	StateLap      QueueState = "lap"
	StateHeld     QueueState = "held"
	StateEmpty    QueueState = "empty"
	StateComplete QueueState = "complete"
)

// Adapter interfaces with the laps binary in a workspace.
type Adapter struct {
	WorkspaceDir string
}

// HeadPull runs "laps get head" in the workspace directory and returns the
// current head Lap without claiming it. Current upstream laps output provides
// title, optional assignee, and description; ID remains empty unless upstream
// exposes it later.
func (a *Adapter) HeadPull(ctx context.Context) (Lap, QueueState, error) {
	cmd := exec.CommandContext(ctx, "laps", "get", "head")
	cmd.Dir = a.WorkspaceDir

	return parseHeadCommandOutput(cmd, "laps get head")
}

// ClaimHead runs "laps claim" in the workspace directory and returns the
// claimed head Lap. The claimed lap ID is read from .laps/claim, which is the
// state file laps uses to make a later bare "laps done" complete the intended
// task.
func (a *Adapter) ClaimHead(ctx context.Context) (Lap, QueueState, error) {
	cmd := exec.CommandContext(ctx, "laps", "claim")
	cmd.Dir = a.WorkspaceDir

	lap, state, err := parseHeadCommandOutput(cmd, "laps claim")
	if err != nil || state != StateLap {
		return lap, state, err
	}
	id, err := a.ReadClaim()
	if err != nil {
		return Lap{}, StateLap, err
	}
	lap.ID = id
	return lap, StateLap, nil
}

func parseHeadCommandOutput(cmd *exec.Cmd, label string) (Lap, QueueState, error) {
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 10:
				return NoLap, StateHeld, nil
			case 11:
				return NoLap, StateEmpty, nil
			case 12:
				return NoLap, StateComplete, nil
			default:
				return NoLap, "", fmt.Errorf("%s failed with exit code %d: %s", label, exitErr.ExitCode(), strings.TrimSpace(string(exitErr.Stderr)))
			}
		}
		return NoLap, "", fmt.Errorf("%s failed: %w", label, err)
	}

	lap, err := parseLapOutput(string(out))
	if err != nil {
		return Lap{}, StateLap, err
	}
	return lap, StateLap, nil
}

// ReadClaim returns the lap ID currently claimed by laps in this workspace.
func (a *Adapter) ReadClaim() (string, error) {
	data, err := os.ReadFile(filepath.Join(a.WorkspaceDir, ".laps", "claim"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var claim struct {
		Lap string `json:"lap"`
	}
	if err := json.Unmarshal(data, &claim); err == nil {
		return claim.Lap, nil
	}
	return strings.TrimSpace(string(data)), nil
}

// QueueSize runs "laps list --oneline" and returns the number of active tasks in the queue.
func (a *Adapter) QueueSize(ctx context.Context) (int, error) {
	cmd := exec.CommandContext(ctx, "laps", "list", "--oneline")
	cmd.Dir = a.WorkspaceDir
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, nil
}

// parseLapOutput parses task-detail output from "laps get head" or "laps claim".
//
// Expected formats:
//
//	Title
//
//	Description
//
// or, when an assignee is present:
//
//	Title
//	Assignee: <name>
//
//	Description
func parseLapOutput(output string) (Lap, error) {
	if hintStart := strings.Index(output, "\n\n-----\nNot the lap you intended"); hintStart >= 0 {
		output = output[:hintStart]
	}
	output = strings.TrimSuffix(output, "\n")
	lines := strings.Split(output, "\n")

	if len(lines) < 2 {
		return Lap{}, fmt.Errorf("unexpected laps task output format: %q", output)
	}

	title := lines[0]

	assignee := ""
	descStart := 2
	if strings.HasPrefix(lines[1], "Assignee: ") {
		assignee = strings.TrimPrefix(lines[1], "Assignee: ")
		descStart = 3
	}

	if descStart > len(lines) {
		return Lap{}, fmt.Errorf("unexpected laps task output format: %q", output)
	}

	description := strings.Join(lines[descStart:], "\n")

	return Lap{
		Title:       title,
		Description: description,
		Assignee:    assignee,
	}, nil
}
