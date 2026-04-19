package coordination

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// FileStore is a file-backed implementation of Store that persists
// coordination state to a JSON file for cross-session durability.
type FileStore struct {
	mu   sync.RWMutex
	data map[string][]byte
	path string
}

// NewFileStore creates a file-backed coordination store.
// If the file exists, data is loaded from it. If not, an empty store is created.
//
// Expected:
//   - path is a valid filesystem path for the JSON file.
//
// Returns:
//   - A ready-to-use FileStore, or an error if loading fails.
//
// Side effects:
//   - Reads from the filesystem if the file exists.
func NewFileStore(path string) (*FileStore, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create coordination store directory: %w", err)
	}

	fs := &FileStore{
		data: make(map[string][]byte),
		path: path,
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fs, nil
		}
		return nil, fmt.Errorf("read coordination store: %w", err)
	}

	// Decode as map[string]string (JSON-safe), then convert to []byte.
	var stored map[string]string
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("parse coordination store: %w", err)
	}
	for k, v := range stored {
		fs.data[k] = []byte(v)
	}

	return fs, nil
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
func (fs *FileStore) Get(key string) ([]byte, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	val, ok := fs.data[key]
	if !ok {
		return nil, ErrKeyNotFound
	}

	return val, nil
}

// Set stores a value under the given key and persists to disk.
//
// Expected:
//   - key is a non-empty string identifying the entry.
//   - value is the byte slice to store.
//
// Returns:
//   - nil on success.
//   - An error if persistence fails.
//
// Side effects:
//   - Stores the key-value pair in memory and writes to the backing file.
func (fs *FileStore) Set(key string, value []byte) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.data[key] = value

	return fs.persist()
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
func (fs *FileStore) List(prefix string) ([]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	var keys []string
	for k := range fs.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}

	if keys == nil {
		return []string{}, nil
	}

	return keys, nil
}

// Delete removes the value associated with the given key and persists to disk.
//
// Expected:
//   - key is a non-empty string identifying the entry to remove.
//
// Returns:
//   - nil on success.
//   - ErrKeyNotFound if the key does not exist.
//
// Side effects:
//   - Removes the key-value pair from memory and writes to the backing file.
func (fs *FileStore) Delete(key string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if _, ok := fs.data[key]; !ok {
		return ErrKeyNotFound
	}

	delete(fs.data, key)

	return fs.persist()
}

// Increment atomically increments the integer counter stored at key,
// persists to disk, and returns the new value.
//
// Expected:
//   - key is a non-empty string identifying the counter entry.
//
// Returns:
//   - The new counter value and nil error on success.
//   - 0 and an error if the existing value cannot be parsed as an integer.
//
// Side effects:
//   - Creates the counter at 1 if the key does not exist.
//   - Updates the stored value to the incremented count and writes to the backing file.
func (fs *FileStore) Increment(key string) (int, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	current := 0

	if val, exists := fs.data[key]; exists {
		n, err := strconv.Atoi(string(val))
		if err != nil {
			return 0, fmt.Errorf("parsing counter at %q: %w", key, err)
		}

		current = n
	}

	current++
	fs.data[key] = []byte(strconv.Itoa(current))

	if err := fs.persist(); err != nil {
		return 0, err
	}

	return current, nil
}

// persist writes the in-memory data to the backing JSON file.
// MUST be called while fs.mu is held (from Set/Delete/Increment).
//
// Expected:
//   - The write lock is held by the caller.
//
// Returns:
//   - An error if marshalling or writing fails.
//
// Side effects:
//   - Writes to the filesystem.
func (fs *FileStore) persist() error {
	stored := make(map[string]string, len(fs.data))
	for k, v := range fs.data {
		stored[k] = string(v)
	}
	raw, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal coordination store: %w", err)
	}
	return os.WriteFile(fs.path, raw, 0o600)
}
