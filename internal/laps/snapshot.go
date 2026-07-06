package laps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// QueueSnapshot is a structured read-only view of the laps queue.
type QueueSnapshot struct {
	Missing     bool
	State       string
	Counts      QueueCounts
	Claim       QueueClaim
	Assignees   []QueueAssignee
	ActiveStint string
	Stints      []StintSnapshot
	Gate        *QueueGate
	Entries     []QueueEntry
}

type QueueCounts struct {
	Todo  int
	Done  int
	Total int
}

type QueueClaim struct {
	Valid      bool
	Lap        string
	File       string
	ClaimedAt  string
	AgeSeconds int
}

// QueueAssignee is one row of the status assignee breakdown of todo laps.
type QueueAssignee struct {
	Assignee string `json:"assignee"`
	Todo     int    `json:"todo"`
}

type QueueGate struct {
	State   string
	Stint   string
	Scope   string
	File    string
	Message string
}

type StintSnapshot struct {
	Name     string
	Scope    string
	File     string
	Todo     int
	Done     int
	Total    int
	Queued   bool
	Archived bool
	Active   bool
	Laps     []QueueEntry
}

type QueueEntry struct {
	Kind        string
	ID          string
	Ref         string
	Title       string
	Description string
	Assignee    string
	IsDone      bool
	CompletedAt string
	Order       int
	Laps        []QueueEntry
	Stint       *StintSnapshot
}

// QueueSnapshot loads the laps queue through the laps CLI. Missing workspaces or
// missing laps binaries are represented as Missing snapshots, not errors.
func (a *Adapter) QueueSnapshot(ctx context.Context) (QueueSnapshot, error) {
	if _, err := os.Stat(filepath.Join(a.WorkspaceDir, ".laps")); err != nil {
		if os.IsNotExist(err) {
			return QueueSnapshot{Missing: true}, nil
		}
		return QueueSnapshot{}, err
	}
	if _, err := exec.LookPath("laps"); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return QueueSnapshot{Missing: true}, nil
		}
		return QueueSnapshot{}, err
	}

	status, err := a.loadQueueStatus(ctx)
	if err != nil {
		return QueueSnapshot{}, err
	}
	root, err := a.loadQueueList(ctx, "")
	if err != nil {
		return QueueSnapshot{}, err
	}

	stints := make([]StintSnapshot, len(status.Stints))
	stintsByName := make(map[string]*StintSnapshot, len(status.Stints))
	for i, stint := range status.Stints {
		stints[i] = StintSnapshot{
			Name:     stint.Name,
			Scope:    stint.Scope,
			File:     stint.File,
			Todo:     stint.Todo,
			Done:     stint.Done,
			Total:    stint.Total,
			Queued:   stint.Queued,
			Archived: stint.Archived,
			Active:   stint.Active,
		}
		if stint.Queued && !stint.Archived {
			laps, err := a.loadQueueList(ctx, filepath.ToSlash(filepath.Join("stints", stint.Name+".laps")))
			if err != nil {
				return QueueSnapshot{}, err
			}
			stints[i].Laps = laps
		}
		stintsByName[stints[i].Name] = &stints[i]
	}

	for i := range root {
		if root[i].Kind != "stint" {
			continue
		}
		name := root[i].Ref
		if name == "" {
			name = root[i].ID
		}
		if stint, ok := stintsByName[name]; ok {
			root[i].Stint = stint
			root[i].Laps = append([]QueueEntry(nil), stint.Laps...)
		}
	}

	var gate *QueueGate
	if status.Gate != nil {
		gate = &QueueGate{
			State:   status.Gate.State,
			Stint:   status.Gate.Stint,
			Scope:   status.Gate.Scope,
			File:    status.Gate.File,
			Message: status.Gate.Message,
		}
	}

	activeStint := ""
	if status.ActiveStint != nil {
		activeStint = status.ActiveStint.Name
	}
	return QueueSnapshot{
		State:       status.State,
		Counts:      status.Counts,
		Claim:       status.Claim,
		Assignees:   append([]QueueAssignee(nil), status.Assignees...),
		ActiveStint: activeStint,
		Stints:      stints,
		Gate:        gate,
		Entries:     root,
	}, nil
}

func (a *Adapter) loadQueueStatus(ctx context.Context) (queueStatusJSON, error) {
	cmd := exec.CommandContext(ctx, "laps", "status", "--json-output")
	cmd.Dir = a.WorkspaceDir
	out, err := cmd.Output()
	if err != nil {
		return queueStatusJSON{}, commandError("laps status --json-output", err)
	}
	var status queueStatusJSON
	if err := json.Unmarshal(out, &status); err != nil {
		return queueStatusJSON{}, fmt.Errorf("decode laps status JSON: %w", err)
	}
	return status, nil
}

func (a *Adapter) loadQueueList(ctx context.Context, file string) ([]QueueEntry, error) {
	// --root pins scope: without it, laps list transparently descends into an
	// active stint and the snapshot would lose the root queue and gate context.
	args := []string{"list", "--root", "--all", "--json-output"}
	label := "laps list --root --all --json-output"
	if file != "" {
		args = []string{"-f", file, "list", "--all", "--json-output"}
		label = "laps -f " + file + " list --all --json-output"
	}
	cmd := exec.CommandContext(ctx, "laps", args...)
	cmd.Dir = a.WorkspaceDir
	out, err := cmd.Output()
	if err != nil {
		return nil, commandError(label, err)
	}
	return parseQueueList(out)
}

func parseQueueList(data []byte) ([]QueueEntry, error) {
	var payload queueListJSON
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode laps list JSON: %w", err)
	}
	entries := make([]QueueEntry, len(payload.Tasks))
	for i, task := range payload.Tasks {
		entries[i] = QueueEntry{
			Kind:        task.Kind,
			ID:          task.ID,
			Ref:         task.Ref,
			Title:       task.Title,
			Description: task.Description,
			Assignee:    task.Assignee,
			IsDone:      task.IsDone,
			CompletedAt: task.CompletedAt,
			Order:       task.Order,
		}
	}
	return entries, nil
}

func commandError(label string, err error) error {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("%s failed with exit code %d: %s", label, exitErr.ExitCode(), strings.TrimSpace(string(exitErr.Stderr)))
	}
	return fmt.Errorf("%s failed: %w", label, err)
}

type queueStatusJSON struct {
	File        string          `json:"file"`
	State       string          `json:"state"`
	Counts      QueueCounts     `json:"counts"`
	Head        json.RawMessage `json:"head"`
	Claim       QueueClaim      `json:"claim"`
	Assignees   []QueueAssignee `json:"assignees"`
	ActiveStint *stintJSON      `json:"activeStint"`
	Stints      []stintJSON     `json:"stints"`
	Gate        *QueueGate      `json:"gate"`
}

type stintJSON struct {
	Name     string `json:"name"`
	Scope    string `json:"scope"`
	File     string `json:"file"`
	Todo     int    `json:"todo"`
	Done     int    `json:"done"`
	Total    int    `json:"total"`
	Queued   bool   `json:"queued"`
	Archived bool   `json:"archived"`
	Active   bool   `json:"active"`
}

type queueListJSON struct {
	Tasks []queueTaskJSON `json:"tasks"`
}

type queueTaskJSON struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Ref         string `json:"ref"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Assignee    string `json:"assignee"`
	IsDone      bool   `json:"isDone"`
	CompletedAt string `json:"completedAt"`
	Order       int    `json:"order"`
}
