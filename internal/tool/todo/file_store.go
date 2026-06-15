package todo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileStore is a thread-safe, file-backed implementation of Store. Each
// session's todo list is persisted as a JSON file at
// <baseDir>/<sessionID>.json. The store loads lazily on first Get and
// writes synchronously on every Set so a crash never loses committed
// todo state.
type FileStore struct {
	mu      sync.RWMutex
	baseDir string
	cache   map[string][]Item
}

// NewFileStore creates a new FileStore rooted at baseDir. The directory
// is created if it does not exist.
//
// Expected:
//   - baseDir is a writable filesystem path.
//
// Returns:
//   - A pointer to an initialised FileStore.
//
// Side effects:
//   - Creates baseDir (and parents) if it does not exist.
func NewFileStore(baseDir string) (*FileStore, error) {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating todo store directory: %w", err)
	}
	return &FileStore{
		baseDir: baseDir,
		cache:   make(map[string][]Item),
	}, nil
}

// Set replaces the entire todo list for the given session and persists
// it to disk.
//
// Expected:
//   - sessionID is a non-empty string identifying the session.
//   - todos is the complete desired state for that session.
//
// Returns:
//   - nil on success, or an error if the file write fails.
//
// Side effects:
//   - Acquires a write lock, updates the in-memory cache, and writes a
//     JSON file to disk.
func (s *FileStore) Set(sessionID string, todos []Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	copied := make([]Item, len(todos))
	copy(copied, todos)
	s.cache[sessionID] = copied

	return s.persistLocked(sessionID, copied)
}

// Get returns the current todo list for the given session, loading from
// disk on first access.
//
// Expected:
//   - sessionID is a string identifying the session.
//
// Returns:
//   - The stored slice of Item values, or an empty slice when none exist.
//
// Side effects:
//   - Acquires a read lock; may read from disk on first access for a
//     session.
func (s *FileStore) Get(sessionID string) []Item {
	s.mu.RLock()
	if cached, ok := s.cache[sessionID]; ok {
		s.mu.RUnlock()
		result := make([]Item, len(cached))
		copy(result, cached)
		return result
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if cached, ok := s.cache[sessionID]; ok {
		result := make([]Item, len(cached))
		copy(result, cached)
		return result
	}

	loaded, err := s.loadLocked(sessionID)
	if err != nil {
		return []Item{}
	}
	s.cache[sessionID] = loaded

	result := make([]Item, len(loaded))
	copy(result, loaded)
	return result
}

// persistLocked writes the todo list for sessionID to disk. The caller
// must hold s.mu.
func (s *FileStore) persistLocked(sessionID string, todos []Item) error {
	path := s.pathFor(sessionID)
	data, err := json.MarshalIndent(todos, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling todos for %s: %w", sessionID, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing todos for %s: %w", sessionID, err)
	}
	return os.Rename(tmp, path)
}

// loadLocked reads the todo list for sessionID from disk. The caller
// must hold s.mu.
func (s *FileStore) loadLocked(sessionID string) ([]Item, error) {
	path := s.pathFor(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Item{}, nil
		}
		return nil, fmt.Errorf("reading todos for %s: %w", sessionID, err)
	}
	var items []Item
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("unmarshalling todos for %s: %w", sessionID, err)
	}
	return items, nil
}

// pathFor returns the on-disk path for a session's todo file.
func (s *FileStore) pathFor(sessionID string) string {
	return filepath.Join(s.baseDir, sessionID+".json")
}
