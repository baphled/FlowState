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
func (s *FileChainStore) Flush() error {
	return s.persist()
}
