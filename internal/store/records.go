package store

// TryRecord represents a single agent execution attempt.
type TryRecord struct {
	ID            int      `json:"id"`
	RunID         int      `json:"run_id"`
	RelayID       int      `json:"relay_id"`
	AgentType     string   `json:"agent_type"`
	Completed     bool     `json:"completed"`
	Summary       string   `json:"summary"`
	RemainingWork string   `json:"remaining_work"`
	FilesChanged  []string `json:"files_changed"`
	CommitHash    string   `json:"commit_hash"`
	StartedAt     string   `json:"started_at"`
	EndedAt       string   `json:"ended_at"`
	AttemptNumber int      `json:"attempt_number"`
	LogPath       string   `json:"log_path,omitempty"`
	FailReason    string   `json:"fail_reason,omitempty"`
	RuntimeMs     int64    `json:"runtime_ms,omitempty"`
	LapID         string   `json:"lap_id,omitempty"`
	LapAssignee   string   `json:"lap_assignee,omitempty"`
	RecordedLaps  []string `json:"recorded_laps,omitempty"`
	ToolCalls     int      `json:"tool_calls,omitempty"`
}

// MessageRecord represents an inbox message that can be consumed by a run.
type MessageRecord struct {
	ID                int    `json:"id"`
	Body              string `json:"body"`
	Status            string `json:"status"` // pending, addressed, cancelled
	Position          int    `json:"position"`
	Scope             string `json:"scope"` // "run" (default) or "relay"
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	ConsumedByRunID   *int   `json:"consumed_by_run_id,omitempty"`
	ConsumedByRelayID *int   `json:"consumed_by_relay_id,omitempty"`
	RelayID           *int   `json:"relay_id,omitempty"`
}

// RelayRecord tracks the lifecycle of a relay session.
type RelayRecord struct {
	ID                  int    `json:"id"`
	TargetIterations    int    `json:"target_iterations"`
	CompletedIterations int    `json:"completed_iterations"`
	AgentMix            string `json:"agent_mix"`
	StartedAt           string `json:"started_at"`
	EndedAt             string `json:"ended_at,omitempty"`
	FirstTryID          int    `json:"first_try_id,omitempty"`
	LastTryID           int    `json:"last_try_id,omitempty"`
	ConsumedMessageIDs  []int  `json:"consumed_message_ids,omitempty"`
}

// AgentStatusEvent tracks pause/freeze/unfreeze/active events for an agent type.
type AgentStatusEvent struct {
	AgentType string `json:"agent_type"`
	Model     string `json:"model,omitempty"`
	EventType string `json:"event_type"` // paused, unfrozen, frozen, active
	Timestamp string `json:"timestamp"`
	RelayID   int    `json:"relay_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}
