package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/google/uuid"
)

// ErrSessionNotFound is returned when a requested session does not exist.
var ErrSessionNotFound = errors.New("session not found")

// Message represents a single message in a session's conversation history.
type Message struct {
	ID        string
	Role      string
	Content   string
	AgentID   string
	Timestamp time.Time
}

// Session represents a planning session with conversation history,
// coordination store, and delegation chain status.
type Session struct {
	ID                string
	AgentID           string
	Status            string
	CoordinationStore *coordination.MemoryStore
	Messages          []Message
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Summary provides a lightweight view of a session for listing.
type Summary struct {
	ID           string
	AgentID      string
	Status       string
	CreatedAt    time.Time
	MessageCount int
}

// Manager handles session lifecycle and message routing.
type Manager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
	streamer streaming.Streamer
}

// NewManager creates a new session manager with the given streamer.
// Expected:
//   - streamer is a valid streaming implementation.
//
// Returns:
//   - A manager initialised with an empty session store.
//
// Side effects:
//   - Allocates the manager's internal session map.
func NewManager(streamer streaming.Streamer) *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		streamer: streamer,
	}
}

// CreateSession creates a new session for the given agent ID.
// Expected:
//   - agentID identifies the agent that owns the session.
//
// Returns:
//   - The newly created session when creation succeeds.
//   - An error if the session cannot be recorded.
//
// Side effects:
//   - Generates a new session identifier.
//   - Stores the session in memory.
func (m *Manager) CreateSession(agentID string) (*Session, error) {
	now := time.Now()
	sess := &Session{
		ID:                uuid.New().String(),
		AgentID:           agentID,
		Status:            "active",
		CoordinationStore: coordination.NewMemoryStore(),
		Messages:          make([]Message, 0),
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessions[sess.ID] = sess

	return sess, nil
}

// GetSession retrieves a session by ID.
// Expected:
//   - id identifies an existing session.
//
// Returns:
//   - The matching session when it exists.
//   - ErrSessionNotFound when no session matches the identifier.
//
// Side effects:
//   - Acquires a read lock while inspecting the session store.
func (m *Manager) GetSession(id string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sess, ok := m.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}

	return sess, nil
}

// ListSessions returns summaries of all sessions.
// Returns:
//   - A slice containing one summary per stored session.
//
// Side effects:
//   - Acquires a read lock while iterating over the session store.
func (m *Manager) ListSessions() []*Summary {
	m.mu.RLock()
	defer m.mu.RUnlock()

	summaries := make([]*Summary, 0, len(m.sessions))
	for _, sess := range m.sessions {
		summaries = append(summaries, &Summary{
			ID:           sess.ID,
			AgentID:      sess.AgentID,
			Status:       sess.Status,
			CreatedAt:    sess.CreatedAt,
			MessageCount: len(sess.Messages),
		})
	}

	return summaries
}

// SendMessage sends a message to the session and streams the response.
// Expected:
//   - ctx is valid for the lifetime of the streaming request.
//   - sessionID identifies an existing session.
//   - message contains the user's message content.
//
// Returns:
//   - A stream of provider chunks when the session exists.
//   - ErrSessionNotFound when no session matches the identifier.
//
// Side effects:
//   - Appends the user message to the session history.
//   - Updates the session timestamp.
//   - Delegates streaming to the configured provider.
func (m *Manager) SendMessage(ctx context.Context, sessionID string, message string) (<-chan provider.StreamChunk, error) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, ErrSessionNotFound
	}

	sess.Messages = append(sess.Messages, Message{
		ID:        uuid.New().String(),
		Role:      "user",
		Content:   message,
		AgentID:   sess.AgentID,
		Timestamp: time.Now(),
	})
	sess.UpdatedAt = time.Now()
	m.mu.Unlock()

	return m.streamer.Stream(ctx, sess.AgentID, message)
}

// CloseSession marks a session as completed.
// Expected:
//   - sessionID identifies an existing session.
//
// Returns:
//   - nil when the session is marked completed successfully.
//   - ErrSessionNotFound when no session matches the identifier.
//
// Side effects:
//   - Updates the session status in memory.
//   - Refreshes the session timestamp.
func (m *Manager) CloseSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}

	sess.Status = "completed"
	sess.UpdatedAt = time.Now()

	return nil
}
