package notification

import (
	"sync"
	"time"
)

// Level defines the severity of a notification.
// Level represents the severity of a notification.
type Level string

// Level constants define notification severity values.
const (
	LevelInfo    Level = "info"
	LevelSuccess Level = "success"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
)

// Notification defines a transient notification entry.
type Notification struct {
	ID        string
	Title     string
	Message   string
	Level     Level
	Duration  time.Duration
	CreatedAt time.Time
}

// Manager defines notification lifecycle operations.
type Manager interface {
	// Add stores a notification in the manager.
	Add(notification Notification)
	// Dismiss removes the notification with the provided identifier.
	Dismiss(id string)
	// Active returns notifications whose duration has not elapsed yet.
	Active() []Notification
	// Expired returns notifications whose duration has elapsed.
	Expired() []Notification
}

// InMemoryManager stores notifications in memory with synchronised access.
type InMemoryManager struct {
	mu            sync.Mutex
	notifications []Notification
}

// NewInMemoryManager creates a new in-memory notification manager.
//
// Returns:
//   - An empty in-memory notification manager.
//
// Side effects:
//   - None.
func NewInMemoryManager() *InMemoryManager {
	return &InMemoryManager{}
}

// Add stores a notification in the manager.
//
// Expected:
//   - notification contains the notification to track.
//
// Returns:
//   - None.
//
// Side effects:
//   - Appends the notification to the manager's in-memory collection.
func (m *InMemoryManager) Add(notification Notification) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.notifications = append(m.notifications, notification)
}

// Dismiss removes the notification with the provided identifier.
//
// Expected:
//   - id identifies the notification to remove.
//
// Returns:
//   - None.
//
// Side effects:
//   - Removes matching notifications from the manager's in-memory collection.
func (m *InMemoryManager) Dismiss(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	filtered := m.notifications[:0]
	for _, notification := range m.notifications {
		if notification.ID != id {
			filtered = append(filtered, notification)
		}
	}
	m.notifications = filtered
}

// Active returns notifications whose duration has not elapsed yet.
//
// Expected:
//   - None.
//
// Returns:
//   - Notifications whose duration has not elapsed yet.
//
// Side effects:
//   - None.
func (m *InMemoryManager) Active() []Notification {
	return m.filter(func(notification Notification) bool {
		return time.Since(notification.CreatedAt) < notification.Duration
	})
}

// Expired returns notifications whose duration has elapsed.
//
// Expected:
//   - None.
//
// Returns:
//   - Notifications whose duration has elapsed.
//
// Side effects:
//   - None.
func (m *InMemoryManager) Expired() []Notification {
	return m.filter(func(notification Notification) bool {
		return time.Since(notification.CreatedAt) >= notification.Duration
	})
}

// filter returns notifications that satisfy the provided predicate.
//
// Expected:
//   - match determines whether a notification should be included.
//
// Returns:
//   - The notifications that satisfy the predicate.
//
// Side effects:
//   - None.
func (m *InMemoryManager) filter(match func(Notification) bool) []Notification {
	m.mu.Lock()
	defer m.mu.Unlock()

	filtered := make([]Notification, 0, len(m.notifications))
	for _, notification := range m.notifications {
		if match(notification) {
			filtered = append(filtered, notification)
		}
	}
	return filtered
}
