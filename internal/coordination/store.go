package coordination

import (
	"errors"
	"strings"
	"sync"
)

// ErrKeyNotFound is returned when a requested key does not exist in the store.
var ErrKeyNotFound = errors.New("key not found")

// Store defines the interface for a key-value coordination store
// used by agents to share context during delegation chains.
type Store interface {
	// Get retrieves the value associated with the given key.
	Get(key string) ([]byte, error)
	// Set stores a value under the given key.
	Set(key string, value []byte) error
	// List returns all keys matching the given prefix.
	List(prefix string) ([]string, error)
	// Delete removes the value associated with the given key.
	Delete(key string) error
}

// MemoryStore is an in-memory implementation of Store using a map
// protected by a read-write mutex for concurrent access.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryStore creates a new in-memory coordination store.
//
// Returns:
//   - A ready-to-use MemoryStore instance with an initialised map.
//
// Side effects:
//   - None.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		data: make(map[string][]byte),
	}
}

// Get retrieves the value associated with the given key.
//
// Expected:
//   - key is a non-empty string identifying the entry.
//
// Returns:
//   - The stored byte slice and nil error on success.
//   - nil and ErrKeyNotFound if the key does not exist.
//
// Side effects:
//   - None.
func (s *MemoryStore) Get(key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	val, ok := s.data[key]
	if !ok {
		return nil, ErrKeyNotFound
	}

	return val, nil
}

// Set stores a value under the given key.
//
// Expected:
//   - key is a non-empty string identifying the entry.
//   - value is the byte slice to store.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Stores the key-value pair in memory.
func (s *MemoryStore) Set(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[key] = value

	return nil
}

// List returns all keys matching the given prefix.
//
// Expected:
//   - prefix is the string prefix to filter keys by.
//
// Returns:
//   - A slice of matching key strings and nil error.
//   - An empty slice if no keys match.
//
// Side effects:
//   - None.
func (s *MemoryStore) List(prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var keys []string
	for k := range s.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}

	if keys == nil {
		return []string{}, nil
	}

	return keys, nil
}

// Delete removes the value associated with the given key.
//
// Expected:
//   - key is a non-empty string identifying the entry to remove.
//
// Returns:
//   - nil on success.
//   - ErrKeyNotFound if the key does not exist.
//
// Side effects:
//   - Removes the key-value pair from memory.
func (s *MemoryStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.data[key]; !ok {
		return ErrKeyNotFound
	}

	delete(s.data, key)

	return nil
}
