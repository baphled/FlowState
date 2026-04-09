package recall

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// persistedChainStore stores the serialised chain state on disk.
type persistedChainStore struct {
	ChainID string       `json:"chain_id"`
	Entries []chainEntry `json:"entries"`
}

// FileChainStore provides file-based storage for message chains.
type FileChainStore struct {
	*InMemoryChainStore
	path            string
	mu              sync.Mutex
	pendingPersists int
	flushThreshold  int
	flushInterval   time.Duration
}

// NewFileChainStore creates a new FileChainStore.
//
// Expected:
//   - path identifies the backing JSON file.
//
// Returns:
//   - A file-backed chain store.
//   - An error when the directory cannot be created or existing data cannot be loaded.
//
// Side effects:
//   - Creates the parent directory when needed.
//   - Loads any existing chain state from disk.
func NewFileChainStore(path string) (*FileChainStore, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	store := &FileChainStore{
		InMemoryChainStore: NewInMemoryChainStore(nil),
		path:               path,
		flushThreshold:     5,
		flushInterval:      10 * time.Second,
	}

	if err := store.load(); err != nil {
		return nil, err
	}

	return store, nil
}

// Append adds a message to the chain for the specified agent.
//
// Expected:
//   - agentID identifies the owning agent.
//   - msg contains the message to append.
//
// Returns:
//   - An error when the in-memory append or deferred persistence fails.
//
// Side effects:
//   - Mutates the in-memory chain state.
//   - May persist the chain state asynchronously or immediately.
func (s *FileChainStore) Append(agentID string, msg provider.Message) error {
	s.mu.Lock()
	if err := s.InMemoryChainStore.Append(agentID, msg); err != nil {
		s.mu.Unlock()
		return err
	}
	s.pendingPersists++
	shouldFlush := s.pendingPersists >= s.flushThreshold
	s.mu.Unlock()

	if shouldFlush {
		return s.persist()
	}

	go func() {
		time.Sleep(s.flushInterval)
		if err := s.persist(); err != nil {
			log.Printf("error: failed to persist chain store: %v", err)
		}
	}()
	return nil
}

// persist writes the chain store to disk.
//
// Expected:
//   - The store has valid in-memory state.
//
// Returns:
//   - An error when serialisation, write, or rename fails.
//
// Side effects:
//   - Writes a temporary file and renames it over the target file.
func (s *FileChainStore) persist() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pendingPersists == 0 && s.path != "" {
		return nil
	}

	persisted := persistedChainStore{
		ChainID: s.chainID,
		Entries: s.entries,
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return err
	}

	s.pendingPersists = 0
	log.Printf("info: chain store persisted %d entries", len(s.entries))
	return nil
}

// load restores the chain store from disk.
//
// Expected:
//   - s.path identifies the backing file.
//
// Returns:
//   - An error when the file exists but cannot be read or decoded.
//
// Side effects:
//   - Populates the in-memory chain state from the file when present.
func (s *FileChainStore) load() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var persisted persistedChainStore
	if err := json.Unmarshal(data, &persisted); err != nil {
		return err
	}

	s.chainID = persisted.ChainID
	s.entries = persisted.Entries
	return nil
}

// Flush ensures all pending messages are persisted to storage.
//
// Expected:
//   - The store has valid in-memory state.
//
// Returns:
//   - An error when persistence fails.
//
// Side effects:
//   - Writes pending chain data to disk.
func (s *FileChainStore) Flush() error {
	return s.persist()
}
