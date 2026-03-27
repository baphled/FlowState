package toast

import (
	"sync"
	"time"
)

// Level defines the severity of a toast notification.
// Level represents the severity of a toast notification.
type Level string

// Level constants define toast severity values.
const (
	LevelInfo    Level = "info"
	LevelSuccess Level = "success"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
)

// Toast defines a transient notification entry.
type Toast struct {
	ID        string
	Title     string
	Message   string
	Level     Level
	Duration  time.Duration
	CreatedAt time.Time
}

// Manager defines toast lifecycle operations.
type Manager interface {
	// Add stores a toast in the manager.
	Add(toast Toast)
	// Dismiss removes the toast with the provided identifier.
	Dismiss(id string)
	// Active returns toasts whose duration has not elapsed yet.
	Active() []Toast
	// Expired returns toasts whose duration has elapsed.
	Expired() []Toast
}

// InMemoryManager stores toasts in memory with synchronised access.
type InMemoryManager struct {
	mu     sync.Mutex
	toasts []Toast
}

// NewInMemoryManager creates a new in-memory toast manager.
//
// Expected:
//   - None.
//
// Returns:
//   - A ready-to-use in-memory toast manager.
//
// Side effects:
//   - None.
func NewInMemoryManager() *InMemoryManager {
	return &InMemoryManager{}
}

// Add stores a toast in the manager.
//
// Expected:
//   - toast contains the notification to store.
//
// Returns:
//   - None.
//
// Side effects:
//   - Appends the toast to the manager's in-memory collection.
func (m *InMemoryManager) Add(toast Toast) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.toasts = append(m.toasts, toast)
}

// Dismiss removes the toast with the provided identifier.
//
// Expected:
//   - id identifies the toast to remove.
//
// Returns:
//   - None.
//
// Side effects:
//   - Removes matching toasts from the manager's in-memory collection.
func (m *InMemoryManager) Dismiss(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	filtered := m.toasts[:0]
	for _, toast := range m.toasts {
		if toast.ID != id {
			filtered = append(filtered, toast)
		}
	}
	m.toasts = filtered
}

// Active returns toasts whose duration has not elapsed yet.
//
// Expected:
//   - None.
//
// Returns:
//   - Toasts whose duration has not elapsed yet.
//
// Side effects:
//   - None.
func (m *InMemoryManager) Active() []Toast {
	return m.filter(func(toast Toast) bool {
		return time.Since(toast.CreatedAt) < toast.Duration
	})
}

// Expired returns toasts whose duration has elapsed.
//
// Expected:
//   - None.
//
// Returns:
//   - Toasts whose duration has elapsed.
//
// Side effects:
//   - None.
func (m *InMemoryManager) Expired() []Toast {
	return m.filter(func(toast Toast) bool {
		return time.Since(toast.CreatedAt) >= toast.Duration
	})
}

// filter returns toasts that satisfy the provided predicate.
func (m *InMemoryManager) filter(match func(Toast) bool) []Toast {
	m.mu.Lock()
	defer m.mu.Unlock()

	filtered := make([]Toast, 0, len(m.toasts))
	for _, toast := range m.toasts {
		if match(toast) {
			filtered = append(filtered, toast)
		}
	}
	return filtered
}
