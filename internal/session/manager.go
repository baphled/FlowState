package session

import (
	"context"
	"errors"
	"sort"
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
	// StatusAbandoned indicates the session was reaped by the boot-time
	// orphan sweep — it was still "active" on disk after a parent-process
	// crash that never fired a seal event. Distinct from StatusCompleted
	// so forensic tools and the UI can tell process-crash orphans apart
	// from sessions that ran cleanly to completion. Distinct from
	// StatusFailed because no upstream error was observed; the parent
	// just disappeared.
	StatusAbandoned Status = "abandoned"
)

// DefaultOrphanGrace is the default age threshold the boot-time orphan
// sweep applies to restored sessions still marked "active". A session
// whose UpdatedAt is older than this window is treated as a
// parent-crash orphan and reaped to StatusAbandoned. The window must be
// long enough that a parent restart does not erroneously reap children
// the parent is about to re-attach to (the persistence path bumps
// UpdatedAt on every message append, so any session that has streamed
// in the last 30 minutes is by definition still alive).
const DefaultOrphanGrace = 30 * time.Minute

// Message represents a single message in a session's conversation history.
//
// ModelName / ProviderName carry the (model, provider) pair that produced
// an assistant turn, stamped by the session accumulator at flush time from
// the engine-tagged StreamChunk. The pair is persisted on the message so
// per-turn attribution survives server restart and provider failover —
// the chip in the activity indicator can display "produced by glm-4.6"
// after a reload, and a future per-bubble badge has the data it needs
// without re-reading the session-level CurrentModelID/CurrentProviderID
// (which only tracks the *current* selection, not historical turns).
type Message struct {
	ID           string `json:"id"`
	Role         string `json:"role"`
	Content      string `json:"content"`
	AgentID      string `json:"agentId,omitempty"`
	ToolName     string `json:"toolName,omitempty"`
	ToolInput    string `json:"toolInput,omitempty"`
	TargetAgent  string `json:"targetAgent,omitempty"`
	ChainID      string `json:"chainId,omitempty"`
	ToolCalls    int    `json:"toolCalls,omitempty"`
	LastTool     string `json:"lastTool,omitempty"`
	Status       string `json:"status,omitempty"`
	ModelName    string `json:"modelName,omitempty"`
	ProviderName string `json:"providerName,omitempty"`
	// ThinkingBlocks carries the per-block thinking content produced
	// by Anthropic extended thinking (signed and redacted variants).
	// Persisted on assistant messages so that a session reload can
	// reconstruct the exact thinking blocks that must be replayed on
	// subsequent turns. Without these, Anthropic silently disables
	// extended thinking on turn 2+. Empty for non-thinking turns and
	// for providers that do not produce thinking blocks.
	ThinkingBlocks []provider.ThinkingBlock `json:"thinkingBlocks,omitempty"`
	// StopReason is the upstream provider's terminal stop reason for
	// the turn that produced this message. Empty when unknown. The
	// `refusal` and `model_context_window_exceeded` values (Claude 4+
	// additions) flow through here so consumers can distinguish them
	// from a normal `end_turn`.
	StopReason string    `json:"stopReason,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// Session represents a planning session with conversation history,
// coordination store, and delegation chain status.
type Session struct {
	ID                string                    `json:"id"`
	AgentID           string                    `json:"agent_id"`
	CurrentAgentID    string                    `json:"current_agent_id,omitempty"` // actively selected agent; overrides AgentID when set
	CurrentModelID    string                    `json:"current_model_id,omitempty"`
	CurrentProviderID string                    `json:"current_provider_id,omitempty"`
	Status            string                    `json:"status"`
	ParentID          string                    `json:"parent_id"`
	ParentSessionID   string                    `json:"parent_session_id"`
	Depth             int                       `json:"depth"`
	CoordinationStore *coordination.MemoryStore `json:"coordination_store,omitempty"`
	Messages          []Message                 `json:"messages"`
	CreatedAt         time.Time                 `json:"created_at"`
	UpdatedAt         time.Time                 `json:"updated_at"`
	// EmbeddingModel records the embedding model that was active at session
	// creation time. Frozen at creation — a mid-session config flip MUST NOT
	// rewrite this field, otherwise the diagnostic gets erased and a Recall
	// silent-zero failure (empty results from a dimension mismatch between
	// the configured model and the persisted vectors) becomes invisible.
	// Empty for legacy sessions persisted before this field existed; that
	// absence is the condition the diagnostic exists to flag. See
	// `Bug Fixes/Recall Diagnostic - Embedding Model Stamp (May 2026).md`
	// in the FlowState vault and memory entry
	// `project_flowstate_recall_silent_zero_failure`.
	EmbeddingModel string `json:"embedding_model,omitempty"`
	// ChainID stamps the delegation coordination chain identifier on a
	// child session at spawn time. Empty for top-level (non-delegated)
	// sessions, populated for any session created via
	// CreateWithParentAndChain (the engine spawn path).
	//
	// Today's commit a488b858 closed the live-click sibling-confusion bug
	// on the Vue inline delegation card by carrying a runtime
	// (chainId → childSessionId) map populated from SwarmEvents in the
	// chatStore. That map is empty after a hard reload — FlowState does
	// not replay swarm events on reconnect — so a click on a historical
	// delegation card fell back to the agent-id "most-recent child"
	// resolver and the sibling-confusion bug re-appeared.
	//
	// Persisting ChainID on Session closes the cold-reload hole: the
	// frontend rebuilds the runtime map from GET /api/v1/sessions on
	// load, using the chain_id field on each summary. Omitted from the
	// JSON when empty so legacy sidecars stay byte-identical and the
	// field's presence remains a positive signal of "this session was
	// spawned via a chain". See Bug Fixes/Chat Sibling Confusion
	// (May 2026) in the FlowState vault for the full chain of fixes.
	ChainID string `json:"chain_id,omitempty"`
}

// Summary provides a lightweight view of a session for listing.
//
// ParentID is empty for top-level sessions and carries the parent session
// identifier for delegated child sessions. The Vue SessionSwitcher filters
// `!parentId` to keep child sessions out of the recents dropdown — the
// delegation panel surfaces them separately. The projected value prefers
// the canonical `Session.ParentID` and falls back to the legacy
// `Session.ParentSessionID` so restored or migrated sessions retain a
// stable hierarchy. The JSON tag is camelCase to match the existing
// frontend `SessionSummary` contract.
//
// IsStreaming is populated by the API layer (not by the manager) when the
// session broker reports an active Publish for this session. The field
// defaults to false; callers that have broker context set it after listing.
type Summary struct {
	ID                string    `json:"id"`
	AgentID           string    `json:"agentId"`
	CurrentAgentID    string    `json:"currentAgentId,omitempty"`
	CurrentModelID    string    `json:"currentModelId,omitempty"`
	CurrentProviderID string    `json:"currentProviderId,omitempty"`
	ParentID          string    `json:"parentId,omitempty"`
	// ChainID surfaces the delegation coordination chain identifier so the
	// Vue chatStore can rebuild its (chainId → childSessionId) map from
	// the session list on cold load — closing the reload-hole left by
	// a488b858 where SwarmEvents do not replay on reconnect. Omitted when
	// empty so root sessions stay byte-identical to their pre-field shape.
	ChainID      string    `json:"chainId,omitempty"`
	Title        string    `json:"title"`
	IsStreaming  bool      `json:"isStreaming"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	MessageCount int       `json:"messageCount"`
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
	sessionsDir   string
	// orphanGrace is the age threshold the boot-time orphan sweep
	// applies inside RestoreSessions. Zero means use DefaultOrphanGrace.
	// Negative means "disable the sweep" — exposed so tests and
	// operators can opt out without rewriting the call site.
	orphanGrace time.Duration

	// embeddingModel is the diagnostic value the manager stamps on
	// every newly-created session. Set once at app wiring time from
	// cfg.ResolvedEmbeddingModel(). Empty when the manager was never
	// configured (tests, ephemeral runs); newly-created sessions will
	// carry an empty EmbeddingModel rather than a synthesised default,
	// so a missing value is always traceable to "manager wasn't told"
	// rather than a silent fallback. The field is read under m.mu.
	embeddingModel string

	// attachments is the per-manager content-hashed file store for
	// user-uploaded chat attachments. Lazily constructed via
	// EnsureAttachmentStore on first access (typically the API server's
	// upload-endpoint handler). Nil until first access; callers should
	// route through AttachmentStore() so the lazy-init synchronises.
	attachments *AttachmentStore

	// persistFn overrides the default PersistSession implementation.
	// Nil means use PersistSession. Only set in tests via export_test.go.
	persistFn func(dir string, sess *Session) error

	// inflightMu guards the inflight map.
	inflightMu sync.Mutex
	// inflight maps session IDs to their context cancel functions.
	// When a SendMessage turn starts, a cancel function is registered here.
	// CancelInflight looks up the cancel and fires it; the turn's goroutine
	// deregisters on exit.
	inflight map[string]context.CancelFunc
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
		inflight:      make(map[string]context.CancelFunc),
	}
}

// MarkEndedFromEvent flips the matching session's status to
// StatusCompleted in response to an external "session.ended" event.
// Idempotent and status-precedence-aware: failed > completed > active —
// a previously-failed session is NOT downgraded to completed when an
// end event arrives later. Unknown session IDs are silently ignored
// (events for foreign sessions, or for sessions that pre-date the
// manager's restart, simply have nothing to do here).
//
// This is the bus-driven counterpart to CloseSession. Wire-up at the
// app level: subscribe to the event bus, type-assert the published
// payload to *events.SessionEvent, and forward .Data.SessionID here.
// Keeping the type assertion at the call site means this package does
// NOT need to import plugin/events.
//
// Expected:
//   - sessionID is the session whose status should flip. Empty input
//     is a no-op.
//
// Side effects:
//   - Updates the matching session's Status and UpdatedAt under write
//     lock when a flip applies.
func (m *Manager) MarkEndedFromEvent(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[sessionID]
	if !ok {
		return
	}
	if sess.Status == string(StatusFailed) ||
		sess.Status == string(StatusCompleted) ||
		sess.Status == string(StatusAbandoned) {
		return
	}
	sess.Status = string(StatusCompleted)
	sess.UpdatedAt = time.Now()
	// Bug fix (May 2026 — Session Seal Persistence Hole): the in-memory
	// status flip above never reached disk, so the .meta.json sidecar
	// stayed at "active" and the sealed child re-loaded as active after
	// restart. The bus-driven seal must persist alongside the message-
	// append paths that already use this helper.
	m.persistLocked(sess)
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

// SetSessionsDir enables on-write persistence of session metadata and
// messages to the given directory. An empty dir disables persistence,
// which is the default for tests and ephemeral runs.
//
// Expected:
//   - dir is an absolute path to a writable directory, or empty to disable.
//
// Side effects:
//   - Subsequent message appends will write the session's *.meta.json
//     file under dir so chat history survives a restart.
func (m *Manager) SetSessionsDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionsDir = dir
	// Re-construct the attachment store so it rebinds to the new
	// rootDir; the lazy-init below will rebuild on next access. A
	// previously-loaded in-memory index is dropped — callers that
	// flip SetSessionsDir mid-life are signalling "discard prior
	// state". The on-disk subtree under the old dir is left in place.
	m.attachments = nil
}

// AttachmentStore returns the manager's per-session attachment store,
// lazily constructing it on first access against the manager's
// configured sessionsDir. When sessionsDir is empty, the store is
// still returned with persistence disabled (Put will fail with a
// clear error) so callers can hold a nil-safe reference.
//
// Goroutine-safe via the manager's write lock during init; subsequent
// reads are lock-free since the store's mutex guards its own state.
func (m *Manager) AttachmentStore() *AttachmentStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.attachments == nil {
		m.attachments = NewAttachmentStore(m.sessionsDir)
	}
	return m.attachments
}

// SetEmbeddingModel records the embedding-model name the manager
// will stamp on every newly-created session via its EmbeddingModel
// field. Idempotent — safe to call multiple times. The stamp is
// applied at session-creation time and is frozen on each session
// thereafter; a later SetEmbeddingModel call only affects sessions
// created after the call, never existing ones. Empty input clears
// the configured value (subsequent sessions then have an empty
// EmbeddingModel, which the load path treats as the legacy "no
// diagnostic available" condition).
//
// Wire this from the app layer once, immediately after NewManager,
// using cfg.ResolvedEmbeddingModel() so the (qdrant, ollama,
// embedding-model) tuple stamped on a session matches the tuple the
// recall pipeline was built against. Without this stamp a Recall
// silent-zero failure (empty results from a dimension mismatch
// between the configured model and the persisted vectors) is
// undiagnosable from the session sidecar — see vault note
// "Recall Diagnostic - Embedding Model Stamp (May 2026)" and
// memory entry `project_flowstate_recall_silent_zero_failure`.
//
// Expected:
//   - model is the embedding model identifier; empty disables the stamp.
//
// Side effects:
//   - Updates the manager's embeddingModel field under the write lock.
func (m *Manager) SetEmbeddingModel(model string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.embeddingModel = model
}

// SetOrphanGrace overrides the age threshold the boot-time orphan
// sweep uses inside RestoreSessions. Zero restores DefaultOrphanGrace.
// A negative duration disables the sweep entirely (preserved for
// operators who would rather see ghost-active children than risk a
// false-positive reap during planned migrations).
//
// Expected:
//   - d is the new threshold; zero means "use the default", negative
//     means "disable".
//
// Side effects:
//   - Updates the manager's orphan-grace field under the write lock.
func (m *Manager) SetOrphanGrace(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orphanGrace = d
}

// persistLocked writes the session to disk when sessionsDir is set.
// The caller MUST hold m.mu (read or write). Errors are swallowed to
// avoid blocking the message hot path; persistence is best-effort.
func (m *Manager) persistLocked(sess *Session) {
	if m.sessionsDir == "" || sess == nil {
		return
	}
	fn := m.persistFn
	if fn == nil {
		fn = PersistSession
	}
	_ = fn(m.sessionsDir, sess)
}

// EnsureSession is an alias for RegisterSession that matches the interface name
// used by TUI chat intent. Idempotent — a no-op when the session already exists.
//
// Expected:
//   - sessionID is the externally generated session identifier.
//   - agentID identifies the agent that owns the session.
//
// Returns:
//   - None.
//
// Side effects:
//   - Stores a new session in memory when sessionID is not already present.
func (m *Manager) EnsureSession(sessionID, agentID string) {
	m.RegisterSession(sessionID, agentID)
}

// RegisterSession upserts a session with the given ID into the in-memory store.
// When a session with the same ID already exists, the call is a no-op.
// This allows the TUI's main session (whose ID is determined externally) to be
// registered before any child delegation fires.
//
// Expected:
//   - id is the externally generated session identifier.
//   - agentID identifies the agent that owns the session.
//
// Returns:
//   - None.
//
// Side effects:
//   - Stores a new session in memory when id is not already present.
func (m *Manager) RegisterSession(id, agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[id]; ok {
		return
	}
	now := time.Now()
	m.sessions[id] = &Session{
		ID:                id,
		AgentID:           agentID,
		Status:            string(StatusActive),
		Depth:             0,
		CoordinationStore: coordination.NewMemoryStore(),
		Messages:          make([]Message, 0),
		CreatedAt:         now,
		UpdatedAt:         now,
		// Stamp the configured embedding model so a Recall silent-zero
		// failure on this session is later diagnosable from the .meta.json
		// sidecar. See SetEmbeddingModel for rationale.
		EmbeddingModel: m.embeddingModel,
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
	return m.CreateSessionWithDefaults(agentID, "", "")
}

// CreateSessionWithDefaults creates a new session pre-populated with a
// default (provider, model) pair so the persistent model chip in the chat
// activity indicator can render immediately on a brand-new session, before
// the user has selected a model and before the first assistant turn has
// streamed.
//
// Expected:
//   - agentID identifies the agent that owns the session.
//   - providerID is the default provider identifier (may be empty).
//   - modelID is the default model identifier (may be empty).
//
// Returns:
//   - The newly created session, with CurrentProviderID and CurrentModelID
//     populated from the supplied defaults.
//   - An error if the session cannot be recorded.
//
// Side effects:
//   - Generates a new session identifier.
//   - Stores the session in memory.
//   - Persists the session to the configured sessions dir when one is set,
//     so a process restart between create and first message preserves the
//     defaults (the .meta.json sidecar carries them).
//
// Empty defaults are accepted and result in the same shape CreateSession
// produced before this method existed — used by the legacy CLI and tests
// that don't care about the chip.
func (m *Manager) CreateSessionWithDefaults(agentID, providerID, modelID string) (*Session, error) {
	now := time.Now()
	// Read the configured embedding-model snapshot under the lock so a
	// concurrent SetEmbeddingModel cannot tear the field. The value is
	// then stamped on the new session and frozen — subsequent
	// SetEmbeddingModel calls will not mutate this session.
	m.mu.Lock()
	embeddingModel := m.embeddingModel
	sess := &Session{
		ID:                uuid.New().String(),
		AgentID:           agentID,
		CurrentProviderID: providerID,
		CurrentModelID:    modelID,
		Status:            string(StatusActive),
		Depth:             0,
		CoordinationStore: coordination.NewMemoryStore(),
		Messages:          make([]Message, 0),
		CreatedAt:         now,
		UpdatedAt:         now,
		EmbeddingModel:    embeddingModel,
	}

	m.sessions[sess.ID] = sess
	sessionsDir := m.sessionsDir
	persistFn := m.persistFn
	var snapshot *Session
	if sessionsDir != "" && (providerID != "" || modelID != "") {
		snap := *sess
		msgs := make([]Message, len(sess.Messages))
		copy(msgs, sess.Messages)
		snap.Messages = msgs
		snapshot = &snap
	}
	m.mu.Unlock()

	if snapshot != nil {
		fn := persistFn
		if fn == nil {
			fn = PersistSession
		}
		_ = fn(sessionsDir, snapshot)
	}

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
//
// CreateWithParent is preserved for callers that have no coordination chain
// in scope; it routes through CreateWithParentAndChain with an empty chainID
// so the in-memory and on-disk shape is identical to a sibling created via
// the chain-aware path. New call sites that DO have a chainID (the engine
// spawn path) should call CreateWithParentAndChain directly so the chainID
// is stamped on the child session for cold-reload reconstruction.
func (m *Manager) CreateWithParent(parentID string, agentID string) (*Session, error) {
	return m.CreateWithParentAndChain(parentID, agentID, "")
}

// CreateWithParentAndChain creates a new child session under parentID and
// stamps chainID on the resulting Session.
//
// The engine delegation spawn path (executeSync → resolveOrCreateSession →
// createChildSession) calls this with the authoritative chainID in flight
// so the persisted child session can be linked back to its delegation
// event after a cold reload. The Vue chatStore reads chain_id from the
// session list (GET /api/v1/sessions) on load to rebuild the runtime
// (chainId → childSessionId) map that disambiguates sibling delegations
// on inline-card click.
//
// Expected:
//   - parentID identifies an existing parent session.
//   - agentID identifies the agent for the new session.
//   - chainID is the delegation coordination chain identifier (may be empty
//     for callers that have no chain in scope; the legacy
//     CreateWithParent path routes through here with chainID="").
//
// Returns:
//   - The newly created child session with ParentID, ChainID, and incremented
//     Depth.
//   - ErrSessionNotFound if the parent is not registered.
//
// Side effects:
//   - Generates a new session identifier.
//   - Stores the session in memory.
func (m *Manager) CreateWithParentAndChain(parentID, agentID, chainID string) (*Session, error) {
	m.mu.RLock()
	parent, ok := m.sessions[parentID]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrSessionNotFound
	}
	now := time.Now()
	m.mu.Lock()
	embeddingModel := m.embeddingModel
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
		EmbeddingModel:    embeddingModel,
		ChainID:           chainID,
	}
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

// LastMessageRole returns the role of the session's most recent message and
// whether the session has any messages, performing the read under the
// manager's RLock so concurrent SendMessage writes cannot race the slice
// header.
//
// This accessor exists because returning *Session from GetSession leaks a
// pointer past the lock boundary: callers that read sess.Messages outside
// the lock race with SendMessage's append. Specifically, the SSE
// fast-path in handleSessionStream needs only "did the last turn close"
// to decide whether to emit [DONE] immediately; that question is
// answered by the role of the final message and is safe to project
// while holding RLock.
//
// Expected:
//   - id identifies an existing session.
//
// Returns:
//   - role: the role string of Messages[len-1] when present, empty otherwise.
//   - hasMessages: true when len(Messages) > 0.
//   - ErrSessionNotFound when no session matches the identifier.
//
// Side effects:
//   - Acquires the manager's RLock for the duration of the projection.
//
// Concurrency:
//   - Only acquires RLock; never upgrades to WLock. Safe to call from
//     code paths that may themselves be invoked under the manager lock
//     in future without triggering the RWMutex upgrade deadlock pattern
//     (see the engine buildContextWindow bug-fix note for the canonical
//     anti-pattern).
func (m *Manager) LastMessageRole(id string) (role string, hasMessages bool, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sess, ok := m.sessions[id]
	if !ok {
		return "", false, ErrSessionNotFound
	}
	if len(sess.Messages) == 0 {
		return "", false, nil
	}
	return sess.Messages[len(sess.Messages)-1].Role, true, nil
}

// SnapshotSession returns a value-type snapshot of the named session,
// suitable for projecting into wire-format DTOs (e.g. NewSessionResponse)
// without leaking the manager's *Session pointer past the lock boundary.
//
// The Messages slice is deep-copied so callers can read len/index/range
// it after the manager's RLock is released without racing concurrent
// SendMessage appends. All scalar fields (ID, AgentID, Status,
// CurrentAgentID, CurrentModelID, CurrentProviderID, CreatedAt,
// UpdatedAt, ParentID, ParentSessionID, Depth) are captured by value
// under RLock, so concurrent UpdateSessionAgent / UpdateSessionModel
// writers cannot tear those reads either.
//
// CoordinationStore is intentionally left aliased — it is not part of
// the wire shape produced by NewSessionResponse, callers do not write
// to it from this path, and deep-copying the store would defeat its
// shared-by-design semantics. If a future caller projects it into a
// wire shape, that caller must add its own snapshot boundary.
//
// Expected:
//   - id identifies an existing session.
//
// Returns:
//   - A Session value (not a pointer) with its Messages slice deep-copied.
//   - ErrSessionNotFound when no session matches the identifier.
//
// Side effects:
//   - Acquires the manager's RLock for the duration of the snapshot.
//
// Concurrency:
//   - Only acquires RLock; never upgrades to WLock. Safe to call from
//     code paths that hold no manager lock. Callers must NOT pass the
//     returned snapshot back into mutating Manager methods — the
//     snapshot is decoupled from the live session and writes against
//     it would be silently lost.
func (m *Manager) SnapshotSession(id string) (Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sess, ok := m.sessions[id]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	snap := *sess
	if len(sess.Messages) > 0 {
		snap.Messages = make([]Message, len(sess.Messages))
		copy(snap.Messages, sess.Messages)
	} else {
		snap.Messages = nil
	}
	return snap, nil
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
		updatedAt := sess.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = sess.CreatedAt
		}
		// Prefer the canonical ParentID; fall back to the legacy
		// ParentSessionID so restored sessions persisted before the
		// rename retain a parent link in the projected summary. This
		// mirrors the precedence used by ChildSessions and Depth so a
		// single rule governs which sessions are "child" sessions.
		parentID := sess.ParentID
		if parentID == "" {
			parentID = sess.ParentSessionID
		}
		summaries = append(summaries, &Summary{
			ID:                sess.ID,
			AgentID:           sess.AgentID,
			CurrentAgentID:    sess.CurrentAgentID,
			CurrentModelID:    sess.CurrentModelID,
			CurrentProviderID: sess.CurrentProviderID,
			ParentID:          parentID,
			ChainID:           sess.ChainID,
			Title:             deriveSummaryTitle(sess),
			CreatedAt:         sess.CreatedAt,
			UpdatedAt:         updatedAt,
			MessageCount:      len(sess.Messages),
		})
	}

	return summaries
}

// deriveSummaryTitle returns a non-empty human-readable title for a session.
// It prefers the first user message content (truncated) and falls back to a
// short identifier derived from the session ID so the frontend never sees an
// empty title.
func deriveSummaryTitle(sess *Session) string {
	const maxTitleLen = 60
	for _, msg := range sess.Messages {
		if msg.Role != "user" {
			continue
		}
		trimmed := strings.TrimSpace(msg.Content)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > maxTitleLen {
			return trimmed[:maxTitleLen] + "…"
		}
		return trimmed
	}
	short := sess.ID
	if len(short) > 8 {
		short = short[:8]
	}
	if short == "" {
		return "Untitled session"
	}
	return "Session " + short
}

// RestoreSessions registers persisted sessions into the manager.
// Sessions that already exist (same ID) are skipped.
//
// After populating the in-memory map, RestoreSessions runs the
// boot-time orphan sweep: any restored session whose Status is still
// "active" AND whose UpdatedAt is older than the configured grace
// window is sealed as StatusAbandoned and persisted via the same
// locked-persist helper the seal sites use. This is the backstop for
// the persistence-hole fix — when the parent process crashes
// mid-flight, no seal event ever fires for child sessions, so without
// the sweep they remain "active" on disk forever and the orchestrator
// re-loads them as ghosts on every restart. The sweep targets the
// root cause: untouched-since-crash sessions stay active on disk.
//
// Expected:
//   - sessions is a slice of Sessions loaded from disk persistence.
//
// Returns:
//   - None.
//
// Side effects:
//   - Adds each new session to the in-memory store under its ID.
//   - Promotes stale-active restored sessions to StatusAbandoned and
//     flushes the change to the .meta.json sidecar via persistLocked
//     when sessionsDir is configured.
func (m *Manager) RestoreSessions(sessions []*Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sess := range sessions {
		if _, ok := m.sessions[sess.ID]; !ok {
			if sess.UpdatedAt.IsZero() {
				sess.UpdatedAt = sess.CreatedAt
			}
			if sess.Messages == nil {
				sess.Messages = make([]Message, 0)
			}
			m.sessions[sess.ID] = sess
		}
	}
	m.sweepOrphansLocked()
}

// sweepOrphansLocked promotes restored sessions still marked active
// past the orphan-grace threshold to StatusAbandoned, persisting via
// the locked helper. Caller MUST hold m.mu (write). A negative
// orphanGrace disables the sweep entirely so operators can opt out.
//
// Expected:
//   - m.mu is held for write.
//
// Side effects:
//   - Mutates Status and UpdatedAt for any session that meets the
//     stale-active criteria.
//   - Writes each swept session's .meta.json sidecar via persistLocked
//     when sessionsDir is configured.
func (m *Manager) sweepOrphansLocked() {
	grace := m.orphanGrace
	if grace == 0 {
		grace = DefaultOrphanGrace
	}
	if grace < 0 {
		return
	}
	cutoff := time.Now().Add(-grace)
	for _, sess := range m.sessions {
		if sess == nil {
			continue
		}
		if sess.Status != string(StatusActive) {
			continue
		}
		if sess.UpdatedAt.After(cutoff) {
			continue
		}
		// Promote status to abandoned but PRESERVE UpdatedAt — the
		// pre-sweep mtime is the forensic signal "this session went
		// stale at time T" (i.e. roughly when the parent crashed).
		// Stamping time.Now() here would overwrite that timeline with
		// "we discovered the orphan at boot time T+N" and destroy
		// the answer operators need to "when did the parent crash?".
		// The sweep is a status promotion, not a fresh activity
		// event, so the mtime stays.
		sess.Status = string(StatusAbandoned)
		m.persistLocked(sess)
	}
}

// AllSessions returns every session that has a parent, regardless of which parent session spawned it.
//
// Returns:
//   - A slice containing all sessions that carry a non-empty ParentID,
//     ordered by CreatedAt (oldest first). Stable across calls — Go map
//     iteration is non-deterministic, so a deterministic sort is required
//     for consumers that step through sessions sequentially (e.g. the
//     delegation picker's left/right arrow navigation).
//   - A nil error on success.
//
// Side effects:
//   - Acquires a read lock while scanning the session store.
func (m *Manager) AllSessions() ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		if sess.ParentID != "" {
			result = append(result, sess)
		}
	}
	sortSessionsByCreatedAt(result)

	return result, nil
}

// ChildSessions returns the direct child sessions for the given parent session identifier.
// Expected:
//   - parentID identifies the parent session to inspect.
//
// Returns:
//   - A slice containing each direct child session, ordered by CreatedAt
//     (oldest first). Same rationale as AllSessions: stepping through
//     children with arrow keys requires a deterministic order.
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
	sortSessionsByCreatedAt(children)

	return children, nil
}

// sortSessionsByCreatedAt orders sessions oldest-first. Ties on CreatedAt
// (down-to-the-nanosecond identical timestamps, possible when a parent
// fans out parallel delegations in one tick) break by session ID so the
// order remains stable across calls. Without this tiebreaker a back-to-
// back delegation pair could swap positions between two AllSessions
// calls — exactly the symptom the user reported as "delegated agent
// listing doesn't honour creation order".
func sortSessionsByCreatedAt(sessions []*Session) {
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].CreatedAt.Equal(sessions[j].CreatedAt) {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].CreatedAt.Before(sessions[j].CreatedAt)
	})
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
//   - Acquires the manager write lock only for the in-memory append; releases
//     it before calling persist so that concurrent GetSession readers are not
//     blocked for the duration of disk I/O.
func (m *Manager) appendSessionMessage(sessionID string, msg Message) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return
	}
	msg.ID = uuid.New().String()
	msg.Timestamp = time.Now()
	sess.Messages = append(sess.Messages, msg)

	// When the engine stamps a (model, provider) onto an assistant message
	// and the session-level fields are stale (empty, or a previously failed
	// candidate that the failover hook has since replaced), promote the
	// pair onto the session. This keeps the persistent chip ("on glm-4.6 ·
	// zai") aligned with the model that actually produced the most recent
	// turn, without requiring an explicit UpdateSessionModel PATCH from
	// the client. The check is restricted to assistant turns so tool_call
	// / tool_result / delegation messages — which never carry these
	// fields anyway — cannot accidentally clear the pair.
	if msg.Role == "assistant" {
		if msg.ModelName != "" && sess.CurrentModelID != msg.ModelName {
			sess.CurrentModelID = msg.ModelName
		}
		if msg.ProviderName != "" && sess.CurrentProviderID != msg.ProviderName {
			sess.CurrentProviderID = msg.ProviderName
		}
	}

	// Snapshot the fields needed for persistence under the lock, then release
	// before doing I/O so GetSession readers are not blocked by disk writes.
	sessionsDir := m.sessionsDir
	persistFn := m.persistFn
	var snapshot *Session
	if sessionsDir != "" {
		snap := *sess
		msgs := make([]Message, len(sess.Messages))
		copy(msgs, sess.Messages)
		snap.Messages = msgs
		snapshot = &snap
	}
	m.mu.Unlock()

	if snapshot != nil {
		fn := persistFn
		if fn == nil {
			fn = PersistSession
		}
		_ = fn(sessionsDir, snapshot)
	}
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

	// Resolve the agent ID for this turn once and stamp both the user
	// message and the downstream streaming/accumulator path with the same
	// value. Previously the user message was pinned to sess.AgentID (the
	// creation agent) while the streaming path resolved to
	// CurrentAgentID || AgentID, so a mid-session agent switch left the
	// user's bubble rendering under the original agent and the assistant
	// reply under the new one — see Bug Fixes/Agent Stamping Asymmetry.
	agentID := sess.AgentID
	if sess.CurrentAgentID != "" {
		agentID = sess.CurrentAgentID
	}
	sess.Messages = append(sess.Messages, Message{
		ID:        uuid.New().String(),
		Role:      "user",
		Content:   message,
		AgentID:   agentID,
		Timestamp: time.Now(),
	})
	sess.UpdatedAt = time.Now()
	modelOverride := sess.CurrentModelID
	providerOverride := sess.CurrentProviderID
	// Capture prior messages before releasing the lock so we can pass them
	// to SeedHistory outside the critical section.
	priorMessages := make([]Message, len(sess.Messages)-1)
	copy(priorMessages, sess.Messages[:len(sess.Messages)-1])

	m.persistLocked(sess)
	m.mu.Unlock()

	// Build provider-shaped prior messages once so we can both seed the
	// engine's process-wide store (for the legacy CLI path that still
	// reads from it) and attach them to the per-call context (for the
	// serve path, where the engine uses ctx-scoped history as the
	// authoritative source for the model request payload).
	//
	// ThinkingBlocks and StopReason MUST round-trip onto the projected
	// provider.Message so subsequent turns replay the encrypted thinking
	// signature verbatim. Without this propagation, Anthropic silently
	// disables extended-thinking continuity from turn 2 onward — the
	// session accumulator persists the structured blocks on
	// session.Message.ThinkingBlocks, but the wire payload sent to the
	// model is built from the slice projected here. Empty inputs project
	// to empty (nil) outputs, preserving the no-thinking fall-through.
	//
	// Bug M4-adjacent (May 2026): session.Message persists tool-result
	// errors with Role:"tool_error" (see accumulator.applyToolResult).
	// The Anthropic provider's buildMessages switch only matches
	// Role:"tool" — a raw "tool_error" falls through and the message is
	// silently dropped from the model request payload on reload. The
	// projection seam canonicalises here to Role:"tool" + IsError:true so
	// the wire shape is uniform with the live-stream path stamped by the
	// M4 engine seam. Already-canonical Role:"tool" rows are forwarded
	// unchanged so a future writer that persists IsError directly is not
	// double-flipped.
	var providerMsgs []provider.Message
	if len(priorMessages) > 0 {
		providerMsgs = make([]provider.Message, 0, len(priorMessages))
		for _, msg := range priorMessages {
			role := msg.Role
			isError := false
			if role == "tool_error" {
				role = "tool"
				isError = true
			}
			providerMsgs = append(providerMsgs, provider.Message{
				Role:           role,
				Content:        msg.Content,
				ThinkingBlocks: msg.ThinkingBlocks,
				StopReason:     msg.StopReason,
				IsError:        isError,
			})
		}
	}

	// Pre-populate the engine's in-memory context store with the session's
	// historical messages so the agent retains context after a server restart.
	// Only fires when the streamer implements streaming.HistorySeeder and the
	// session has prior turns; the engine marks the session seeded after the
	// first call so subsequent turns are not duplicated.
	if seeder, ok := m.streamer.(streaming.HistorySeeder); ok && len(providerMsgs) > 0 {
		seeder.SeedHistory(sessionID, providerMsgs)
	}

	// Wrap the context with WithCancel so CancelInflight can terminate the turn.
	// Register the cancel function keyed by sessionID so a concurrent API call
	// can cancel the in-flight turn. The cancel is deregistered when the turn
	// completes (AccumulateStream closes its channel).
	cancelCtx, cancel := context.WithCancel(ctx)
	m.inflightMu.Lock()
	m.inflight[sessionID] = cancel
	m.inflightMu.Unlock()

	// Start a monitor goroutine that deregisters the cancel when the turn is done
	go func() {
		// Wait for the turn to complete by checking if the cancel is still registered
		// and the context is done. A better approach is to use a channel that gets
		// closed by the turn completion — we'll handle that via a wrapper below.
	}()

	ctx = context.WithValue(cancelCtx, IDKey{}, sessionID)
	// Attach the per-session prior history so the engine's
	// buildContextWindow can source the model request payload from this
	// session's messages alone, not from the shared FileContextStore that
	// accumulates every session's turns. Without this, two concurrent
	// sessions sharing one engine see each other's history in the model
	// request — see session_integration_test.go cross-session isolation.
	ctx = WithPriorMessages(ctx, providerMsgs)
	if providerOverride != "" {
		ctx = context.WithValue(ctx, ProviderOverrideKey{}, providerOverride)
	}
	if modelOverride != "" {
		ctx = context.WithValue(ctx, ModelOverrideKey{}, modelOverride)
	}
	rawCh, err := m.streamer.Stream(ctx, agentID, message)
	if err != nil {
		// Clean up the registered cancel on stream error
		m.inflightMu.Lock()
		delete(m.inflight, sessionID)
		m.inflightMu.Unlock()
		return nil, err
	}

	accumCh := AccumulateStream(ctx, m, sessionID, agentID, rawCh)

	// Wrap the accumCh to deregister the cancel when the turn completes.
	// We do this at the outermost layer (before recorder tee, before return)
	// so the cancel is deregistered only when all consumers have finished.
	finalCh := make(chan provider.StreamChunk, 64)
	// Capture sessionID explicitly to avoid race detector issues with closure variable access
	capturedSessionID := sessionID
	hasRecorder := m.recorder != nil
	go func() {
		defer close(finalCh)
		defer func() {
			m.inflightMu.Lock()
			delete(m.inflight, capturedSessionID)
			m.inflightMu.Unlock()
		}()

		if hasRecorder {
			// If we have a recorder, tee the chunks
			for chunk := range accumCh {
				m.recorder.RecordChunk(capturedSessionID, chunk)
				finalCh <- chunk
			}
		} else {
			// Otherwise, just forward
			for chunk := range accumCh {
				finalCh <- chunk
			}
		}
	}()

	return finalCh, nil
}

// UpdateSessionAgent updates the active agent for the given session.
//
// Expected:
//   - sessionID identifies an existing session.
//   - agentID is the ID of the agent to switch to.
//
// Returns:
//   - nil when the agent is updated successfully.
//   - ErrSessionNotFound when no session matches the identifier.
//
// Side effects:
//   - Sets CurrentAgentID on the session so subsequent SendMessage calls
//     stream through the new agent rather than the original session agent.
func (m *Manager) UpdateSessionAgent(sessionID, agentID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return ErrSessionNotFound
	}

	sess.CurrentAgentID = agentID
	sessionsDir := m.sessionsDir
	persistFn := m.persistFn
	var snapshot *Session
	if sessionsDir != "" {
		snap := *sess
		msgs := make([]Message, len(sess.Messages))
		copy(msgs, sess.Messages)
		snap.Messages = msgs
		snapshot = &snap
	}
	m.mu.Unlock()

	if snapshot != nil {
		fn := persistFn
		if fn == nil {
			fn = PersistSession
		}
		_ = fn(sessionsDir, snapshot)
	}
	return nil
}

// UpdateSessionModel sets the active provider and model identifiers on a session.
//
// Expected:
//   - sessionID identifies an existing session.
//   - providerID identifies the active provider for subsequent turns.
//   - modelID identifies the active model for subsequent turns.
//
// Returns:
//   - nil when the session is updated successfully.
//   - ErrSessionNotFound when no session matches the identifier.
//
// Side effects:
//   - Sets CurrentProviderID and CurrentModelID on the session so subsequent
//     SendMessage calls stream through the new provider/model pairing.
//   - Persists the change to the session sidecar via persistLocked.
func (m *Manager) UpdateSessionModel(sessionID, providerID, modelID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return ErrSessionNotFound
	}

	sess.CurrentProviderID = providerID
	sess.CurrentModelID = modelID
	sessionsDir := m.sessionsDir
	persistFn := m.persistFn
	var snapshot *Session
	if sessionsDir != "" {
		snap := *sess
		msgs := make([]Message, len(sess.Messages))
		copy(msgs, sess.Messages)
		snap.Messages = msgs
		snapshot = &snap
	}
	m.mu.Unlock()

	if snapshot != nil {
		fn := persistFn
		if fn == nil {
			fn = PersistSession
		}
		_ = fn(sessionsDir, snapshot)
	}
	return nil
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
	// Bug fix (May 2026 — Session Seal Persistence Hole): mirror the
	// MarkEndedFromEvent fix on the direct-close site. Without this the
	// engine's closeSessionIfManaged success path leaves a stale "active"
	// sidecar, undoing the seal at next restart.
	m.persistLocked(sess)

	return nil
}

// DeleteSession removes a session entirely — both from the in-memory map and
// from disk (when sessionsDir is configured). This is the destructive
// counterpart to CloseSession: where Close flips the lifecycle status to
// "completed" (the session sticks around for forensics, replay, and
// child-history navigation), Delete is for "I never want to see this
// session again" — backs the Vue UI's per-row trash button in
// SessionBrowser / SessionSwitcher.
//
// Expected:
//   - sessionID identifies an existing session.
//
// Returns:
//   - nil when the session was removed.
//   - ErrSessionNotFound when no session matches sessionID.
//
// Side effects:
//   - Deletes the session from the in-memory map.
//   - Deletes the session's .meta.json sidecar from disk when sessionsDir is
//     configured (a missing sidecar is tolerated — a not-yet-persisted session
//     is still deletable). The events.jsonl WAL is also removed so the
//     session leaves no residue on disk.
//   - Does NOT cascade to child sessions: child sessions are independent
//     records on disk and the caller is expected to delete them too if
//     desired. A future cascade option can be layered on top.
func (m *Manager) DeleteSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[sessionID]; !ok {
		return ErrSessionNotFound
	}

	delete(m.sessions, sessionID)

	if m.sessionsDir != "" {
		// removeSessionFiles is split out so the persistence symbols stay in
		// persistence.go and the manager keeps a single I/O choke-point. The
		// helper tolerates missing files so a never-persisted session
		// (e.g. one created in tests without SetSessionsDir before the call)
		// still deletes cleanly.
		removeSessionFiles(m.sessionsDir, sessionID)
	}

	// Attachment subtree cleanup (plan §6 task-02 AC: session delete
	// removes the entire <sessionID>/attachments/ directory). The
	// store is goroutine-safe; calling under m.mu is acceptable
	// because RemoveSession does not call back into Manager.
	if m.attachments != nil {
		m.attachments.RemoveSession(sessionID)
	}

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

// ErrMessageNotFound is returned when a message ID is not found in a session.
var ErrMessageNotFound = errors.New("message not found")

// TruncateMessages removes all messages from (not including) the message
// with the given ID, then persists the session. The trigger message itself is
// also removed so the caller can re-populate the composer with its content
// and re-send.
//
// Expected:
//   - sessionID identifies an existing session.
//   - afterMessageID is the ID of the message whose content the user wants
//     to edit. All messages from this message onward (inclusive) are removed
//     so the caller can re-compose and re-send.
//
// Returns:
//   - nil on success.
//   - ErrSessionNotFound when no session matches sessionID.
//   - ErrMessageNotFound when afterMessageID is not present in the session.
//
// Side effects:
//   - Slices sess.Messages and persists the result to disk.
func (m *Manager) TruncateMessages(sessionID, afterMessageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}

	index := -1
	for i, msg := range sess.Messages {
		if msg.ID == afterMessageID {
			index = i
			break
		}
	}
	if index < 0 {
		return ErrMessageNotFound
	}

	sess.Messages = sess.Messages[:index]
	sess.UpdatedAt = time.Now()
	m.persistLocked(sess)
	return nil
}

// CancelInflight fires the context cancellation for the in-flight turn of a session.
//
// Expected:
//   - sessionID identifies the session with a potentially in-flight turn.
//
// Returns:
//   - true when a cancel was registered and fired for this session.
//   - false when no in-flight turn exists for the session.
//
// Side effects:
//   - Cancels the context passed to the streamer, which propagates to all
//     downstream goroutines and context-aware operations.
//   - Does not deregister the cancel function; that is done by the turn's
//     draining goroutine on channel close.
func (m *Manager) CancelInflight(sessionID string) bool {
	m.inflightMu.Lock()
	defer m.inflightMu.Unlock()

	cancel, ok := m.inflight[sessionID]
	if !ok {
		return false
	}

	cancel()
	return true
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
