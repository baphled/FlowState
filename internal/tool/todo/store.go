package todo

import "sync"

// Item represents a single todo entry.
type Item struct {
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
}

// Store manages the todo list for each session.
type Store interface {
	// Set replaces the entire todo list for a session.
	Set(sessionID string, todos []Item) error
	// Get returns the current todo list for a session.
	Get(sessionID string) []Item
}

// MemoryStore is a thread-safe in-memory implementation of Store.
type MemoryStore struct {
	mu    sync.RWMutex
	todos map[string][]Item
}

// NewMemoryStore creates a new empty MemoryStore.
//
// Returns:
//   - A pointer to an initialised MemoryStore with an empty internal map.
//
// Side effects:
//   - None.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		todos: make(map[string][]Item),
	}
}

// Set replaces the entire todo list for the given session.
//
// Expected:
//   - sessionID is a non-empty string identifying the session.
//   - todos is the complete desired state for that session.
//
// Returns:
//   - nil always; the method never produces an error.
//
// Side effects:
//   - Acquires a write lock and replaces the stored list for the session.
func (s *MemoryStore) Set(sessionID string, todos []Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.todos[sessionID] = todos
	return nil
}

// Get returns the current todo list for the given session.
//
// Expected:
//   - sessionID is a string identifying the session.
//
// Returns:
//   - The stored slice of Item values, or an empty slice when none exist.
//
// Side effects:
//   - Acquires a read lock while inspecting the internal map.
func (s *MemoryStore) Get(sessionID string) []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	todos, ok := s.todos[sessionID]
	if !ok {
		return []Item{}
	}
	result := make([]Item, len(todos))
	copy(result, todos)
	return result
}
