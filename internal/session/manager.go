package session

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/permissionmode"
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

// DefaultToolAnomalyStreakCap is the number of consecutive tool-use-
// anomaly assistant messages (ToolUseNoCalls or AbandonedTool) tolerated
// before the session escalates to failed. The Z.AI GLM provider family
// stamps these sentinels as false positives on otherwise healthy turns,
// so a single occurrence is softened under a corrective continuation;
// a sustained streak indicates a genuine model pathology.
const DefaultToolAnomalyStreakCap = 3

// ToolAnomalyCorrectivePrompt is the corrective instruction the engine
// re-sends after a tolerated tool-use-anomaly turn, nudging the model
// to either emit the tool calls it committed to in thinking or produce
// a plain assistant answer.
const ToolAnomalyCorrectivePrompt = "Your previous turn announced a tool call but emitted none. " +
	"Either issue the tool call now using the available tool schema, or answer directly in plain text."

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
	// TargetSessionID is the child session identifier for a delegation
	// message. Stamped by the engine at message persist time so the
	// frontend's loadSessionForDelegation can route to the correct child
	// session on cold reload when the chainSessions map is empty.
	// Empty for non-delegation messages and for delegation messages that
	// predate this field.
	TargetSessionID string `json:"targetSessionId,omitempty"`
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
	StopReason string `json:"stopReason,omitempty"`
	// DurationMs is the streaming duration from accumulator creation to
	// message flush, in milliseconds. Stamped on assistant messages.
	DurationMs int64     `json:"durationMs,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// Session represents a planning session with conversation history,
// coordination store, and delegation chain status.
type Session struct {
	ID                string `json:"id"`
	AgentID           string `json:"agent_id"`
	CurrentAgentID    string `json:"current_agent_id,omitempty"` // actively selected agent; overrides AgentID when set
	CurrentModelID    string `json:"current_model_id,omitempty"`
	CurrentProviderID string `json:"current_provider_id,omitempty"`
	// ModelPinned marks the Current* pair as an explicit user selection
	// (UI model switch). Pinned pairs outrank manifest and config defaults
	// everywhere, survive failover flushes, and are inherited by delegated
	// child sessions.
	ModelPinned       bool                      `json:"model_pinned,omitempty"`
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
	// PermissionMode is the per-session safety dial introduced by the
	// Permission Modes plan (May 2026). Valid values are:
	//
	//   - "plan"          read-only; engine filters write tools out
	//   - "default"       permissions.yaml + legacy denied-roots both apply
	//   - "accept_edits"  auto-accept Write/Edit/MultiEdit prompts
	//   - "ask"           interactive — pathguard denial publishes a
	//                     permission_required event and suspends the
	//                     tool call until the operator grants or denies.
	//                     Added by Slice 1 of the Permission Mode
	//                     ModeAskUser Extension plan (May 2026); the
	//                     interactive plumbing lands in Slices 2-5.
	//   - "yolo"          full pathguard bypass (trusted sandboxes only)
	//
	// Empty string is canonicalised to "default" by every reader
	// (permissionmode.FromContext, the constructor defaults below).
	// Persisted with omitempty so legacy session sidecars stay
	// byte-identical until a mode other than "default" is set.
	PermissionMode string `json:"permission_mode,omitempty"`
	// FailureReason captures the terminal error that caused this session
	// to flip to StatusFailed. Written once on the active -> failed
	// transition; empty for all other states. Truncated to 256 chars.
	FailureReason string `json:"failure_reason,omitempty"`
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
	ID                string `json:"id"`
	AgentID           string `json:"agentId"`
	CurrentAgentID    string `json:"currentAgentId,omitempty"`
	CurrentModelID    string `json:"currentModelId,omitempty"`
	CurrentProviderID string `json:"currentProviderId,omitempty"`
	ParentID          string `json:"parentId,omitempty"`
	// ChainID surfaces the delegation coordination chain identifier so the
	// Vue chatStore can rebuild its (chainId → childSessionId) map from
	// the session list on cold load — closing the reload-hole left by
	// a488b858 where SwarmEvents do not replay on reconnect. Omitted when
	// empty so root sessions stay byte-identical to their pre-field shape.
	ChainID     string `json:"chainId,omitempty"`
	Title       string `json:"title"`
	IsStreaming bool   `json:"isStreaming"`
	// ActiveTurnID is the in-flight Turn UUID for this session, or ""
	// when no Turn is Running. Populated by the API layer (not the
	// manager) at GET /api/v1/sessions via
	// turn.Registry.FindActiveBySession so callers can resolve "what
	// Turn is this session currently driving" in one round-trip.
	// Always emitted (never omitempty) so clients see a defined string
	// rather than undefined — matches the SessionResponse.ActiveTurnID
	// contract on the single-session DTO.
	//
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Turn-Based Post-Then-Poll Architecture (May 2026).md §4d Commit 1.
	ActiveTurnID string    `json:"activeTurnId"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	// PermissionMode mirrors Session.PermissionMode so the Vue
	// chatStore can hydrate the chip directly from the session list
	// on cold load — backend payload is the canonical source per the
	// Permission Modes plan (May 2026) Slice 3, with localStorage as
	// the offline boot fall-back only. Omitted when empty so sessions
	// persisted before the field existed stay byte-identical to their
	// pre-field summary shape.
	PermissionMode string `json:"permissionMode,omitempty"`
	MessageCount   int    `json:"messageCount"`
}

// Recorder captures stream chunks for session recording.
type Recorder interface {
	// RecordChunk captures a stream chunk for the given session.
	RecordChunk(sessionID string, chunk provider.StreamChunk)
}

// Manager handles session lifecycle and message routing.
type Manager struct {
	sessions map[string]*Session
	// toolAnomalyStreaks counts consecutive tool-use-anomaly assistant
	// messages (ToolUseNoCalls, AbandonedTool) per session. The Z.AI
	// GLM provider family emits these sentinels as false positives on
	// otherwise healthy turns, so the first occurrences are tolerated
	// under a corrective continuation rather than failing the session;
	// only DefaultToolAnomalyStreakCap consecutive anomalies escalate
	// to failed. A healthy assistant message resets the streak. Read
	// and written under m.mu.
	toolAnomalyStreaks map[string]int
	mu                 sync.RWMutex
	streamer           streaming.Streamer
	notifications      map[string][]streaming.CompletionNotificationEvent
	notifMu            sync.Mutex
	recorder           Recorder
	sessionsDir        string
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

	// mcpGrantsMu guards mcpGrants.
	mcpGrantsMu sync.RWMutex
	// mcpGrants holds in-memory per-session MCP server grants accumulated
	// via ModeAskUser "Allow this session" prompts. Permission Mode
	// ModeAskUser Extension plan (May 2026) Slice 5.
	//
	// Distinct from Session.PermissionMode + permissions.yaml: this map
	// is intentionally NOT persisted to the session sidecar. The plan
	// §4 Slice 5 explicitly contracts the "session" scope as in-memory-
	// only — operators get a per-session grant that evaporates on
	// daemon restart, distinct from the "forever" scope which writes to
	// permissions.yaml.
	//
	// Stored as a set-like map[string]struct{} so re-grants are
	// idempotent at lookup time. Reads via SessionMCPGrants take an
	// RLock; writes via AppendSessionMCPGrant take the write lock.
	mcpGrants map[string]map[string]struct{}
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
		sessions:           make(map[string]*Session),
		toolAnomalyStreaks: make(map[string]int),
		streamer:           streamer,
		notifications:      make(map[string][]streaming.CompletionNotificationEvent),
		inflight:           make(map[string]context.CancelFunc),
		mcpGrants:          make(map[string]map[string]struct{}),
	}
}

// AppendSessionMCPGrant records an in-memory MCP server grant for the
// supplied session. The grant persists for the lifetime of the running
// daemon and is NOT written to the session sidecar — Permission Mode
// ModeAskUser Extension plan (May 2026) Slice 5 contracts the
// "session" scope as in-memory-only.
//
// Returns nil on success (including on a re-grant — the operation is
// idempotent). Returns ErrSessionNotFound when sessionID is unknown to
// the manager so the API handler can surface a 404 distinctly from a
// successful no-op.
//
// Side effects:
//   - Appends mcpServer to mcpGrants[sessionID] under the write lock.
//
// Expected: parameters for AppendSessionMCPGrant.
// Returns: result of AppendSessionMCPGrant.
func (m *Manager) AppendSessionMCPGrant(sessionID, mcpServer string) error {
	m.mu.RLock()
	_, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return ErrSessionNotFound
	}

	m.mcpGrantsMu.Lock()
	defer m.mcpGrantsMu.Unlock()
	servers, ok := m.mcpGrants[sessionID]
	if !ok {
		servers = make(map[string]struct{})
		m.mcpGrants[sessionID] = servers
	}
	servers[mcpServer] = struct{}{}
	return nil
}

// SessionMCPGrants returns a snapshot of the MCP servers granted to
// sessionID via the ModeAskUser "Allow this session" path. Order is
// not stable (set semantics); callers that need a stable order must
// sort the returned slice. Returns an empty slice when no grants
// exist for the session — never returns nil.
//
// Concurrency: read-only — takes mcpGrantsMu under RLock.
//
// Expected: parameters for SessionMCPGrants.
// Returns: result of SessionMCPGrants.
// Side effects: None.
func (m *Manager) SessionMCPGrants(sessionID string) []string {
	m.mcpGrantsMu.RLock()
	defer m.mcpGrantsMu.RUnlock()
	servers, ok := m.mcpGrants[sessionID]
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(servers))
	for s := range servers {
		out = append(out, s)
	}
	return out
}

// MarkEndedFromEvent flips the matching session's status to
// StatusCompleted in response to an external "session.ended" event.
// Idempotent and status-precedence-aware (Option A, June 2026):
// abandoned > completed > active > failed — a previously-failed
// session that was demoted back to active by the recovery-demotion
// path is now eligible for this seal. Previously-completed and
// abandoned sessions are skipped (terminal). Unknown session IDs
// are silently ignored (events for foreign sessions, or for sessions
// that pre-date the manager's restart, simply have nothing to do here).
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
//
// Returns: result of MarkEndedFromEvent.
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
	// Option A (June 2026): a previously-failed-but-recovered session
	// (demoted back to active by the recovery-demotion path in
	// appendSessionMessage) can now be sealed as completed. The status
	// precedence is: abandoned > completed > active > failed — failed
	// is no longer terminal; a recovered session reaches the end event
	// with a clean stop and should become completed alongside every
	// other sealed session.
	if sess.Status == string(StatusCompleted) ||
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
//
// Returns: result of SetSessionsDir.
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
//
// Expected: parameters for AttachmentStore.
// Returns: result of AttachmentStore.
// Side effects: None.
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
//
// Returns: result of SetEmbeddingModel.
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
//
// Returns: result of SetOrphanGrace.
func (m *Manager) SetOrphanGrace(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orphanGrace = d
}

// persistLocked writes the session to disk when sessionsDir is set.
// The caller MUST hold m.mu (read or write). Errors are swallowed to
// avoid blocking the message hot path; persistence is best-effort.
//
// Expected: parameters for persistLocked.
// Returns: result of persistLocked.
// Side effects: None.
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
		// Default the permission mode so every reader sees a stable
		// value (Permission Modes plan §4 Slice 1). FromContext also
		// canonicalises empty → "default", so this is belt-and-braces
		// for any direct reader that touches sess.PermissionMode.
		PermissionMode: permissionmode.ModeDefault,
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
		PermissionMode:    permissionmode.ModeDefault,
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
		PermissionMode:    permissionmode.ModeDefault,
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
//
// Expected: parameters for ListSessions.
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
			PermissionMode:    sess.PermissionMode,
			MessageCount:      len(sess.Messages),
		})
	}

	return summaries
}

// deriveSummaryTitle returns a non-empty human-readable title for a session.
// It prefers the first user message content (truncated) and falls back to a
// short identifier derived from the session ID so the frontend never sees an
// empty title.
//
// Expected: parameters for deriveSummaryTitle.
// Returns: result of deriveSummaryTitle.
// Side effects: None.
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
//
// Returns: result of sweepOrphansLocked.
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
//
// Expected: parameters for AllSessions.
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
//
// Expected: parameters for sortSessionsByCreatedAt.
// Side effects: None.
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
//
// appendSessionMessage appends msg to the session under the manager lock
// and applies assistant-flush promotion: the message's ModelName and
// ProviderName — the pair that actually served the turn, including after a
// failover cascade — become the session's CurrentModelID/CurrentProviderID
// whenever non-empty. The recorded pair feeds PrepareSend's per-turn
// override, so a session sticks to the provider+model that last succeeded
// instead of re-attempting a dead create-time default every turn. Empty
// fields never clobber the recorded pair, and non-assistant roles are
// ignored. The updated pair persists via the standard append persistence.
func (m *Manager) appendSessionMessage(sessionID string, msg Message) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return
	}
	// Id-preserving: only assign when empty. Turn-aware callers
	// (internal/session/accumulator.go::turnAwareAppender) pre-assign
	// id + timestamp so the Turn.MessagesAdded snapshot agrees with the
	// session-stored copy. Without this guard the manager would
	// overwrite the pre-assigned id, leaving the Turn registry holding
	// rows with empty ids — which the FE's pollTurnUntilTerminal at
	// chatStore.ts:1813 silently skips via `if (!row.id) continue`,
	// producing the "loading bar but no content" symptom (May 2026
	// bug-hunt, Turn-based plan Phase 3 follow-up).
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	sess.Messages = append(sess.Messages, msg)

	if msg.Role == "assistant" {
		if !sess.ModelPinned {
			if msg.ModelName != "" {
				sess.CurrentModelID = msg.ModelName
			}
			if msg.ProviderName != "" {
				sess.CurrentProviderID = msg.ProviderName
			}
		}
		// Surfaced-failure flip (Bugs E, F and G, May 2026). When the
		// accumulator stamps a surfaced-failure sentinel on the flushed
		// assistant message, flip Status active -> failed so the chat UI,
		// session list, and API expose the failure immediately rather
		// than waiting on the 30-min boot-time orphan reap.
		//
		// Sentinels that drive the flip:
		//   - StopReasonStreamTruncated (Bug F): openaicompat wire cut
		//     before terminal finish_reason landed. Live reproducers —
		//     glm-4.6 plan-writer session 8779c2ae-69a4-... and glm-5
		//     plan-writer session 5e37d947-c049-4d5c-a209-658d9c6a5186 on zai.
		//   - StopReasonToolUseNoCalls (Bug G): provider announced
		//     "tool_use" as finish_reason but emitted zero tool_call
		//     blocks — wire contract violation. Live reproducer —
		//     plan-writer session 8169ca2d-5536-41af-b947-ba3fd7514416
		//     on glm-5/zai. The plan was never written; pre-flip the
		//     session completed silently.
		//   - StopReasonAbandonedTool (Bug E): whitespace-only content
		//     with non-empty reasoning and zero tool_calls — model
		//     committed to a tool call in the thinking channel but never
		//     emitted it. The accumulator stamps the sentinel at
		//     accumulator.go:844-849; without this flip the session
		//     remained "completed" with no deliverable. Live reproducer —
		//     child session 3fcb56df-8224-485c-9187-2aaad9ed5879 on
		//     glm-4.5/zai (executor) and glm-5 plan-writer abandons on
		//     large plans (user-facing dogfood blocker).
		//
		// Restricted to the assistant-role branch: a future refactor that
		// accidentally populates StopReason on a tool_call / tool_result
		// row must NOT trigger spurious failed states.
		//
		// The status guard mirrors MarkEndedFromEvent's precedence
		// (manager.go:287-290): failed is the terminal state — already-
		// failed sessions stay failed (idempotent); already-completed
		// sessions DO escalate to failed because a surfaced failure is a
		// higher-precedence signal than a (now-known-premature) seal;
		// abandoned sessions are reaped artefacts that don't accept
		// further mutations.
		toolAnomaly := msg.StopReason == StopReasonToolUseNoCalls ||
			msg.StopReason == StopReasonAbandonedTool
		if toolAnomaly {
			m.toolAnomalyStreaks[sessionID]++
			if m.toolAnomalyStreaks[sessionID] < DefaultToolAnomalyStreakCap {
				sess.Status = string(StatusActive)
				sess.FailureReason = ""
				m.mu.Unlock()
				return
			}
		} else {
			delete(m.toolAnomalyStreaks, sessionID)
		}
		if (msg.StopReason == StopReasonStreamTruncated ||
			msg.StopReason == StopReasonToolUseNoCalls ||
			msg.StopReason == StopReasonAbandonedTool ||
			msg.StopReason == StopReasonToolLoopExceeded) &&
			sess.Status != string(StatusFailed) &&
			sess.Status != string(StatusAbandoned) {
			sess.Status = string(StatusFailed)
			// Capture the stop reason as the failure reason, truncated to 256 chars.
			// msg.StopReason is a plain string field, so no conversion is needed.
			reason := msg.StopReason
			if len(reason) > 256 {
				reason = reason[:256]
			}
			sess.FailureReason = reason
		} else if msg.StopReason != StopReasonStreamTruncated &&
			msg.StopReason != StopReasonToolUseNoCalls &&
			msg.StopReason != StopReasonAbandonedTool &&
			msg.StopReason != StopReasonToolLoopExceeded &&
			sess.Status == string(StatusFailed) {
			// Failed-recovery demotion (Option A, June 2026). A healthy
			// assistant message arrived on a session that was previously
			// marked failed by a sentinel-stamped turn. The engine's
			// tool-loop continuation injected a continuation prompt and
			// the model produced a genuine assistant response, proving
			// the session is still viable — for example, the Z.AI glm
			// provider family falsely flags tool_use_no_calls on every
			// turn, but subsequent turns complete successfully. Demote
			// back to active so downstream consumers (UI, API, delegation
			// chain) see the recovery. A previously-demoted session can
			// still transition to completed via CloseSession or
			// MarkEndedFromEvent, both updated below to allow the
			// failed -> completed edge.
			sess.Status = string(StatusActive)
			// Demotion implies recovery — clear the failure reason.
			sess.FailureReason = ""
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
//
// SendMessageWithAttachments is SendMessage augmented with per-turn
// attachment ids. The ids are resolved against the manager's
// AttachmentStore and the materialised slice is threaded onto the
// context via session.WithAttachments so the engine can stamp it on
// the user message inside Stream.
//
// Two-phase reference (plan §6 task-06): each id is MarkReserved'd
// BEFORE the streamer Stream call fires; on success the new user
// message id is MarkReferenced'd; on failure all reservations are
// ReleaseReservation'd. The atomic counter on the storage layer makes
// MarkReserved → MarkReferenced safe under concurrent uploads.
//
// Empty `attachmentIDs` falls through to the same code path as
// SendMessage with no behaviour change.
//
// Expected:
//   - ctx is valid for the lifetime of the streaming request.
//   - sessionID identifies an existing session.
//   - message is the user's message content.
//   - attachmentIDs is the slice of ids the caller wants threaded onto
//     this turn (already validated as existing in the session store
//     by the upload + reference flow). Empty means text-only.
//
// Returns:
//   - A stream of provider chunks when the session exists and
//     attachments resolve.
//   - ErrSessionNotFound when no session matches the identifier.
//   - session.ErrAttachmentNotFound when any id is not present in
//     the session's attachment index.
//

// PrepareSendWithAttachments resolves attachments, reserves them, appends
// the user message via PrepareSend, and promotes reservations to permanent
// references. Returns the prepared context and agent ID for StartStream.
//
// When attachmentIDs is empty, delegates directly to PrepareSend.
//
// Expected:
//   - ctx carries per-turn overrides.
//   - sessionID identifies an existing session.
//   - message is the raw user text.
//   - attachmentIDs references previously uploaded attachments.
//
// Returns:
//   - The prepared context (carrying session ID, prior messages, overrides,
//     permission mode, attachments, and inflight cancel).
//   - The resolved agent ID for streaming.
//   - ErrSessionNotFound, attachment resolution errors, or errors from
//     PrepareSend.
//
// Side effects:
//   - Resolves and reserves attachments.
//   - Appends user message and persists session.
//   - Promotes reservations to permanent references.
func (m *Manager) PrepareSendWithAttachments(
	ctx context.Context, sessionID, message string, attachmentIDs []string,
) (context.Context, string, error) {
	if len(attachmentIDs) == 0 {
		return m.PrepareSend(ctx, sessionID, message)
	}
	store := m.AttachmentStore()
	materialised, err := store.Resolve(sessionID, attachmentIDs)
	if err != nil {
		return nil, "", err
	}
	atts := make([]provider.Attachment, 0, len(materialised))
	for _, mat := range materialised {
		atts = append(atts, provider.Attachment{
			ID:               mat.Record.ID,
			Kind:             mat.Record.Kind,
			MediaType:        mat.Record.MediaType,
			OriginalFilename: mat.Record.OriginalFilename,
			SizeBytes:        mat.Record.SizeBytes,
			Data:             mat.Data,
		})
		store.MarkReserved(sessionID, mat.Record.ID)
	}

	ctx = WithAttachments(ctx, atts)

	priorCount := 0
	if snap, err := m.SnapshotSession(sessionID); err == nil {
		priorCount = len(snap.Messages)
	}

	preparedCtx, agentID, prepErr := m.PrepareSend(ctx, sessionID, message)
	if prepErr != nil {
		for _, id := range attachmentIDs {
			store.ReleaseReservation(sessionID, id)
		}
		return nil, "", prepErr
	}

	if snap, snapErr := m.SnapshotSession(sessionID); snapErr == nil && len(snap.Messages) > priorCount {
		msgID := snap.Messages[len(snap.Messages)-1].ID
		for _, id := range attachmentIDs {
			store.MarkReferenced(sessionID, id, msgID)
		}
	} else {
		for _, id := range attachmentIDs {
			store.ReleaseReservation(sessionID, id)
		}
	}

	return preparedCtx, agentID, nil
}

// SendMessageWithAttachments resolves attachments, appends the user message,
// and returns a channel of streaming response chunks.
//
// Convenience wrapper around PrepareSendWithAttachments + StartStream for
// callers that need the synchronous (blocking) shape.
//
// Expected: parameters for SendMessageWithAttachments.
// Returns: result of SendMessageWithAttachments.
// Side effects: None.
func (m *Manager) SendMessageWithAttachments(
	ctx context.Context, sessionID, message string, attachmentIDs []string,
) (<-chan provider.StreamChunk, error) {
	preparedCtx, agentID, err := m.PrepareSendWithAttachments(ctx, sessionID, message, attachmentIDs)
	if err != nil {
		return nil, err
	}
	return m.StartStream(preparedCtx, sessionID, agentID, message)
}

// SendMessage appends a user message to the session and returns a channel
// of streaming response chunks.
//
// Prior messages are projected through a role-canonicalisation step before
// they reach the provider adapter. The session accumulator persists messages
// with roles outside the provider-adapter switch set of {user, assistant,
// system, tool}: tool_error, tool_result, tool_call, thinking, delegation,
// and delegation_started. Both the OpenAI-compat and Anthropic provider
// adapters silently drop unrecognised roles (see openaicompat.go:74 and
// anthropic.go:1628). The projection seam maps each persisted role to a
// standard role so no message is silently dropped from the model request
// payload on reload:
//
//	tool_error         -> tool   + IsError:true
//	tool_result        -> tool
//	tool_call          -> assistant  (content describes the call)
//	thinking           -> assistant
//	delegation         -> assistant
//	delegation_started -> assistant
//
// Expected: parameters for SendMessage.
// Returns: result of SendMessage.
// Side effects: None.
func (m *Manager) SendMessage(ctx context.Context, sessionID string, message string) (<-chan provider.StreamChunk, error) {
	preparedCtx, agentID, err := m.PrepareSend(ctx, sessionID, message)
	if err != nil {
		return nil, err
	}
	return m.StartStream(preparedCtx, sessionID, agentID, message)
}

// PrepareSend appends the user message to the session, builds provider
// messages from prior history, and returns the prepared context plus the
// resolved agent ID. The returned context carries all per-turn values
// (prior messages, provider/model overrides, permission mode, session ID,
// inflight cancel) that StartStream needs to drive the provider call.
//
// Split from SendMessage so the dispatcher can append the user message
// synchronously (making it available in the POST response snapshot) while
// deferring the provider call to a background goroutine. This eliminates
// the POST handler's blocking on time-to-first-chunk.
//
// Expected:
//   - ctx carries per-turn overrides (StreamAgentOverrideKey, attachments).
//   - sessionID identifies an existing session.
//   - message is the raw user text.
//
// Returns:
//   - A context.Context threaded with session ID, prior messages, model /
//     provider overrides, permission mode, and an inflight cancel function.
//   - The agent ID the streamer should drive under (honours override).
//   - ErrSessionNotFound when the session does not exist.
//
// Side effects:
//   - Appends a user message to the session and persists it.
//   - Registers an inflight cancel function keyed by sessionID.
//   - Seeds the engine's history store when prior messages exist.
func (m *Manager) PrepareSend(ctx context.Context, sessionID string, message string) (context.Context, string, error) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, "", ErrSessionNotFound
	}

	agentID := sess.AgentID
	if sess.CurrentAgentID != "" {
		agentID = sess.CurrentAgentID
	}
	userMessageAgent := agentID
	if override := StreamAgentOverrideFromContext(ctx); override != "" {
		agentID = override
	}
	sess.Messages = append(sess.Messages, Message{
		ID:        uuid.New().String(),
		Role:      "user",
		Content:   message,
		AgentID:   userMessageAgent,
		Timestamp: time.Now(),
	})
	sess.UpdatedAt = time.Now()
	modelOverride := sess.CurrentModelID
	providerOverride := sess.CurrentProviderID
	permMode := sess.PermissionMode
	priorMessages := make([]Message, len(sess.Messages)-1)
	copy(priorMessages, sess.Messages[:len(sess.Messages)-1])

	m.persistLocked(sess)
	m.mu.Unlock()

	var providerMsgs []provider.Message
	if len(priorMessages) > 0 {
		providerMsgs = make([]provider.Message, 0, len(priorMessages))
		for _, msg := range priorMessages {
			role := msg.Role
			isError := false

			switch role {
			case "tool_error":
				role = "tool"
				isError = true
			case "tool_result":
				role = "tool"
			case "tool_call":
				var content string
				if msg.ToolInput != "" {
					content = "[" + msg.Content + " with input: " + msg.ToolInput + "]"
				} else {
					content = "[" + msg.Content + "]"
				}
				providerMsgs = append(providerMsgs, provider.Message{
					Role:    "assistant",
					Content: content,
				})
				continue
			case "thinking":
				role = "assistant"
			case "delegation", "delegation_started":
				role = "assistant"
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

	if seeder, ok := m.streamer.(streaming.HistorySeeder); ok && len(providerMsgs) > 0 {
		seeder.SeedHistory(sessionID, providerMsgs)
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	m.inflightMu.Lock()
	m.inflight[sessionID] = cancel
	m.inflightMu.Unlock()

	preparedCtx := context.WithValue(cancelCtx, IDKey{}, sessionID)
	preparedCtx = WithPriorMessages(preparedCtx, providerMsgs)
	if providerOverride != "" {
		preparedCtx = context.WithValue(preparedCtx, ProviderOverrideKey{}, providerOverride)
	}
	if modelOverride != "" {
		preparedCtx = context.WithValue(preparedCtx, ModelOverrideKey{}, modelOverride)
	}
	preparedCtx = permissionmode.WithMode(preparedCtx, permMode)

	return preparedCtx, agentID, nil
}

// StartStream drives the provider stream using the context returned by
// PrepareSend and returns a channel of response chunks. The caller must
// pass the exact context and agentID returned by PrepareSend.
//
// On error the inflight cancel registered by PrepareSend is cleaned up.
// On success the returned channel is wrapped so that the inflight cancel
// is deregistered when the channel drains to completion.
//
// Expected:
//   - ctx is the context returned by PrepareSend.
//   - sessionID matches the session PrepareSend appended to.
//   - agentID is the resolved agent ID from PrepareSend.
//   - message is the raw user text (needed by engine.Stream for
//     buildContextWindow).
//
// Returns:
//   - A buffered channel of streaming chunks. Closed when the stream
//     completes.
//   - Any error from the streamer's Stream call.
//
// Side effects:
//   - Calls m.streamer.Stream (the engine), which blocks until the
//     provider emits its first chunk (failover hook peek).
//   - Spawns an AccumulateStream goroutine and a recorder-tee goroutine.
//   - Deregisters the inflight cancel when the channel drains.
func (m *Manager) StartStream(ctx context.Context, sessionID string, agentID string, message string) (<-chan provider.StreamChunk, error) {
	rawCh, err := m.streamer.Stream(ctx, agentID, message)
	if err != nil {
		m.inflightMu.Lock()
		delete(m.inflight, sessionID)
		m.inflightMu.Unlock()
		return nil, err
	}

	accumCh := AccumulateStream(ctx, m, sessionID, agentID, rawCh)

	finalCh := make(chan provider.StreamChunk, 64)
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
			for chunk := range accumCh {
				m.recorder.RecordChunk(capturedSessionID, chunk)
				finalCh <- chunk
			}
		} else {
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
	sess.ModelPinned = true
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

// ErrInvalidPermissionMode is returned by UpdatePermissionMode when the
// caller-supplied mode is outside the closed vocabulary defined by
// permissionmode. The handler at the API seam maps this to a 400 so
// schema-drift (typo, stale client) never silently writes an unknown
// string onto the session.
var ErrInvalidPermissionMode = errors.New("invalid permission mode")

// UpdatePermissionMode sets the per-session permission mode and
// persists the change so a process restart keeps the user's selection.
//
// Permission Modes plan (May 2026), §4 Slice 3.
//
// Expected:
//   - sessionID identifies an existing session.
//   - mode is one of the five canonical permissionmode constants
//     (ModePlan / ModeDefault / ModeAcceptEdits / ModeAskUser /
//     ModeYolo). Other values, including the empty string, are
//     rejected so the persisted value can never be a non-vocabulary
//     string the pathguard short-circuit doesn't recognise.
//     ModeAskUser ("ask") joined the vocabulary in Slice 1 of the
//     Permission Mode ModeAskUser Extension plan (May 2026).
//
// Returns:
//   - nil when the mode is updated successfully.
//   - ErrSessionNotFound when no session matches the identifier.
//   - ErrInvalidPermissionMode when mode is outside the vocabulary.
//
// Side effects:
//   - Sets PermissionMode on the in-memory Session so the next
//     SendMessage's ctx-stamp picks it up (manager.go around the
//     `permMode := sess.PermissionMode` snapshot inside SendMessage).
//   - Writes the updated session sidecar via PersistSession so the
//     selection survives a `flowstate serve` restart.
func (m *Manager) UpdatePermissionMode(sessionID, mode string) error {
	switch mode {
	case permissionmode.ModePlan,
		permissionmode.ModeDefault,
		permissionmode.ModeAcceptEdits,
		permissionmode.ModeAskUser,
		permissionmode.ModeYolo:
		// valid — fall through. ModeAskUser ("ask") admitted by Slice 1
		// of the Permission Mode ModeAskUser Extension plan (May 2026);
		// see internal/permissionmode/mode.go:ModeAskUser for the
		// behavioural contract.
	default:
		return ErrInvalidPermissionMode
	}

	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return ErrSessionNotFound
	}

	sess.PermissionMode = mode
	sess.UpdatedAt = time.Now()
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
// Cascade semantics (user bug May 2026 — "delete cascade"): deleting a
// session also removes every delegated descendant (children, grandchildren,
// transitively). Cascade order is children-first then root (post-order DFS)
// so a partial failure on disk cleanup leaves orphan children invisible to
// the FE (which filters root sessions by !parentId) rather than orphan
// parents that would persist in the session list. Cycle protection is
// inherited from sessionTree's visited-map walk — no separate guard.
//
// Atomicity: the in-memory delete sweep is atomic under m.mu. On-disk
// artefacts (sidecar, WAL, attachment subtree) are best-effort and a
// failure on a descendant does not roll back the in-memory removal —
// any disk residue becomes an orphan that future tooling can sweep.
// This mirrors the original single-session policy (removeSessionFiles
// already ignores errors).
//
// Expected:
//   - sessionID identifies an existing session.
//
// Returns:
//   - nil when the session (and any descendants) were removed.
//   - ErrSessionNotFound when no session matches sessionID.
//
// Side effects:
//   - Deletes the session and all descendants from the in-memory map.
//   - Deletes each removed session's .meta.json sidecar and .events.jsonl WAL
//     from disk when sessionsDir is configured (missing files are tolerated).
//   - Removes each removed session's attachment subtree via AttachmentStore.
//   - Drops each removed session's pending notification queue and any
//     registered inflight cancel (clears latent in-memory state — the
//     turn goroutine's own defer-driven dereg is a no-op once the entry
//     is gone).
func (m *Manager) DeleteSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	root, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}

	// Walk the descendant set first under the same lock so callers see
	// an atomic before/after in the in-memory map. sessionTree handles
	// cycles via its internal visited map.
	tree := sessionTree(m.sessions, root, make(map[string]bool))
	// Post-order: descendants first, then root last. If on-disk cleanup
	// fails partway, an orphan descendant is invisible to the FE
	// (root-only filter); an orphan root is not.
	ids := make([]string, 0, len(tree))
	for i := len(tree) - 1; i >= 0; i-- {
		ids = append(ids, tree[i].ID)
	}

	for _, id := range ids {
		delete(m.sessions, id)

		if m.sessionsDir != "" {
			// removeSessionFiles tolerates missing files so a
			// never-persisted descendant (e.g. CreateWithParent that
			// has not yet flushed) still cleans up the parent's
			// sidecar without erroring.
			removeSessionFiles(m.sessionsDir, id)
		}

		// Attachment subtree cleanup (plan §6 task-02 AC: session delete
		// removes the entire <sessionID>/attachments/ directory). The
		// store is goroutine-safe; calling under m.mu is acceptable
		// because RemoveSession does not call back into Manager.
		if m.attachments != nil {
			m.attachments.RemoveSession(id)
		}

		// Backstop adjacent latent surfaces (memory
		// feedback_close_latent_surfaces_too): per-session in-memory
		// state (pending notifications, inflight cancels) is not part
		// of the session record but is keyed by session id. Without
		// cleanup these entries leak per delete and a future re-created
		// session reusing the id would inherit stale state.
		m.notifMu.Lock()
		delete(m.notifications, id)
		m.notifMu.Unlock()

		m.inflightMu.Lock()
		if cancel, ok := m.inflight[id]; ok {
			// Cancel any in-flight turn before pulling the session
			// out from under it. Best-effort: the turn goroutine's
			// own defer will try to re-deregister but the missing
			// entry is a no-op.
			cancel()
			delete(m.inflight, id)
		}
		m.inflightMu.Unlock()
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

// ReapOrphanDelegations scans every session for delegation_started messages
// that never received a terminal delegation (completed/failed) status.
// Each such message is updated in place to a terminal delegation with
// status "abandoned" and the session is persisted to disk.
//
// Call this after RestoreSessions during boot-time recovery to close the
// lifecycle of delegations that were in-flight when the process exited.
// Without this, the parent session carries a delegation_started message
// forever — the orphaned tool_call is harmless (providers silently drop
// unknown roles) but the stale delegation_started is visible in the UI
// and prevents proper lifecycle observability.
//
// Expected:
//   - The manager's sessions map is fully populated (e.g. after RestoreSessions).
//
// Returns:
//   - None.
//
// Side effects:
//   - Mutates delegation_started messages to delegation in affected sessions.
//   - Persists each modified session's .meta.json sidecar via persistLocked.
func (m *Manager) ReapOrphanDelegations() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, sess := range m.sessions {
		m.reapOrphanDelegationsLocked(sess)
	}
}

// reapOrphanDelegationsLocked scans a single session for orphaned
// delegation_started messages and flips them to terminal delegation.
// Caller MUST hold m.mu (write).
//
// Expected:
//   - sess may be nil or have zero messages (both are no-ops).
//
// Side effects:
//   - Mutates sess.Messages in place.
//   - Calls persistLocked when any message was modified.
//
// Returns: result of reapOrphanDelegationsLocked.
func (m *Manager) reapOrphanDelegationsLocked(sess *Session) {
	if sess == nil || len(sess.Messages) == 0 {
		return
	}

	// Collect terminal delegation chain IDs so we can identify
	// delegation_started messages that never resolved.
	terminalChains := make(map[string]bool)
	for _, msg := range sess.Messages {
		if msg.Role != "delegation" {
			continue
		}
		key := msg.ChainID
		if key == "" {
			key = msg.TargetAgent
		}
		terminalChains[key] = true
	}

	modified := false
	for i, msg := range sess.Messages {
		if msg.Role != "delegation_started" {
			continue
		}
		key := msg.ChainID
		if key == "" {
			key = msg.TargetAgent
		}
		if terminalChains[key] {
			continue
		}
		// Found an orphaned delegation_started — flip to terminal status.
		sess.Messages[i].Role = "delegation"
		sess.Messages[i].Status = "abandoned"
		sess.Messages[i].Content = "Delegation terminated (process restart)"
		modified = true
	}

	if modified {
		m.persistLocked(sess)
	}
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
