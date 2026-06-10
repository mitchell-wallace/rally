package store

// TryRecord represents a single agent execution attempt.
type TryRecord struct {
	ID            int          `json:"id"`
	RunID         int          `json:"run_id"`
	RelayID       int          `json:"relay_id"`
	AgentType     string       `json:"agent_type"`
	Completed     bool         `json:"completed"`
	Summary       string       `json:"summary"`
	RemainingWork string       `json:"remaining_work"`
	FilesChanged  []string     `json:"files_changed"`
	CommitHash    string       `json:"commit_hash"`
	CommitHistory []string     `json:"commit_history,omitempty"`
	StartedAt     string       `json:"started_at"`
	EndedAt       string       `json:"ended_at"`
	AttemptNumber int          `json:"attempt_number"`
	LogPath       string       `json:"log_path,omitempty"`
	FailReason    string       `json:"fail_reason,omitempty"`
	Category      string       `json:"category,omitempty"`
	RuntimeMs     int64        `json:"runtime_ms,omitempty"`
	LapID         string       `json:"lap_id,omitempty"`
	LapAssignee   string       `json:"lap_assignee,omitempty"`
	RecordedLaps  []string     `json:"recorded_laps,omitempty"`
	LapsAttempted []LapAttempt `json:"laps_attempted,omitempty"`
	ToolCalls     int          `json:"tool_calls,omitempty"`
}

// LapAttempt records a laps completion or handoff command observed during a try.
type LapAttempt struct {
	LapID     string `json:"lap_id"`
	Timestamp string `json:"timestamp"`
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
	// EventType is one of: paused, retry_failed, frozen, probation, benched,
	// active, unfrozen.
	EventType string `json:"event_type"`
	Timestamp string `json:"timestamp"`
	RelayID   int    `json:"relay_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
	// ResetAt and QuotaScope are set on benched events: ResetAt is the RFC3339
	// deadline after which the agent is re-probed, and QuotaScope identifies the
	// usage-limit quota bucket that was exhausted.
	ResetAt    string `json:"reset_at,omitempty"`
	QuotaScope string `json:"quota_scope,omitempty"`
}
