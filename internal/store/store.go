package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	agentStatusWindowSize = 500
	messagesWindowSize    = 200
)

// Store provides unified read/write access to Rally's JSONL-backed storage.
//
// In production a Store is driven by a single relay goroutine, but tests (and
// any future live readers) observe it concurrently while the relay writes. mu
// guards every access to cache so those reads/writes can't race. Public methods
// take the lock; the private truncate/recovery helpers run under a caller that
// already holds it and must not re-lock.
type Store struct {
	dir      string
	stateDir string
	cache    *Cache
	mu       sync.Mutex
}

// NewStore creates a Store and loads all JSONL data into memory.
func NewStore(dir string) (*Store, error) {
	// If the .rally directory exists, run migration to ensure any legacy files
	// are moved to the state/ directory before loading the cache.
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		if err := MigrateRallyStateLayout(filepath.Dir(dir)); err != nil {
			return nil, fmt.Errorf("migrate rally state layout: %w", err)
		}
	}

	cache, err := LoadCache(dir)
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, stateDir: filepath.Join(dir, "state"), cache: cache}, nil
}
