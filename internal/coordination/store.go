// Package coordination provides a key-value store for sharing context between
// agents during delegation chains.
package coordination

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Store defines the interface for key-value storage of coordination data.
type Store interface {
	// Get retrieves a value by key.
	Get(key string) ([]byte, error)
	// Set stores a key-value pair.
	Set(key string, value []byte) error
	// List returns all keys matching the given prefix.
	List(prefix string) ([]string, error)
	// Delete removes a key-value pair.
	Delete(key string) error
}

// Entry represents a key-value pair in the store.
type Entry struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

// FileStore implements Store with JSON file persistence.
type FileStore struct {
	path    string
	entries map[string][]byte
	mu      sync.RWMutex
}

// NewFileStore creates a new FileStore at the given path.
//
// If path is empty, it uses the default XDG data directory path.
//
// Expected:
//   - path is a valid file path for persisting coordination data.
//
// Returns:
//   - A FileStore loaded with any existing entries from the file.
//
// Side effects:
//   - Reads from the filesystem if the file already exists.
func NewFileStore(path string) *FileStore {
	if path == "" {
		path = defaultDataPath()
	}

	store := &FileStore{
		path:    path,
		entries: make(map[string][]byte),
	}
	store.load()
	return store
}

// defaultDataPath returns the default XDG data directory path for coordination data.
//
// Expected:
//   - None.
//
// Returns:
//   - A string path to the default coordination store location.
//
// Side effects:
//   - Reads XDG_DATA_HOME environment variable and user home directory.
func defaultDataPath() string {
	xdgDataHome := os.Getenv("XDG_DATA_HOME")
	if xdgDataHome != "" {
		return filepath.Join(xdgDataHome, "flowstate", "coordination", "store.json")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "flowstate", "coordination", "store.json")
	}

	return filepath.Join(homeDir, ".local", "share", "flowstate", "coordination", "store.json")
}

// load reads and unmarshals entries from the JSON file.
//
// Expected:
//   - None.
//
// Returns:
//   - None.
//
// Side effects:
//   - Populates s.entries with unmarshalled data from the file.
func (s *FileStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		s.entries = make(map[string][]byte)
		return
	}

	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		s.entries = make(map[string][]byte)
		return
	}

	s.entries = make(map[string][]byte)
	for _, e := range entries {
		s.entries[e.Key] = e.Value
	}
}

// persist writes the current entries to the JSON file atomically.
//
// Expected:
//   - s.entries contains the data to persist.
//
// Returns:
//   - An error if the write operation fails, nil on success.
//
// Side effects:
//   - Creates the directory if it does not exist.
//   - Writes to the filesystem.
func (s *FileStore) persist() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	entries := make([]Entry, 0, len(s.entries))
	for k, v := range s.entries {
		entries = append(entries, Entry{Key: k, Value: v})
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling: %w", err)
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

// Get retrieves a value by key.
//
// Expected:
//   - key is the key to retrieve.
//
// Returns:
//   - The value associated with the key.
//   - An error if the key does not exist.
//
// Side effects:
//   - None.
func (s *FileStore) Get(key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	value, ok := s.entries[key]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", key)
	}

	return value, nil
}

// Set stores a key-value pair.
//
// Expected:
//   - key is the key to store.
//   - value is the value to store.
//
// Returns:
//   - An error if the value cannot be written to disk.
//
// Side effects:
//   - Persists the updated entries to the JSON file.
func (s *FileStore) Set(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries[key] = value
	return s.persist()
}

// List returns all keys matching the given prefix.
//
// Expected:
//   - prefix is the prefix to filter keys by. Empty string returns all keys.
//
// Returns:
//   - A slice of matching keys, or an empty slice if none match.
//
// Side effects:
//   - None.
func (s *FileStore) List(prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var keys []string
	for k := range s.entries {
		if prefix == "" || strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}

	if keys == nil {
		keys = []string{}
	}

	return keys, nil
}

// Delete removes a key-value pair.
//
// Expected:
//   - key is the key to delete.
//
// Returns:
//   - An error if the key does not exist.
//
// Side effects:
//   - Persists the updated entries to the JSON file.
func (s *FileStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.entries[key]; !ok {
		return fmt.Errorf("key not found: %s", key)
	}

	delete(s.entries, key)
	return s.persist()
}
