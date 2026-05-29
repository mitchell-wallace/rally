package store

import (
	"fmt"
	"path/filepath"
)

// Cache holds all records in memory with index maps for O(1) lookups.
type Cache struct {
	Tries        []TryRecord
	Messages     []MessageRecord
	Relays       []RelayRecord
	AgentStatus  []AgentStatusEvent
	MessageIndex map[int]int // id -> index in Messages
	TryIndex     map[int]int // id -> index in Tries
	RelayIndex   map[int]int // id -> index in Relays
}

// LoadCache reads all JSONL files from rallyDir/state into a new Cache.
func LoadCache(rallyDir string) (*Cache, error) {
	c := &Cache{
		MessageIndex: make(map[int]int),
		TryIndex:     make(map[int]int),
		RelayIndex:   make(map[int]int),
	}

	stateDir := filepath.Join(rallyDir, "state")

	tries, err := readJSONL[TryRecord](filepath.Join(stateDir, "tries.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("load tries: %w", err)
	}
	c.Tries = tries
	for i, t := range tries {
		c.TryIndex[t.ID] = i
	}

	msgs, err := readJSONL[MessageRecord](filepath.Join(stateDir, "messages.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("load messages: %w", err)
	}
	c.Messages = msgs
	for i, m := range msgs {
		c.MessageIndex[m.ID] = i
	}

	relays, err := readJSONL[RelayRecord](filepath.Join(stateDir, "relays.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("load relays: %w", err)
	}
	c.Relays = relays
	for i, r := range relays {
		c.RelayIndex[r.ID] = i
	}

	status, err := readJSONL[AgentStatusEvent](filepath.Join(stateDir, "agent_status.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("load agent_status: %w", err)
	}
	c.AgentStatus = status

	return c, nil
}
