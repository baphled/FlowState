package session

import (
	"context"
	"errors"
	"strings"
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
	ToolName  string    `json:"tool_name,omitempty"`
	ToolInput string    `json:"tool_input,omitempty"`
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
	Depth             int                       `json:"depth"`
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

// Recorder captures stream chunks for session recording.
type Recorder interface {
	// RecordChunk captures a stream chunk for the given session.
	RecordChunk(sessionID string, chunk provider.StreamChunk)
}

// Manager handles session lifecycle and message routing.
type Manager struct {
	sessions      map[string]*Session
	mu            sync.RWMutex
	streamer      streaming.Streamer
	notifications map[string][]streaming.CompletionNotificationEvent
	notifMu       sync.Mutex
	recorder      Recorder
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
		sessions:      make(map[string]*Session),
		streamer:      streamer,
		notifications: make(map[string][]streaming.CompletionNotificationEvent),
	}
}

// SetRecorder attaches an optional session recorder to the manager.
// When set, SendMessage tees stream chunks to the recorder alongside
// the caller's channel.
//
// Expected:
//   - r may be nil to disable recording.
//
// Returns: none.
// Side effects: updates the recorder reference.
func (m *Manager) SetRecorder(r Recorder) {
	m.recorder = r
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
		Depth:             0,
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

// CreateWithParent creates a new session as a child of the given parent session.
// Expected:
//   - parentID identifies an existing parent session.
//   - agentID identifies the agent for the new session.
//
// Returns:
//   - The newly created child session with ParentID and incremented Depth.
//   - An error if the parent does not exist or session cannot be recorded.
//
// Side effects:
//   - Generates a new session identifier.
//   - Stores the session in memory.
func (m *Manager) CreateWithParent(parentID string, agentID string) (*Session, error) {
	m.mu.RLock()
	parent, ok := m.sessions[parentID]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrSessionNotFound
	}
	now := time.Now()
	sess := &Session{
		ID:                uuid.New().String(),
		AgentID:           agentID,
		Status:            string(StatusActive),
		ParentID:          parentID,
		Depth:             parent.Depth + 1,
		CoordinationStore: coordination.NewMemoryStore(),
		Messages:          make([]Message, 0),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	m.mu.Lock()
	m.sessions[sess.ID] = sess
	m.mu.Unlock()
	return sess, nil
}

// GetRootSession walks up the parent chain to find the root session.
// Expected:
//   - sessionID identifies an existing session.
//
// Returns:
//   - The root session at the top of the parent chain.
//   - An error if the session or root does not exist.
//
// Side effects:
//   - Acquires read locks while traversing the session store.
func (m *Manager) GetRootSession(sessionID string) (*Session, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrSessionNotFound
	}
	for {
		parentID := sess.ParentID
		if parentID == "" {
			return sess, nil
		}
		m.mu.RLock()
		parent, ok := m.sessions[parentID]
		m.mu.RUnlock()
		if !ok {
			return nil, ErrSessionNotFound
		}
		sess = parent
	}
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

// extractPrimaryArg returns the primary display argument for a named tool call.
//
// Expected:
//   - name is a known tool identifier.
//   - args contains the tool call arguments map.
//
// Returns:
//   - The string value of the primary argument key, or empty string for unknown tools.
//
// Side effects:
//   - None.
func extractPrimaryArg(name string, args map[string]any) string {
	keys := map[string]string{
		"bash":       "command",
		"read":       "filePath",
		"write":      "filePath",
		"edit":       "filePath",
		"glob":       "pattern",
		"grep":       "pattern",
		"skill_load": "name",
	}
	key, ok := keys[name]
	if !ok {
		return ""
	}
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// appendSessionMessage safely appends a message to the named session's history.
//
// Expected:
//   - sessionID identifies an existing session.
//   - msg contains the message to append (ID and Timestamp will be assigned here).
//
// Returns:
//   - None.
//
// Side effects:
//   - Acquires the manager lock and appends msg to the session's Messages slice.
func (m *Manager) appendSessionMessage(sessionID string, msg Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return
	}
	msg.ID = uuid.New().String()
	msg.Timestamp = time.Now()
	sess.Messages = append(sess.Messages, msg)
}

// accumState holds mutable state for the stream accumulation goroutine.
type accumState struct {
	sessionID     string
	agentID       string
	contentBuf    strings.Builder
	lastToolName  string
	lastToolInput string
}

// processChunk handles a single stream chunk, updating state and persisting messages.
//
// Expected:
//   - chunk is the next chunk from the raw stream.
//
// Returns:
//   - None.
//
// Side effects:
//   - May call appendSessionMessage to persist accumulated content to session history.
//   - Mutates s.contentBuf, s.lastToolName, and s.lastToolInput in place.
func (m *Manager) processChunk(s *accumState, chunk provider.StreamChunk) {
	switch {
	case chunk.ToolCall != nil:
		if s.contentBuf.Len() > 0 {
			m.appendSessionMessage(s.sessionID, Message{
				Role:    "assistant",
				Content: s.contentBuf.String(),
				AgentID: s.agentID,
			})
			s.contentBuf.Reset()
		}
		s.lastToolName = chunk.ToolCall.Name
		s.lastToolInput = extractPrimaryArg(chunk.ToolCall.Name, chunk.ToolCall.Arguments)
	case chunk.ToolResult != nil:
		m.appendSessionMessage(s.sessionID, Message{
			Role:      "tool_result",
			Content:   chunk.ToolResult.Content,
			ToolName:  s.lastToolName,
			ToolInput: s.lastToolInput,
			AgentID:   s.agentID,
		})
	case chunk.Done:
		if s.contentBuf.Len() > 0 {
			m.appendSessionMessage(s.sessionID, Message{
				Role:    "assistant",
				Content: s.contentBuf.String(),
				AgentID: s.agentID,
			})
			s.contentBuf.Reset()
		}
	default:
		if chunk.Content != "" {
			s.contentBuf.WriteString(chunk.Content)
		}
	}
}

// accumulateStream wraps rawCh with a goroutine that records assistant and tool
// messages into session history while forwarding every chunk to the returned channel.
//
// Expected:
//   - sessionID and agentID are valid identifiers for the active session.
//   - rawCh is the stream channel returned by the streamer.
//
// Returns:
//   - A new channel that receives the same chunks as rawCh.
//
// Side effects:
//   - Spawns a goroutine that appends messages to the session via appendSessionMessage.
func (m *Manager) accumulateStream(sessionID, agentID string, rawCh <-chan provider.StreamChunk) <-chan provider.StreamChunk {
	accumCh := make(chan provider.StreamChunk, 64)
	go func() {
		defer close(accumCh)
		s := &accumState{sessionID: sessionID, agentID: agentID}
		for chunk := range rawCh {
			m.processChunk(s, chunk)
			accumCh <- chunk
		}
	}()
	return accumCh
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
//   - Accumulates assistant and tool messages from the stream into session history.
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
	agentID := sess.AgentID
	m.mu.Unlock()

	ctx = context.WithValue(ctx, IDKey{}, sessionID)
	rawCh, err := m.streamer.Stream(ctx, agentID, message)
	if err != nil {
		return nil, err
	}

	accumCh := m.accumulateStream(sessionID, agentID, rawCh)

	if m.recorder != nil {
		teedCh := make(chan provider.StreamChunk, 64)
		go func() {
			defer close(teedCh)
			for chunk := range accumCh {
				m.recorder.RecordChunk(sessionID, chunk)
				teedCh <- chunk
			}
		}()
		return teedCh, nil
	}

	return accumCh, nil
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

// InjectNotification stores a completion notification for the given session.
// Expected:
//   - sessionID is non-empty.
//   - notification is a valid CompletionNotificationEvent.
//
// Returns:
//   - An error if sessionID is empty.
//   - nil when injection succeeds.
//
// Side effects:
//   - Appends notification to the in-memory notification store for sessionID.
func (m *Manager) InjectNotification(sessionID string, notification streaming.CompletionNotificationEvent) error {
	if sessionID == "" {
		return errors.New("session ID must not be empty")
	}

	m.notifMu.Lock()
	defer m.notifMu.Unlock()
	m.notifications[sessionID] = append(m.notifications[sessionID], notification)
	return nil
}

// GetNotifications retrieves and clears pending notifications for the given session.
// Expected:
//   - sessionID is non-empty.
//
// Returns:
//   - A slice of pending notifications (empty slice if none exist).
//   - An error if sessionID is empty.
//
// Side effects:
//   - Clears the notification queue for sessionID after retrieval.
func (m *Manager) GetNotifications(sessionID string) ([]streaming.CompletionNotificationEvent, error) {
	if sessionID == "" {
		return nil, errors.New("session ID must not be empty")
	}

	m.notifMu.Lock()
	defer m.notifMu.Unlock()
	notifications := m.notifications[sessionID]
	delete(m.notifications, sessionID)
	if notifications == nil {
		return []streaming.CompletionNotificationEvent{}, nil
	}
	return notifications, nil
}

// Depth returns the number of parent links between a session and the root.
// Expected:
//   - sessions contains the parent chain for the requested session.
//   - sessionID identifies the session whose depth should be calculated.
//
// Returns:
//   - The number of parent links between the session and the root.
//
// Side effects:
//   - None.
func Depth(sessions map[string]*Session, sessionID string) int {
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
