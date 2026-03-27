package messages

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Scope string

const (
	ScopeSession Scope = "session"
	ScopeBatch   Scope = "batch"
)

type EventType string

const (
	EventMessageCreated  EventType = "message_created"
	EventMessageUpdated  EventType = "message_updated"
	EventMessageConsumed EventType = "message_consumed"
	EventMessageCanceled EventType = "message_cancelled"
)

type Event struct {
	EventID         int       `json:"event_id"`
	MessageID       int       `json:"message_id"`
	Scope           Scope     `json:"scope"`
	EventType       EventType `json:"event_type"`
	CreatedAt       string    `json:"created_at,omitempty"`
	UpdatedAt       string    `json:"updated_at,omitempty"`
	ConsumedAt      string    `json:"consumed_at,omitempty"`
	Body            string    `json:"body,omitempty"`
	TargetBatchID   *int      `json:"target_batch_id,omitempty"`
	ApplyBatchID    *int      `json:"apply_batch_id,omitempty"`
	TargetSessionID *int      `json:"target_session_id,omitempty"`
}

type Message struct {
	MessageID       int
	Scope           Scope
	Body            string
	TargetBatchID   *int
	ApplyBatchID    *int
	TargetSessionID *int
	CreatedAt       string
	UpdatedAt       string
	ConsumedAt      string
	CanceledAt      string
	Consumed        bool
	Canceled        bool
}

func (m Message) Pending() bool {
	return !m.Consumed && !m.Canceled
}

type Store struct {
	path string
}

func NewStore(dataDir string) *Store {
	return &Store{path: filepath.Join(dataDir, "messages.jsonl")}
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Load() ([]Event, error) {
	file, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var events []Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("decode message event: %w", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func Fold(events []Event) map[int]*Message {
	state := map[int]*Message{}
	for _, event := range events {
		msg, ok := state[event.MessageID]
		if !ok {
			msg = &Message{MessageID: event.MessageID}
			state[event.MessageID] = msg
		}
		switch event.EventType {
		case EventMessageCreated:
			msg.Scope = event.Scope
			msg.Body = event.Body
			msg.TargetBatchID = event.TargetBatchID
			msg.ApplyBatchID = event.ApplyBatchID
			msg.TargetSessionID = event.TargetSessionID
			msg.CreatedAt = event.CreatedAt
		case EventMessageUpdated:
			msg.Body = event.Body
			msg.TargetBatchID = event.TargetBatchID
			msg.TargetSessionID = event.TargetSessionID
			msg.UpdatedAt = event.UpdatedAt
		case EventMessageConsumed:
			msg.ApplyBatchID = event.ApplyBatchID
			msg.TargetSessionID = event.TargetSessionID
			msg.ConsumedAt = event.ConsumedAt
			msg.Consumed = true
		case EventMessageCanceled:
			msg.CanceledAt = event.UpdatedAt
			msg.Canceled = true
		}
	}
	return state
}

func OrderedMessages(state map[int]*Message) []*Message {
	ids := make([]int, 0, len(state))
	for id := range state {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	result := make([]*Message, 0, len(ids))
	for _, id := range ids {
		result = append(result, state[id])
	}
	return result
}

func (s *Store) Append(event Event) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = file.Write(append(encoded, '\n'))
	return err
}

func Timestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func PendingForDisplay(events []Event) ([]*Message, error) {
	folded := Fold(events)
	items := OrderedMessages(folded)
	return items, nil
}
