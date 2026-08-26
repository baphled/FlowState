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
	// Apply atomically reads the current list, invokes fn with a copy,
	// and — if fn returns nil error — stores the result. The entire
	// read-fn-write cycle runs under the store's write lock, making
	// concurrent batched calls from executeToolCallBatch safe against
	// lost-update races. When fn returns an error, the store is left
	// unchanged and Apply returns the current list alongside the error.
	Apply(sessionID string, fn func(current []Item) (next []Item, err error)) ([]Item, error)
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

// Apply atomically reads, transforms, and writes the todo list for
// sessionID under a single write lock. See Store.Apply for the full
// contract.
//
// Expected: parameters for Apply.
// Returns: result of Apply.
// Side effects: None.
func (s *MemoryStore) Apply(sessionID string, fn func(current []Item) (next []Item, err error)) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.todos[sessionID]
	if current == nil {
		current = []Item{}
	}
	currentCopy := make([]Item, len(current))
	copy(currentCopy, current)

	result, err := fn(currentCopy)
	if err != nil {
		return currentCopy, err
	}
	s.todos[sessionID] = result
	return result, nil
}
