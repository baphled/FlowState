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

// Status represents the lifecycle state of a session.
type Status string

const (
	// StatusActive indicates the session is currently running.
	StatusActive Status = "active"
	// StatusCompleted indicates the session finished successfully.
	StatusCompleted Status = "completed"
	// StatusFailed indicates the session ended with an error.
	StatusFailed Status = "failed"
)

// Message represents a single message in a session's conversation history.
type Message struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	AgentID   string    `json:"agent_id"`
	Timestamp time.Time `json:"timestamp"`
}

// Session represents a planning session with conversation history,
// coordination store, and delegation chain status.
type Session struct {
	ID                string                    `json:"id"`
	AgentID           string                    `json:"agent_id"`
	Status            string                    `json:"status"`
	ParentID          string                    `json:"parent_id"`
	ParentSessionID   string                    `json:"parent_session_id"`
	CoordinationStore *coordination.MemoryStore `json:"coordination_store,omitempty"`
	Messages          []Message                 `json:"messages"`
	CreatedAt         time.Time                 `json:"created_at"`
	UpdatedAt         time.Time                 `json:"updated_at"`
}

// Summary provides a lightweight view of a session for listing.
type Summary struct {
	ID           string    `json:"id"`
	AgentID      string    `json:"agent_id"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	MessageCount int       `json:"message_count"`
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
		Status:            string(StatusActive),
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

// ChildSessions returns the direct child sessions for the given parent session identifier.
// Expected:
//   - parentID identifies the parent session to inspect.
//
// Returns:
//   - A slice containing each direct child session.
//   - A nil error when the lookup succeeds.
//
// Side effects:
//   - Acquires a read lock while scanning the session store.
func (m *Manager) ChildSessions(parentID string) ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	children := make([]*Session, 0)
	for _, sess := range m.sessions {
		if sess.ParentID == parentID || sess.ParentSessionID == parentID {
			children = append(children, sess)
		}
	}

	return children, nil
}

// SessionTree returns the root session and its descendants in depth-first order.
// Expected:
//   - rootID identifies the root session for the tree lookup.
//
// Returns:
//   - A slice containing the root session followed by its descendants.
//   - ErrSessionNotFound when the root session does not exist.
//
// Side effects:
//   - Acquires a read lock while traversing the session store.
func (m *Manager) SessionTree(rootID string) ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	root, ok := m.sessions[rootID]
	if !ok {
		return nil, ErrSessionNotFound
	}

	return sessionTree(m.sessions, root, make(map[string]bool)), nil
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

	ctx = context.WithValue(ctx, IDKey{}, sessionID)
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

	sess.Status = string(StatusCompleted)
	sess.UpdatedAt = time.Now()

	return nil
}

// SessionDepth returns the number of parent links between a session and the root.
// Expected:
//   - sessions contains the parent chain for the requested session.
//   - sessionID identifies the session whose depth should be calculated.
//
// Returns:
//   - The number of parent links between the session and the root.
//
// Side effects:
//   - None.
//
//revive:disable-next-line:exported
func SessionDepth(sessions map[string]*Session, sessionID string) int {
	return sessionDepth(sessions, sessionID, make(map[string]bool))
}

// sessionTree walks the session map in depth-first order.
// Expected:
//   - sessions contains the complete in-memory session map.
//   - root identifies the starting session for traversal.
//
// Returns:
//   - A depth-first slice rooted at the provided session.
//
// Side effects:
//   - None.
func sessionTree(sessions map[string]*Session, root *Session, visited map[string]bool) []*Session {
	if root == nil {
		return nil
	}
	if visited[root.ID] {
		return nil
	}

	visited[root.ID] = true
	result := []*Session{root}
	for _, sess := range sessions {
		if sess == nil || visited[sess.ID] {
			continue
		}
		if sess.ParentID == root.ID || sess.ParentSessionID == root.ID {
			result = append(result, sessionTree(sessions, sess, visited)...)
		}
	}

	return result
}

// sessionDepth walks the parent chain to calculate a session's depth.
// Expected:
//   - sessions contains the parent chain lookup data.
//   - sessionID identifies the session to inspect.
//
// Returns:
//   - The number of parent links between the session and the root.
//
// Side effects:
//   - None.
func sessionDepth(sessions map[string]*Session, sessionID string, visited map[string]bool) int {
	sess, ok := sessions[sessionID]
	if !ok || sess == nil {
		return 0
	}
	if visited[sessionID] {
		return 0
	}

	parentID := sess.ParentID
	if parentID == "" {
		parentID = sess.ParentSessionID
	}
	if parentID == "" {
		return 0
	}

	visited[sessionID] = true
	return 1 + sessionDepth(sessions, parentID, visited)
}
