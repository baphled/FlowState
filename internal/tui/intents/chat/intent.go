// Package chat provides the chat intent for FlowState TUI.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	tooldisplay "github.com/baphled/flowstate/internal/tool/display"
	"github.com/baphled/flowstate/internal/tui/components/notification"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/agentpicker"
	"github.com/baphled/flowstate/internal/tui/intents/eventdetails"
	"github.com/baphled/flowstate/internal/tui/intents/models"
	"github.com/baphled/flowstate/internal/tui/intents/sessionbrowser"
	"github.com/baphled/flowstate/internal/tui/intents/sessiontree"
	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/tui/uikit/navigation"
	"github.com/baphled/flowstate/internal/tui/uikit/theme"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
	"github.com/baphled/flowstate/internal/tui/views/chat"
	"github.com/baphled/flowstate/internal/ui/terminal"
	"github.com/baphled/flowstate/internal/ui/themes"
)

// StreamChunkMsg carries a streaming response chunk to the chat intent.
// EventType propagates the provider.StreamChunk.EventType, enabling the intent
// to handle special events such as "harness_retry" without modifying the
// standard chunk processing pipeline.
type StreamChunkMsg struct {
	Content      string
	Error        error
	Done         bool
	EventType    string
	ToolCallName string
	// ToolCallArgs carries the raw provider.ToolCall.Arguments map
	// (as received from the model) on chunks that carry a tool_call.
	// Downstream renderers (the inline tool widget) use this map to
	// build rich, opencode-style displays without re-parsing the
	// flattened "name: primary-arg" string in ToolCallName.
	ToolCallArgs   map[string]any
	ToolStatus     string
	ToolResult     string
	ToolIsError    bool
	DelegationInfo *provider.DelegationInfo
	Thinking       string
	Next           tea.Cmd
	// ModelID is the model that produced this chunk, stamped by the engine at stream time.
	ModelID string
	// ToolCallID propagates provider.StreamChunk.ToolCallID (P2 T1). It is the
	// upstream provider's tool-use identifier and correlates tool_call chunks
	// with the subsequent tool_result chunk within a single provider. Retained
	// on chunks and surfaced by the Ctrl+E event-details modal for the audit
	// trail; downstream coalesce now keys on InternalToolCallID instead so
	// cross-provider failover correlates correctly (P14b).
	ToolCallID string
	// InternalToolCallID propagates provider.StreamChunk.InternalToolCallID
	// (P14a). It is the FlowState-internal, session-scoped identifier minted
	// by streaming.ToolCallCorrelator and stamped by the engine on every
	// tool-related chunk before forwarding it to consumers. Same logical
	// tool call observed on two different providers resolves to the same
	// InternalToolCallID even though the providers' native ToolCallIDs
	// are disjoint — this is the contract the activity pane coalesce and
	// the event-details modal depend on for cross-provider pairing.
	InternalToolCallID string
}

// SpinnerTickMsg is sent periodically to advance the chat spinner animation.
type SpinnerTickMsg struct{}

// SessionViewerTickMsg is sent periodically to refresh the live session viewer
// whilst it is active, so the child session content updates in real-time.
type SessionViewerTickMsg struct{}

// SessionSavedMsg signals completion of an async session save operation.
type SessionSavedMsg struct {
	// Err holds any error that occurred during saving, or nil on success.
	Err error
}

// BackgroundTaskCompletedMsg carries a background task completion notification
// into the Bubble Tea event loop so the planner can be re-triggered.
type BackgroundTaskCompletedMsg struct {
	TaskID      string
	Agent       string
	Description string
	Duration    string
	Status      string
}

// EventBusNotificationMsg bridges event bus notifications into the Bubble Tea
// event loop. Event bus handlers run on arbitrary goroutines; this message
// carries the event payload safely into Update() for processing on the main
// goroutine.
type EventBusNotificationMsg struct {
	ProviderError *events.ProviderErrorEvent
	RateLimited   *events.ProviderEvent
	ToolError     *events.ToolExecuteErrorEvent
}

// SwarmEventAppendedMsg signals that a new entry has been written to the
// swarm event store by a stream worker goroutine and the activity pane
// should re-render. Readers fetch the current snapshot via swarmStore.All()
// so they always see the latest consistent view, independent of delivery
// order.
//
// The ID field carries the appended SwarmEvent.ID for telemetry and for
// tests that want to assert correlation — handlers must not rely on it for
// correctness because the store is the source of truth.
//
// Per the ADR thread-safety contract, the chat intent never mutates the
// activity history slice directly from stream goroutines — producers
// Append to the store under its mutex, then post this message back into
// the Bubble Tea event loop which renders on the main goroutine.
type SwarmEventAppendedMsg struct {
	// ID is the SwarmEvent.ID of the newly appended event. Optional
	// (empty when unavailable, for example when dispatched after a
	// restore loop rather than a single append).
	ID string
}

// tickSpinner returns a Cmd that fires a SpinnerTickMsg after a short delay.
//
// Returns:
//   - A tea.Cmd that sends SpinnerTickMsg after 100ms.
//
// Side effects:
//   - None.
func tickSpinner() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return SpinnerTickMsg{}
	})
}

// tickSessionViewer returns a Cmd that fires a SessionViewerTickMsg after 500ms,
// driving periodic refresh of the live session viewer.
//
// Returns:
//   - A tea.Cmd that sends SessionViewerTickMsg after 500ms.
//
// Side effects:
//   - None.
func tickSessionViewer() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
		return SessionViewerTickMsg{}
	})
}

// ToolPermissionMsg requests user approval for a tool invocation.
//
// P17.S3 extends the request/response shape with a session-scoped
// "remember" signal. Callers send ToolPermissionMsg, the user replies
// with y/n/s, and the intent echoes a ToolPermissionDecision on the
// Response channel. When Decision.Remember is true the intent will
// auto-approve future requests for the same ToolName until the session
// switches (handleSessionLoaded clears the cache).
type ToolPermissionMsg struct {
	// ToolName identifies the tool invocation the user must authorise.
	// Used both as the display label and as the cache key for
	// session-scoped approval memory.
	ToolName string
	// Arguments carry the tool's call parameters so the UI can show
	// the user what they're approving.
	Arguments map[string]interface{}
	// Response is the channel on which the user's decision is
	// published. Callers own the channel and must drain it on every
	// send to avoid deadlocks. The channel type carries a
	// ToolPermissionDecision so callers can tell an "approve once"
	// (Remember=false) from an "approve and remember" (Remember=true).
	Response chan<- ToolPermissionDecision
	// Remember, when already true on the inbound message, signals that
	// an earlier approval has been cached and the prompt must not be
	// shown. In normal use the caller sets it to false and the intent
	// sets it on the outbound ToolPermissionDecision when the user
	// presses 's'. Kept on the request side so callers that bypass the
	// emission check can still round-trip the flag.
	Remember bool
}

// ToolPermissionDecision is the tri-state response to a
// ToolPermissionMsg. Approve/deny live on Approved; the session-scoped
// remember signal lives on Remember so a single channel type covers the
// y/n/s input surface without needing a new enum.
type ToolPermissionDecision struct {
	// Approved is true for 'y' (approve once) and 's' (approve and
	// remember). False for 'n' (deny).
	Approved bool
	// Remember is true only for 's'. When the caller observes a
	// Remember=true approval, it should cache the ToolName as
	// auto-approved for the remainder of the session.
	Remember bool
}

// AppShell abstracts app methods needed by the chat intent.
type AppShell interface {
	// WriteConfig persists the given application configuration.
	WriteConfig(cfg *config.AppConfig) error
	// List returns the names of all registered providers.
	List() []string
	// Get returns the provider with the given name.
	Get(name string) (provider.Provider, error)
}

// SessionLister lists available sessions and manages session metadata.
type SessionLister interface {
	// List returns metadata for all saved sessions, sorted by most recently active first.
	List() []contextpkg.SessionInfo
	// SetTitle updates the title of an existing session.
	SetTitle(sessionID string, title string) error
	// Load retrieves a context store from a saved session.
	Load(sessionID string) (*recall.FileContextStore, error)
	// Save persists the session store to disk with the provided metadata.
	Save(sessionID string, store *recall.FileContextStore, meta contextpkg.SessionMetadata) error
	// Delete removes a session's persisted state (metadata + activity timeline).
	// Missing files are tolerated — Delete is idempotent.
	Delete(sessionID string) error
	// Fork clones an existing session into a new independent session at
	// the supplied pivot message (P18b). An empty pivot produces a full
	// clone — the first-cut fork-at-last semantics.
	Fork(originID, pivotMessageID string) (string, error)
}

// SwarmEventPersister is an optional interface that a SessionLister may
// implement to support persisting and restoring SwarmEvent activity timelines
// alongside session data. The chat intent type-asserts the session store at
// save/load time — implementations that do not support event persistence are
// silently skipped.
type SwarmEventPersister interface {
	// SaveEvents persists events for a session. Empty slices produce no file.
	// Callers migrated to the P4 WAL should prefer AppendEvent for per-event
	// durability and SaveEvents only for close-time compaction.
	SaveEvents(sessionID string, evs []streaming.SwarmEvent) error
	// LoadEvents restores events for a session. Missing files return nil, nil.
	LoadEvents(sessionID string) ([]streaming.SwarmEvent, error)
	// AppendEvent writes a single event to the session's WAL and fsyncs
	// before returning. Implementations must be safe to call from producer
	// goroutines on the streaming hot path.
	AppendEvent(sessionID string, ev streaming.SwarmEvent) error
}

// SessionChildLister lists child sessions visible to the session manager.
type SessionChildLister interface {
	// ChildSessions returns child sessions for the given parent session ID.
	ChildSessions(parentID string) ([]*session.Session, error)
	// AllSessions returns every session that has a parent, regardless of which run created it.
	AllSessions() ([]*session.Session, error)
}

// SessionManager manages active chat sessions and message delivery.
type SessionManager interface {
	// EnsureSession makes sure the session exists for the provided agent.
	EnsureSession(sessionID string, agentID string)
	// SendMessage sends a message to the session and returns streamed chunks.
	SendMessage(ctx context.Context, sessionID string, message string) (<-chan provider.StreamChunk, error)
	// GetSession retrieves a session by ID for ancestry traversal.
	GetSession(id string) (*session.Session, error)
}

// IntentConfig holds the configuration for creating a new chat Intent.
type IntentConfig struct {
	App                AppShell
	Engine             *engine.Engine
	Streamer           Streamer
	SessionManager     SessionManager
	AgentID            string
	SessionID          string
	ProviderName       string
	ModelName          string
	TokenBudget        int
	AgentRegistry      *agent.Registry
	SessionStore       SessionLister
	ModelResolver      contextpkg.ModelResolver // Optional: enables dynamic model context limits
	ChildSessionLister SessionChildLister
}

// Intent handles chat interactions in the TUI.
type Intent struct {
	app                AppShell
	engine             *engine.Engine
	streamer           Streamer
	sessionManager     SessionManager
	agentID            string
	sessionID          string
	input              string
	width              int
	height             int
	statusBar          *layout.StatusBar
	statusIndicator    *widgets.StatusIndicator
	tokenCount         int
	responseTokenCount int
	tokenCounter       contextpkg.TokenCounter
	providerName       string
	modelName          string
	tokenBudget        int
	tickFrame          int
	// turnStartedAt is the wall-clock instant the current streaming
	// turn began (set by beginTurn). On the Done chunk the intent
	// computes time.Since(turnStartedAt) and stamps it onto the view
	// so finaliseChunk seals the duration into the final assistant
	// message. Mirrors view.turnStartedAt; we track it on the intent
	// as well so the duration computation does not depend on the
	// view's state at Done time.
	turnStartedAt time.Time
	// spinnerActive tracks whether the SpinnerTickMsg chain is in flight.
	// Stream-chunk handlers ran tickSpinner() unconditionally on every
	// chunk, which scheduled a fresh 100ms timer goroutine each time.
	// Under fast streams (chunks arriving every 5-50ms) parallel tick
	// chains accumulated, each firing Update+View at 10Hz; after a
	// minute the host process held hundreds of timers and the CPU
	// pegged. ensureSpinnerActive returns tickSpinner() only when the
	// chain is dead, so at most one chain is ever active.
	spinnerActive bool
	streamChan         <-chan provider.StreamChunk
	pendingPermission  *ToolPermissionMsg
	// sessionApprovedTools is the P17.S3 session-scoped approval cache.
	// Populated when the user resolves a prompt with the 's' key
	// (ToolPermissionDecision.Remember=true); consulted by
	// handleToolPermission to auto-approve future requests for the
	// same ToolName without re-prompting. Cleared on session switch
	// via handleSessionLoaded so approvals cannot leak across
	// sessions.
	sessionApprovedTools map[string]struct{}
	result               *tuiintents.IntentResult
	msgViewport          *viewport.Model
	vpReady              bool
	atBottom             bool
	agentRegistry        *agent.Registry
	sessionStore         SessionLister
	childSessionLister   SessionChildLister
	view                 *chat.View
	loadingModal         *feedback.Modal
	errorModal           *feedback.Modal
	notifications        *notification.Component
	notificationManager  notification.Manager
	// activeToolCall holds the name of the currently executing tool call during streaming.
	activeToolCall string
	activeThinking string
	streamCancel   context.CancelFunc
	// streamCtx is the cancellable context for the current streaming
	// producer. Stored alongside streamCancel so readNextChunk can
	// select on ctx.Done() and unblock when the user triggers the
	// double-Esc interrupt (P1/D1). Nil when no stream is active.
	streamCtx context.Context
	// lastEscTime records when Esc was last pressed while streaming, enabling
	// the 500ms double-press window that cancels an active stream.
	lastEscTime time.Time
	// userCancelled is set when the user initiates a stream cancel via the
	// double-Esc interrupt. It tells handleStreamChunk that the subsequent
	// context.Canceled error propagating from the provider is a legitimate
	// user action (not a failure), so the error must not surface as a chat
	// error message. The flag is consumed on the first cancel-related chunk.
	userCancelled  bool
	completionChan <-chan streaming.CompletionNotificationEvent
	// backgroundManager tracks active background delegation tasks.
	backgroundManager *engine.BackgroundTaskManager
	// completionOrchestrator handles re-prompting when all background tasks finish.
	// When set, the TUI delegates re-prompt decisions to the orchestrator and
	// receives re-prompt streams via its subscription channel.
	completionOrchestrator *engine.CompletionOrchestrator
	// rePromptChan receives re-prompt stream channels from the orchestrator.
	rePromptChan <-chan (<-chan provider.StreamChunk)
	// cachedScreenLayout holds the reusable ScreenLayout for View() to avoid allocations.
	cachedScreenLayout    *layout.ScreenLayout
	breadcrumbPath        string
	delegationPickerModal *chat.DelegationPickerModal
	sessionViewport       *viewport.Model
	sessionViewerActive   bool
	sessionViewerID       string
	// eventNotifChan bridges event bus notifications into the Bubble Tea loop.
	eventNotifChan chan EventBusNotificationMsg
	// swarmActivity renders the secondary-pane activity timeline. Instantiated
	// once in NewIntent and reused across View() calls to avoid allocations.
	swarmActivity *swarmactivity.SwarmActivityPane
	// swarmStore holds delegation and tool-call events feeding the activity
	// pane. Thread-safe by construction: stream worker goroutines mutate via
	// Append under the store's mutex; the chat intent reads via All() on the
	// Bubble Tea goroutine inside View() and Update handlers. Re-render is
	// driven by SwarmEventAppendedMsg, not shared-slice mutation.
	swarmStore streaming.SwarmEventStore
	// swarmVisibleTypes is the authoritative per-type visibility filter
	// applied to the activity pane on every render. The chat intent owns
	// this map (P3 A3) so transient filter churn on the pane cannot
	// silently hide non-tool_call event types, and so the P11 Ctrl+T filter
	// cycler has a single place to mutate visibility. Defaulted to
	// all-types-visible at construction. Rebuilt in handleFilterToggle from
	// swarmFilterProfile whenever the user presses Ctrl+T.
	swarmVisibleTypes map[streaming.SwarmEventType]bool
	// swarmFilterProfile tracks which preset the user has cycled to via
	// Ctrl+T (P11). The authoritative swarmVisibleTypes map is derived
	// from this field; callers that mutate swarmVisibleTypes directly are
	// expected to keep it consistent with the profile (or reset to
	// swarmFilterProfileAll, which is the constructor default).
	swarmFilterProfile swarmFilterProfile
	// sessionTrail holds the session-ancestry breadcrumb trail built by
	// walking ParentID links via the session manager. Refreshed on
	// construction and session switch.
	sessionTrail *navigation.SessionTrail
	// turnUserMessage captures the current turn's user prompt so the
	// premature-delegation-misfire detector (P7/C2) can inspect it for
	// @<agent-name> mentions when the first assistant chunk arrives.
	// Cleared on msg.Done so stale prompts from a previous turn cannot
	// leak into detection for a new one.
	turnUserMessage string
	// turnHasText is true once any text content has been emitted for the
	// current assistant turn. The P7/C2 detector only fires when the
	// very first content-bearing chunk is a bare tool_use — if the
	// assistant speaks before tool-calling, the reply is not the
	// misfire pattern.
	turnHasText bool
	// prematureWarningFired gates the P7/C2 warning to at most one
	// notification per user turn so a chain of misfired tool_use chunks
	// does not spam the notification area.
	prematureWarningFired bool
}

var runningInTests bool

// chatHintSuffix is the fixed portion of the chat view's status-bar hint,
// appended to the dynamic status prefix by View(). Extracted as a package
// const so the long-line linter is satisfied without sacrificing
// readability. Ctrl+G: tree was added in Wave 2 / T14b; "toggle activity"
// was shortened to "activity" to keep the line within the 140-column budget.
const chatHintSuffix = "  ·  Alt+Enter: new line" +
	"  ·  Enter: send" +
	"  ·  /models /model /help" +
	"  ·  Ctrl+G: tree" +
	"  ·  Ctrl+E: event" +
	"  ·  Ctrl+T: filter" +
	"  ·  ↑/↓: scroll" +
	"  ·  Ctrl+C: quit"

// tokenCounterFromConfig builds a token counter from the given intent configuration.
//
// Expected:
//   - cfg is a valid IntentConfig; cfg.ModelResolver and cfg.ProviderName are optional.
//
// Returns:
//   - A TokenCounter using the model resolver if available, or a default tiktoken counter.
//
// Side effects:
//   - None.
func tokenCounterFromConfig(cfg IntentConfig) contextpkg.TokenCounter {
	if cfg.ModelResolver != nil && cfg.ProviderName != "" {
		return contextpkg.NewTiktokenCounterWithResolver(cfg.ModelResolver, cfg.ProviderName)
	}
	return contextpkg.NewTiktokenCounter()
}

// defaultSwarmVisibleTypes returns a fresh map with every known
// SwarmEventType marked visible. Used as the initial value of the chat
// intent's swarmVisibleTypes field (P3 A3) and as the reset target for
// the P11 swarmFilterProfileAll preset behind Ctrl+T.
//
// Returns:
//   - A new map keyed by SwarmEventType with all known types set to true.
//
// Side effects:
//   - None.
func defaultSwarmVisibleTypes() map[streaming.SwarmEventType]bool {
	return map[streaming.SwarmEventType]bool{
		streaming.EventDelegation: true,
		streaming.EventToolCall:   true,
		streaming.EventToolResult: true,
		streaming.EventPlan:       true,
		streaming.EventReview:     true,
	}
}

// swarmFilterProfile is the P11 filter preset enum the chat intent cycles
// through each time the user presses Ctrl+T. Profiles are chosen so that no
// state ever leaves every type hidden — the cycle is safe, and the P8
// "All events hidden" recovery hint remains reachable only via programmatic
// mutation of swarmVisibleTypes, not via the keyboard.
type swarmFilterProfile int

const (
	// SwarmFilterProfileAll: shows every SwarmEventType. It is the
	// constructor default and the wrap-around target of the cycle.
	swarmFilterProfileAll swarmFilterProfile = iota
	// SwarmFilterProfileToolsOnly: shows only EventToolCall and
	// EventToolResult — the noisy, high-frequency events. Useful when the
	// user wants to watch what the agent is touching in real time.
	swarmFilterProfileToolsOnly
	// SwarmFilterProfileDelegationsOnly: hides the noisy tool events and
	// leaves only the higher-signal delegation, plan, and review events.
	swarmFilterProfileDelegationsOnly
)

// swarmFilterProfileName returns the human-readable label rendered in the
// activity pane footer when the profile is non-default. Returns the empty
// string for swarmFilterProfileAll so the default state stays visually
// quiet.
//
// Expected:
//   - p is one of the three defined swarmFilterProfile constants.
//
// Returns:
//   - A short, user-facing profile label, or "" for the default profile.
//
// Side effects:
//   - None.
func swarmFilterProfileName(p swarmFilterProfile) string {
	switch p {
	case swarmFilterProfileAll:
		return ""
	case swarmFilterProfileToolsOnly:
		return "Tool calls only"
	case swarmFilterProfileDelegationsOnly:
		return "Delegations + plan + review"
	}
	return ""
}

// swarmVisibleTypesForProfile returns a fresh visibleTypes map shaped by
// the given profile. The returned map is safe for the caller to mutate; no
// shared state is retained.
//
// Expected:
//   - p is one of the three defined swarmFilterProfile constants. Unknown
//     values fall through to the all-visible default so callers cannot
//     accidentally render an all-hidden pane by passing a zero value of a
//     future enum extension.
//
// Returns:
//   - A map keyed by SwarmEventType with booleans reflecting the profile.
//
// Side effects:
//   - None.
func swarmVisibleTypesForProfile(p swarmFilterProfile) map[streaming.SwarmEventType]bool {
	switch p {
	case swarmFilterProfileToolsOnly:
		return map[streaming.SwarmEventType]bool{
			streaming.EventDelegation: false,
			streaming.EventToolCall:   true,
			streaming.EventToolResult: true,
			streaming.EventPlan:       false,
			streaming.EventReview:     false,
		}
	case swarmFilterProfileDelegationsOnly:
		return map[streaming.SwarmEventType]bool{
			streaming.EventDelegation: true,
			streaming.EventToolCall:   false,
			streaming.EventToolResult: false,
			streaming.EventPlan:       true,
			streaming.EventReview:     true,
		}
	default:
		// swarmFilterProfileAll and any unknown enum extension fall back
		// to the all-visible map so callers cannot accidentally hide
		// every type by passing a zero or out-of-range value.
		return defaultSwarmVisibleTypes()
	}
}

// nextSwarmFilterProfile returns the profile that follows p in the P11
// cycle: all -> toolsOnly -> delegationsOnly -> all.
//
// Expected:
//   - p is one of the three defined swarmFilterProfile constants. Unknown
//     values wrap to the all-visible default so the cycle is self-healing.
//
// Returns:
//   - The next profile in the cycle.
//
// Side effects:
//   - None.
func nextSwarmFilterProfile(p swarmFilterProfile) swarmFilterProfile {
	switch p {
	case swarmFilterProfileAll:
		return swarmFilterProfileToolsOnly
	case swarmFilterProfileToolsOnly:
		return swarmFilterProfileDelegationsOnly
	case swarmFilterProfileDelegationsOnly:
		return swarmFilterProfileAll
	}
	return swarmFilterProfileAll
}

// NewIntent creates a new chat Intent from the given configuration.
//
// Expected:
//   - cfg.Engine is a non-nil Engine instance.
//   - cfg.AgentID and cfg.SessionID are non-empty strings.
//   - cfg.ProviderName and cfg.ModelName identify the active provider and model.
//   - cfg.TokenBudget is the maximum token allocation for the session.
//
// Returns:
//   - An initialised Intent with default dimensions (80x24) and a configured StatusBar.
//
// Side effects:
//   - None.
func NewIntent(cfg IntentConfig) *Intent {
	sb := layout.NewStatusBar(80)
	sb.Update(layout.StatusBarMsg{
		Provider:    cfg.ProviderName,
		Model:       cfg.ModelName,
		AgentID:     cfg.AgentID,
		TokensUsed:  0,
		TokenBudget: cfg.TokenBudget,
	})

	notifManager := notification.NewInMemoryManager()
	if cfg.Engine != nil {
		cfg.Engine.SetModelPreference(cfg.ProviderName, cfg.ModelName)
	}

	intent := &Intent{
		app:                  cfg.App,
		engine:               cfg.Engine,
		streamer:             cfg.Streamer,
		sessionManager:       cfg.SessionManager,
		agentID:              cfg.AgentID,
		sessionID:            cfg.SessionID,
		input:                "",
		width:                80,
		height:               24,
		statusBar:            sb,
		statusIndicator:      widgets.NewStatusIndicator(nil),
		tokenCount:           0,
		tokenCounter:         tokenCounterFromConfig(cfg),
		providerName:         cfg.ProviderName,
		modelName:            cfg.ModelName,
		tokenBudget:          cfg.TokenBudget,
		tickFrame:            0,
		result:               nil,
		atBottom:             true,
		agentRegistry:        cfg.AgentRegistry,
		sessionStore:         cfg.SessionStore,
		childSessionLister:   cfg.ChildSessionLister,
		view:                 chat.NewView(),
		notifications:        notification.NewComponent(notifManager),
		notificationManager:  notifManager,
		breadcrumbPath:       "Chat",
		swarmActivity:        swarmactivity.NewSwarmActivityPane(),
		swarmStore:           buildSwarmStore(cfg.SessionStore, cfg.SessionID),
		swarmVisibleTypes:    defaultSwarmVisibleTypes(),
		swarmFilterProfile:   swarmFilterProfileAll,
		sessionTrail:         navigation.NewSessionTrail(),
		sessionApprovedTools: map[string]struct{}{},
	}
	intent.refreshSessionTrail()
	return intent
}

// buildSwarmStore constructs the per-intent SwarmEventStore. When the
// session store satisfies SwarmEventPersister, the returned store is a
// write-through decorator that persists every Append to the session's
// JSONL WAL (P4). Otherwise a plain in-memory store is returned so tests
// and embedded callers without a persister continue to work.
//
// Expected:
//   - sessionStore may be nil or any SessionLister implementation; only
//     implementations that also satisfy SwarmEventPersister get the WAL
//     wrapper.
//   - sessionID may be empty; the wrapper closes over it so appends routed
//     after a session rename go to the original file (rename is out of
//     scope for P4).
//
// Returns:
//   - A SwarmEventStore ready for use by the chat intent.
//
// Side effects:
//   - None until Append/All/Clear is invoked.
func buildSwarmStore(sessionStore SessionLister, sessionID string) streaming.SwarmEventStore {
	mem := streaming.NewMemorySwarmStore(streaming.DefaultSwarmStoreCapacity)
	ep, ok := sessionStore.(SwarmEventPersister)
	if !ok || sessionID == "" {
		return mem
	}
	appendFn := func(ev streaming.SwarmEvent) error {
		return ep.AppendEvent(sessionID, ev)
	}
	return streaming.NewPersistedSwarmStore(mem, appendFn)
}

// SetCompletionChannel attaches a channel that receives background task completion
// notifications. The chat intent listens on this channel via a tea.Cmd and
// re-triggers the planner when a notification arrives.
//
// Expected:
//   - ch is a buffered channel or nil to disable notifications.
//
// Side effects:
//   - Stores the channel reference for use in Init().
func (i *Intent) SetCompletionChannel(ch <-chan streaming.CompletionNotificationEvent) {
	i.completionChan = ch
}

// SetBackgroundManager attaches the background task manager for tracking active delegations.
//
// Expected:
//   - mgr is a non-nil BackgroundTaskManager from the core app.
//
// Returns:
//   - None.
//
// Side effects:
//   - Stores the manager reference on the intent.
func (i *Intent) SetBackgroundManager(mgr *engine.BackgroundTaskManager) {
	i.backgroundManager = mgr
}

// SetCompletionOrchestrator attaches the completion orchestrator for
// engine-level re-prompting. When set, the TUI subscribes to the
// orchestrator's re-prompt channel and no longer triggers re-prompts itself.
//
// Expected:
//   - orch is a non-nil CompletionOrchestrator that has been started.
//
// Returns:
//   - None.
//
// Side effects:
//   - Stores the orchestrator reference and subscribes for re-prompt streams.
func (i *Intent) SetCompletionOrchestrator(orch *engine.CompletionOrchestrator) {
	i.completionOrchestrator = orch
	if orch != nil && i.sessionID != "" {
		i.rePromptChan = orch.SubscribeRePrompt(i.sessionID)
	}
}

// Init returns the initial command for the intent.
//
// Returns:
//   - A tea.Cmd that starts the spinner tick loop.
//
// Side effects:
//   - Schedules the first SpinnerTickMsg.
func (i *Intent) Init() tea.Cmd {
	i.syncViewAgentMeta()
	if i.sessionID != "" && i.sessionStore != nil {
		if store, err := i.sessionStore.Load(i.sessionID); err == nil {
			i.handleSessionLoaded(sessionbrowser.SessionLoadedMsg{SessionID: i.sessionID, Store: store})
		}
	}

	if runningInTests {
		return nil
	}
	cmds := []tea.Cmd{tickSpinner(), i.notifications.Init()}
	if i.engine != nil && i.engine.EventBus() != nil {
		i.subscribeToFailoverEvents()
		cmds = append(cmds, i.waitForEventBusNotification())
	}
	if i.completionChan != nil {
		cmds = append(cmds, i.waitForCompletion())
	}
	return tea.Batch(cmds...)
}

// subscribeToFailoverEvents subscribes to provider and tool error events on the
// engine event bus, pushing them onto the eventNotifChan channel. A companion
// tea.Cmd (waitForEventBusNotification) reads from the channel and delivers
// EventBusNotificationMsg into the Bubble Tea Update loop, ensuring all
// notification state mutations happen on the main goroutine.
//
// Expected:
//   - i.engine is initialised with a non-nil event bus.
//   - i.sessionID is the current session identifier.
//
// Side effects:
//   - Creates eventNotifChan.
//   - Subscribes event handlers to the engine event bus.
func (i *Intent) subscribeToFailoverEvents() {
	if i.engine == nil || i.engine.EventBus() == nil {
		return
	}

	i.eventNotifChan = make(chan EventBusNotificationMsg, 16)
	bus := i.engine.EventBus()

	bus.Subscribe(events.EventProviderError, func(msg any) {
		evt, ok := msg.(*events.ProviderErrorEvent)
		if !ok || evt.Data.SessionID != i.sessionID {
			return
		}
		select {
		case i.eventNotifChan <- EventBusNotificationMsg{ProviderError: evt}:
		default:
		}
	})

	bus.Subscribe(events.EventProviderRateLimited, func(msg any) {
		evt, ok := msg.(*events.ProviderEvent)
		if !ok || evt.Data.SessionID != i.sessionID {
			return
		}
		select {
		case i.eventNotifChan <- EventBusNotificationMsg{RateLimited: evt}:
		default:
		}
	})

	bus.Subscribe(events.EventToolExecuteError, func(msg any) {
		evt, ok := msg.(*events.ToolExecuteErrorEvent)
		if !ok || evt.Data.SessionID != i.sessionID {
			return
		}
		select {
		case i.eventNotifChan <- EventBusNotificationMsg{ToolError: evt}:
		default:
		}
	})
}

// waitForEventBusNotification returns a tea.Cmd that blocks until an event bus
// notification arrives on the channel, converting it into an
// EventBusNotificationMsg for the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd that blocks on the eventNotifChan channel.
//
// Side effects:
//   - None until the returned Cmd is executed by the Bubble Tea runtime.
func (i *Intent) waitForEventBusNotification() tea.Cmd {
	ch := i.eventNotifChan
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// handleEventBusNotification processes an event bus notification on the main
// goroutine, delegating to the notification Component and refreshing the
// viewport so the notification renders immediately.
//
// Expected:
//   - msg contains exactly one non-nil event payload.
//
// Returns:
//   - A tea.Cmd that re-enqueues the wait for the next event bus notification.
//
// Side effects:
//   - Adds a notification via the Component.
//   - Refreshes the viewport.
func (i *Intent) handleEventBusNotification(msg EventBusNotificationMsg) tea.Cmd {
	switch {
	case msg.ProviderError != nil:
		i.notifications.AddProviderErrorNotification(msg.ProviderError)
	case msg.RateLimited != nil:
		i.notifications.AddProviderRateLimitedNotification(msg.RateLimited)
	case msg.ToolError != nil:
		i.notifications.AddToolExecuteErrorNotification(msg.ToolError)
	}
	i.refreshViewport()
	return i.waitForEventBusNotification()
}

// Update processes a Bubble Tea message and returns any command to execute.
//
// Expected:
//   - msg is a tea.Msg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd to execute, or nil if no command is needed.
//
// Side effects:
//   - Updates terminal dimensions on WindowSizeMsg.
//   - Accumulates token count on StreamChunkMsg.
//   - Delegates to handleKeyMsg for key events.
//   - Delegates to msgViewport for mouse events.
func (i *Intent) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return i.handleKeyMsg(msg)
	case tea.MouseMsg:
		return i.handleMouseMsg(msg)
	case tea.WindowSizeMsg:
		return i.handleWindowSize(msg)
	case StreamChunkMsg:
		return i.handleStreamChunkMsg(msg)
	case ToolPermissionMsg:
		i.handleToolPermission(msg)
		return nil
	case ExternalEditorFinishedMsg:
		return i.handleExternalEditorFinished(msg)
	case SessionSavedMsg:
		return nil
	case SpinnerTickMsg:
		return i.handleSpinnerTick()
	case SessionViewerTickMsg:
		return i.handleSessionViewerTick()
	case notification.TickMsg:
		return i.notifications.Update(msg)
	}
	return i.handleNavigationMsg(msg)
}

// handleNavigationMsg consumes session-navigation and background-task
// messages that do not merit their own slot in the top-level Update
// switch. Split out to keep Update's cyclomatic complexity within the
// project's gocyclo budget after P17.S1 added ExternalEditorFinishedMsg.
//
// Expected:
//   - msg is any tea.Msg that did not match one of the high-frequency
//     cases handled inline in Update.
//
// Returns:
//   - A tea.Cmd from the corresponding sub-handler, or whatever
//     handleMiscMsg returns when no navigation case matches.
//
// Side effects:
//   - Whatever side effects the delegated handler produces.
func (i *Intent) handleNavigationMsg(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case sessionbrowser.SessionSelectedMsg:
		return i.handleSessionResult(msg)
	case sessionbrowser.SessionLoadedMsg:
		return i.handleSessionLoaded(msg)
	case sessionbrowser.SessionDeletedMsg:
		return i.handleSessionDeleted(msg)
	case sessionbrowser.SessionForkedMsg:
		return i.handleSessionForked(msg)
	case sessiontree.SelectedMsg:
		return i.handleSessionTreeSelection(msg)
	case BackgroundTaskCompletedMsg:
		return i.handleBackgroundTaskCompleted(msg)
	}
	return i.handleMiscMsg(msg)
}

// handleSwarmEventAppended acknowledges a newly-stored swarm event. The
// pane re-reads swarmStore.All() in View() so no state mutation is
// required here; returning from Update is sufficient to trigger Bubble
// Tea's next render cycle.
//
// Returns:
//   - A nil tea.Cmd; no follow-up work is required.
//
// Side effects:
//   - None; the store was mutated by the producer goroutine.
func (i *Intent) handleSwarmEventAppended() tea.Cmd {
	return nil
}

// handleMiscMsg dispatches low-frequency messages that do not warrant
// individual cases in the main Update switch, keeping its cyclomatic
// complexity within the project's gocyclo budget.
//
// Expected:
//   - msg is a tea.Msg that was not matched by Update's type switch.
//
// Returns:
//   - A tea.Cmd from the matched handler, or nil for unrecognised messages.
//
// Side effects:
//   - Depends on the concrete message type handled.
func (i *Intent) handleMiscMsg(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case EventBusNotificationMsg:
		return i.handleEventBusNotification(m)
	case SwarmEventAppendedMsg:
		return i.handleSwarmEventAppended()
	}
	return nil
}

// handleSpinnerTick advances spinner animations for streaming and loading states.
//
// Returns:
//   - A tea.Cmd to schedule the next tick if animations are active, or nil.
//
// Side effects:
//   - Advances the tick frame and spinner animations.
func (i *Intent) handleSpinnerTick() tea.Cmd {
	if i.view.IsStreaming() {
		i.tickFrame++
		i.view.SetTickFrame(i.tickFrame)
	}
	if i.loadingModal != nil {
		i.loadingModal.AdvanceSpinner()
	}
	if i.view.IsStreaming() || i.loadingModal != nil {
		// Chain stays alive — spinnerActive must be true so concurrent
		// callers (chunk handlers, modal openers) don't kick off a
		// second chain via ensureSpinnerActive.
		i.spinnerActive = true
		return tickSpinner()
	}
	// Nothing left to animate; the chain dies. Mark the slot free so the
	// next stream-start (or modal-open) reschedules a single fresh tick.
	i.spinnerActive = false
	return nil
}

// ensureSpinnerActive returns a tickSpinner Cmd only when no spinner
// chain is currently in flight. Stream-chunk handlers and loading-
// modal entry points call this in place of tickSpinner() directly so
// at most one 100ms timer chain runs at any given moment, regardless
// of how many chunks arrive.
//
// Expected:
//   - Caller intends to drive the spinner (streaming, loading, etc.).
//
// Returns:
//   - tickSpinner() when no chain is active (and marks spinnerActive
//     true).
//   - nil when a chain is already running; caller batches the nil
//     into its tea.Cmd return without effect.
//
// Side effects:
//   - Mutates i.spinnerActive.
func (i *Intent) ensureSpinnerActive() tea.Cmd {
	if i.spinnerActive {
		return nil
	}
	i.spinnerActive = true
	return tickSpinner()
}

// handleSessionViewerTick refreshes the live session viewer with the latest
// child session content and schedules the next tick.
//
// Returns:
//   - A tea.Cmd to schedule the next tick, or nil when the viewer is no longer active.
//
// Side effects:
//   - Updates sessionViewport content and scrolls to the bottom on each tick.
func (i *Intent) handleSessionViewerTick() tea.Cmd {
	if !i.sessionViewerActive || i.sessionViewport == nil || i.childSessionLister == nil {
		return nil
	}
	sessions, err := i.childSessionLister.ChildSessions(i.sessionID)
	if err != nil {
		return tickSessionViewer()
	}
	for _, sess := range sessions {
		if sess.ID == i.sessionViewerID {
			content := i.renderSessionContent(sess)
			i.sessionViewport.SetContent(content)
			i.sessionViewport.GotoBottom()
			break
		}
	}
	return tickSessionViewer()
}

// waitForCompletion returns a tea.Cmd that blocks until a background task
// completion notification arrives on the completion channel, then converts it
// to a BackgroundTaskCompletedMsg for the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd that blocks on the completion channel.
//
// Side effects:
//   - None until the returned Cmd is executed by the Bubble Tea runtime.
func (i *Intent) waitForCompletion() tea.Cmd {
	ch := i.completionChan
	return func() tea.Msg {
		notif, ok := <-ch
		if !ok {
			return nil
		}
		return BackgroundTaskCompletedMsg{
			TaskID:      notif.TaskID,
			Agent:       notif.Agent,
			Description: notif.Description,
			Duration:    notif.Duration.String(),
			Status:      notif.Status,
		}
	}
}

// handleBackgroundTaskCompleted records a completed background task and
// re-triggers the planner only when all delegated tasks have finished.
//
// Expected:
//   - msg contains task completion details.
//
// Returns:
//   - A tea.Cmd that waits for further completions and optionally starts a new stream.
//
// Side effects:
//   - Adds a system message to the chat view.
//   - Starts a new LLM stream when no background tasks remain.
func (i *Intent) handleBackgroundTaskCompleted(msg BackgroundTaskCompletedMsg) tea.Cmd {
	reminder := formatCompletionReminder(msg)
	i.view.AddMessage(chat.Message{Role: "system", Content: reminder})
	i.atBottom = true
	i.refreshViewport()

	var cmds []tea.Cmd

	if i.completionChan != nil {
		cmds = append(cmds, i.waitForCompletion())
	}

	allDone := i.backgroundManager == nil || i.backgroundManager.ActiveCount() == 0
	if allDone {
		i.view.StartStreaming()
		i.cancelActiveStream()
		ctx, cancel := context.WithCancel(context.Background())
		i.streamCancel = cancel
		i.streamCtx = ctx
		cmds = append(cmds, func() tea.Msg {
			var stream <-chan provider.StreamChunk
			var err error
			if i.sessionManager != nil {
				i.sessionManager.EnsureSession(i.sessionID, i.agentID)
				stream, err = i.sessionManager.SendMessage(ctx, i.sessionID, reminder)
			} else {
				stream, err = i.streamer.Stream(ctx, i.agentID, reminder)
			}
			if err != nil {
				return StreamChunkMsg{Content: "", Error: err, Done: true}
			}
			return i.readNextChunkFrom(stream)
		})
	}

	return tea.Batch(cmds...)
}

// formatCompletionReminder builds the system-reminder message for a completed
// background task. Delegates to the shared engine.FormatCompletionReminder so
// that all consumers (TUI, CLI, API) produce identical reminder text.
//
// Expected:
//   - msg contains task completion details.
//
// Returns:
//   - A formatted system-reminder string for the planner.
//
// Side effects:
//   - None.
func formatCompletionReminder(msg BackgroundTaskCompletedMsg) string {
	return engine.FormatCompletionReminder(msg.TaskID, msg.Agent, msg.Duration)
}

// handleDelegationKeyMsg processes keyboard input for the delegation picker modal.
//
// Expected:
//   - msg is a tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd to execute, or nil if no command is needed.
//
// Side effects:
//   - Updates modal state or sets sessionViewerModal on Enter.
func (i *Intent) handleDelegationKeyMsg(msg tea.KeyMsg) tea.Cmd {
	if i.delegationPickerModal == nil {
		return nil
	}
	switch msg.String() {
	case "esc":
		i.delegationPickerModal = nil
	case "up", "k":
		i.delegationPickerModal.MoveUp()
	case "down", "j":
		i.delegationPickerModal.MoveDown()
	case "enter":
		if sel := i.delegationPickerModal.Selected(); sel != nil {
			i.delegationPickerModal = nil
			content := i.renderSessionContent(sel)
			svpHeight := i.height - 6
			if svpHeight < 1 {
				svpHeight = 1
			}
			vp := viewport.New(i.width, svpHeight)
			vp.SetContent(content)
			vp.GotoBottom()
			i.sessionViewport = &vp
			i.sessionViewerActive = true
			i.sessionViewerID = sel.ID
			i.breadcrumbPath = "Chat > " + sel.ID[:8]
			i.cachedScreenLayout = nil
			return tickSessionViewer()
		}
	}
	return nil
}

// handleSessionViewerKeyMsg processes keyboard input for the session viewer.
//
// Expected:
//   - msg is a tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd and true if the key was consumed, or nil and false otherwise.
//
// Side effects:
//   - Closes session viewer on Esc; forwards scroll keys to sessionViewport.
func (i *Intent) handleSessionViewerKeyMsg(msg tea.KeyMsg) (tea.Cmd, bool) {
	if !i.sessionViewerActive || i.sessionViewport == nil {
		return nil, false
	}
	if msg.String() == "esc" {
		i.sessionViewerActive = false
		i.sessionViewport = nil
		i.sessionViewerID = ""
		i.breadcrumbPath = "Chat"
		i.cachedScreenLayout = nil
		return nil, true
	}
	switch msg.Type {
	case tea.KeyPgUp, tea.KeyPgDown, tea.KeyUp, tea.KeyDown, tea.KeyHome, tea.KeyEnd:
		vp, cmd := i.sessionViewport.Update(msg)
		i.sessionViewport = &vp
		return cmd, true
	}
	switch msg.String() {
	case "k":
		vp, cmd := i.sessionViewport.Update(tea.KeyMsg{Type: tea.KeyUp})
		i.sessionViewport = &vp
		return cmd, true
	case "j":
		vp, cmd := i.sessionViewport.Update(tea.KeyMsg{Type: tea.KeyDown})
		i.sessionViewport = &vp
		return cmd, true
	}
	return nil, true
}

// handleModalKeyMsg processes keyboard input when a feedback modal is active.
//
// Expected:
//   - msg is a tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - true if a modal consumed the input, false otherwise.
//
// Side effects:
//   - Dismisses error modal on Esc or Enter.
func (i *Intent) handleModalKeyMsg(msg tea.KeyMsg) bool {
	if i.errorModal != nil {
		if msg.Type == tea.KeyEsc || msg.Type == tea.KeyEnter {
			i.errorModal = nil
		}
		return true
	}
	return i.loadingModal != nil
}

// handleScrollKey processes viewport scroll keys when the viewport is ready.
//
// Expected:
//   - msg is a tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd and true if the key was a scroll key, or nil and false otherwise.
//
// Side effects:
//   - Updates the viewport position on scroll keys.
//   - Updates atBottom flag based on new scroll position.
func (i *Intent) handleScrollKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if !i.vpReady {
		return nil, false
	}
	switch msg.Type {
	case tea.KeyPgUp, tea.KeyPgDown, tea.KeyUp, tea.KeyDown, tea.KeyHome, tea.KeyEnd:
		var cmd tea.Cmd
		vp, cmd := i.msgViewport.Update(msg)
		i.msgViewport = &vp
		i.atBottom = vp.AtBottom()
		return cmd, true
	}
	return nil, false
}

// handleMouseMsg processes mouse events, delegating to viewport for scroll wheel support.
//
// Expected:
//   - msg is a tea.MouseMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd from the viewport's Update method, or nil if viewport not ready.
//
// Side effects:
//   - Updates the viewport position on mouse wheel events.
//   - Updates atBottom flag based on new scroll position.
//   - Toggles active delegation block on left click.
func (i *Intent) handleMouseMsg(msg tea.MouseMsg) tea.Cmd {
	if i.sessionViewerActive && i.sessionViewport != nil {
		vp, cmd := i.sessionViewport.Update(msg)
		i.sessionViewport = &vp
		return cmd
	}
	if !i.vpReady || i.msgViewport == nil {
		return nil
	}
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
		i.view.ToggleActiveDelegationBlock()
	}
	vp, cmd := i.msgViewport.Update(msg)
	i.msgViewport = &vp
	i.atBottom = vp.AtBottom()
	return cmd
}

// handleWindowSize initialises or resizes the viewport when the terminal size changes.
//
// Expected:
//   - msg is a tea.WindowSizeMsg from the Bubble Tea event loop.
//
// Returns:
//   - nil (window sizing doesn't produce commands).
//
// Side effects:
//   - Creates or updates msgViewport dimensions.
//   - Caches screen layout information.
func (i *Intent) handleWindowSize(msg tea.WindowSizeMsg) tea.Cmd {
	i.width = msg.Width
	i.height = msg.Height
	i.notifications.SetWidth(msg.Width)
	vpHeight := i.computeViewportHeight()
	if !i.vpReady {
		vp := viewport.New(msg.Width, vpHeight)
		i.msgViewport = &vp
		i.msgViewport.SetContent("")
		i.vpReady = true
	} else {
		i.msgViewport.Width = msg.Width
		i.msgViewport.Height = vpHeight
	}
	if i.sessionViewerActive && i.sessionViewport != nil {
		svpHeight := msg.Height - 6
		if svpHeight < 1 {
			svpHeight = 1
		}
		i.sessionViewport.Width = msg.Width
		i.sessionViewport.Height = svpHeight
	}
	i.cachedScreenLayout = layout.NewScreenLayout(&terminal.Info{Width: msg.Width, Height: msg.Height}).
		WithBreadcrumbs("Chat").
		WithFooterSeparator(true)
	return nil
}

// handleKeyMsg processes keyboard input directly without mode switching.
//
// Expected:
//   - msg is a tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd to execute, or nil if no command is needed.
//
// Side effects:
//   - Updates input or returns a quit command based on key input.
func (i *Intent) handleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	if i.sessionViewerActive {
		cmd, _ := i.handleSessionViewerKeyMsg(msg)
		return cmd
	}
	if i.delegationPickerModal != nil {
		return i.handleDelegationKeyMsg(msg)
	}
	if i.handleModalKeyMsg(msg) {
		return nil
	}
	if i.pendingPermission != nil {
		return i.handlePermissionKey(msg)
	}
	if cmd, handled := i.handleScrollKey(msg); handled {
		return cmd
	}
	return i.handleInputKey(msg)
}

// handleInputKey processes keyboard input for text input and control commands.
//
// Expected:
//   - msg is a tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd to execute, or nil if no command is needed.
//
// Side effects:
//   - Updates i.input on typing keys.
func (i *Intent) handleInputKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		return i.handleEscapeKey()
	case tea.KeyCtrlC:
		i.cancelActiveStream()
		return tea.Sequence(i.saveSession(), tea.Quit)
	case tea.KeyCtrlD:
		return i.openDelegationPicker()
	case tea.KeyTab:
		return i.toggleAgent()
	case tea.KeyCtrlA:
		return i.openAgentPicker()
	case tea.KeyCtrlP:
		return i.openModelSelector()
	case tea.KeyCtrlS:
		return i.openSessionBrowser()
	case tea.KeyCtrlT:
		return i.handleFilterToggle()
	case tea.KeyCtrlG:
		return i.openSessionTree()
	case tea.KeyCtrlE:
		return i.openEventDetails()
	case tea.KeyCtrlX:
		return i.openExternalEditor()
	case tea.KeyCtrlK:
		return i.cancelActiveTool()
	}
	return i.handleTextInputKey(msg)
}

// handleTextInputKey processes keys that mutate the input buffer directly
// (backspace, enter, space, printable runes). Split out of handleInputKey
// to keep its cyclomatic complexity within the project's gocyclo budget.
//
// Expected:
//   - msg is a tea.KeyMsg whose Type did not match a command-returning key
//     in handleInputKey.
//
// Returns:
//   - A tea.Cmd when Enter submits a non-empty message; otherwise nil.
//
// Side effects:
//   - Mutates i.input; may call i.updateViewportForInput on Alt+Enter.
func (i *Intent) handleTextInputKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyBackspace:
		if i.input != "" {
			i.input = i.input[:len(i.input)-1]
		}
		return nil
	case tea.KeyEnter:
		if msg.Alt {
			i.input += "\n"
			i.updateViewportForInput()
			return nil
		}
		if i.input != "" {
			return i.sendMessage()
		}
		return nil
	case tea.KeySpace:
		i.input += " "
		return nil
	case tea.KeyRunes:
		i.input += string(msg.Runes)
		return nil
	}
	return nil
}

// handleFilterToggle advances the chat intent's swarmFilterProfile to the
// next preset in the P11 cycle and rebuilds swarmVisibleTypes from the new
// profile. View() reasserts the map on every render so the change is
// picked up on the next Bubble Tea tick without any additional plumbing.
//
// The cycle is designed to skip any all-hidden state so the user cannot
// accidentally blank the timeline by mashing Ctrl+T. The P8 "All events
// hidden" recovery hint remains reachable only via programmatic visibility
// mutation, which preserves its role as a safety net rather than a normal
// keyboard state.
//
// Returns:
//   - Always nil; Bubble Tea re-renders on the next tick.
//
// Side effects:
//   - Mutates i.swarmFilterProfile and i.swarmVisibleTypes.
func (i *Intent) handleFilterToggle() tea.Cmd {
	i.swarmFilterProfile = nextSwarmFilterProfile(i.swarmFilterProfile)
	i.swarmVisibleTypes = swarmVisibleTypesForProfile(i.swarmFilterProfile)
	return nil
}

// inputLineCount returns the number of lines in the current input.
//
// Returns:
//   - The count of lines (1 for empty/single-line input, more for multiline).
//
// Side effects:
//   - None.
func (i *Intent) inputLineCount() int {
	return strings.Count(i.input, "\n") + 1
}

// notificationHeight returns the number of terminal lines occupied by the
// active notification overlay, including the trailing newline separator.
//
// Returns:
//   - 0 when no notifications are active, or the line count plus one otherwise.
//
// Side effects:
//   - None.
func (i *Intent) notificationHeight() int {
	view := i.notifications.View()
	if view == "" {
		return 0
	}
	return lipgloss.Height(view) + 1
}

// computeViewportHeight returns the viewport height that accounts for the
// footer, multiline input, and any active notification overlay.
//
// Returns:
//   - The number of lines available for the message viewport (minimum 1).
//
// Side effects:
//   - None.
func (i *Intent) computeViewportHeight() int {
	extraLines := i.inputLineCount() - 1
	footerHeight := 8 + extraLines
	vpHeight := i.height - footerHeight - i.notificationHeight() - i.sessionTrailHeight()
	if vpHeight < 1 {
		vpHeight = 1
	}
	return vpHeight
}

// sessionTrailHeight returns the number of rows consumed by the session trail
// header. Returns 1 when the trail renders non-empty content, 0 otherwise.
//
// Expected:
//   - i.sessionTrail is non-nil (guaranteed by NewIntent).
//
// Returns:
//   - 1 when the trail has renderable items, 0 when empty.
//
// Side effects:
//   - None.
func (i *Intent) sessionTrailHeight() int {
	if i.sessionTrail == nil || len(i.sessionTrail.Items()) == 0 {
		return 0
	}
	return 1
}

// dualPanePrimaryWidth mirrors the 70/30 split from layout.splitPaneWidths
// (which is unexported). When the secondary pane is visible and the terminal
// is at least 80 columns wide, the primary content gets 70% of the available
// width minus the separator column. Otherwise the full terminal width is used.
const chatDualPaneMinWidth = 80

// renderSessionTrailLine renders the session trail breadcrumb styled faint.
// Returns an empty string when the trail is empty, so the caller can skip
// the header row entirely.
//
// Expected:
//   - i.sessionTrail is non-nil.
//   - i.width reflects the current terminal width.
//
// Returns:
//   - A styled trail string, or "" when the trail is empty.
//
// Side effects:
//   - None.
func (i *Intent) renderSessionTrailLine() string {
	if i.sessionTrail == nil {
		return ""
	}

	primaryWidth := i.width
	if i.width >= chatDualPaneMinWidth {
		available := i.width - 1
		primaryWidth = (available * 7) / 10
	}

	trail := i.sessionTrail.Render(primaryWidth)
	if trail == "" {
		return ""
	}

	return lipgloss.NewStyle().Faint(true).Render(trail)
}

// updateViewportForInput adjusts the viewport height to account for multiline
// input and active notifications.
//
// Side effects:
//   - Updates msgViewport.Height.
func (i *Intent) updateViewportForInput() {
	if !i.vpReady {
		return
	}
	i.msgViewport.Height = i.computeViewportHeight()
}

// formatStreamError decides whether a stream-chunk error should be surfaced to
// the user and, if so, returns the pre-formatted display string. A
// user-initiated cancel (double-Esc) is a legitimate action, not an error:
// when the pending userCancelled flag is set and the error wraps
// context.Canceled, the flag is consumed and an empty string is returned so
// handleStreamChunk does not append an error artefact to the chat. Any other
// error — including an upstream context.Canceled from a provider deadline — is
// formatted normally and, when the classifier marks it critical, emitted via
// slog.Error so operators inspecting structured logs see the condition even
// after the TUI exits (P18a S3).
//
// Expected:
//   - err may be nil (no error chunk), context.Canceled (user cancel or
//     upstream cancel), or any other provider error.
//
// Returns:
//   - Empty string when the error was a user-initiated cancel (suppressed)
//     or when err is nil. Otherwise the formatted error message for display.
//
// Side effects:
//   - Clears i.userCancelled when a user-initiated cancel is consumed.
//   - Emits a slog.Error record for critical-severity errors (P18a).
func (i *Intent) formatStreamError(err error) string {
	if err == nil {
		return ""
	}
	if i.userCancelled && errors.Is(err, context.Canceled) {
		i.userCancelled = false
		return ""
	}
	se := provider.ClassifyStreamError(err)
	if se != nil && se.Severity == provider.SeverityCritical {
		slog.Error(
			"stream critical error",
			"provider", se.Provider,
			"err", se.Err,
			"severity", se.Severity.String(),
		)
	}
	return chat.FormatErrorMessage(err)
}

// shouldRefreshViewport reports whether the streamed chunk causes a
// visible change in the message viewport. Empty heartbeat-style chunks
// (no content, no tool change, no done flag, no pending tool-call
// commit) skip the refresh — a no-op refresh re-renders the entire
// message slice through markdown + lipgloss styling, which is
// expensive and shows up in profiles during streaming. Status-bar
// updates flow through Bubble Tea's View() path, not refreshViewport,
// so syncStatusBar work is unaffected by the skip.
//
// Expected:
//   - msg is the incoming StreamChunkMsg.
//   - i.activeToolCall reflects state set by an earlier chunk (a
//     pending tool_call commit that THIS chunk will trigger via
//     handleStreamChunk's commitToolCall).
//
// Returns:
//   - true when the chunk affects the message viewport's contents.
//
// Side effects:
//   - None (pure inspection).
func (i *Intent) shouldRefreshViewport(msg StreamChunkMsg) bool {
	if msg.Done || msg.Error != nil || msg.DelegationInfo != nil {
		return true
	}
	if msg.Content != "" || msg.Thinking != "" {
		return true
	}
	if msg.ToolCallName != "" || msg.ToolStatus != "" || msg.ToolResult != "" {
		return true
	}
	// A previously-stored activeToolCall commits via commitToolCall on
	// the next non-tool-name chunk. Refresh so the committed message
	// becomes visible.
	if i.activeToolCall != "" {
		return true
	}
	return false
}

// handleStreamChunk processes a streaming response chunk.
//
// Expected:
//   - msg is a StreamChunkMsg with content from the provider stream.
//
// Side effects:
//   - Delegates to view.HandleChunk for streaming state management.
//   - Counts tokens and updates the StatusBar.
func (i *Intent) handleStreamChunk(msg StreamChunkMsg) {
	errMsg := i.formatStreamError(msg.Error)

	// Stamp the turn duration onto the view BEFORE the Done path
	// runs HandleChunk → finaliseChunk; finaliseChunk reads it from
	// view.turnDuration when constructing the final assistant
	// Message so the footer shows model + actual elapsed time
	// instead of "0ms".
	if msg.Done && !i.turnStartedAt.IsZero() {
		i.view.SetTurnDuration(time.Since(i.turnStartedAt))
		i.turnStartedAt = time.Time{}
	}

	i.appendThinking(msg.Thinking)

	lastToolCall, committedSkill := i.commitToolCall(msg)

	suppressResult := isReadToolCall(lastToolCall) && !msg.ToolIsError
	if msg.ToolResult != "" && !committedSkill && !suppressResult {
		i.view.AddMessage(toolResultMessage(lastToolCall, msg.ToolResult, msg.ToolIsError))
	}

	if msg.DelegationInfo != nil {
		i.view.HandleDelegation(msg.DelegationInfo)
		i.notifications.AddDelegationNotification(msg.DelegationInfo)
	}

	if msg.Done && msg.ModelID != "" {
		i.view.SetModelID(msg.ModelID)
	}

	i.view.HandleChunk(msg.Content, msg.Done, errMsg, msg.ToolCallName, msg.ToolStatus)
	// Forward the rich tool-call payload (raw args map + result preview)
	// alongside the legacy name/status fields so the inline ToolCallWidget
	// can render an opencode-style "name: arg" header plus a 1-5 line
	// result snippet without re-parsing the flattened summary string.
	if msg.ToolCallArgs != nil {
		i.view.SetToolCallArgs(msg.ToolCallArgs)
	}
	if msg.ToolResult != "" {
		i.view.SetToolCallResult(msg.ToolResult)
	}

	i.accumulateResponseTokens(msg.Content)
	i.flushThinking(msg.Done)
	i.finaliseStreamIfDone(msg)
	i.syncStatusBar()
}

// commitToolCall advances the active tool-call state machine for the given chunk.
//
// Expected:
//   - msg carries the current streaming chunk with tool-call metadata.
//
// Returns:
//   - lastToolCall: the tool-call name that was just committed, if any.
//   - committedSkill: true when the committed call was a skill_load.
//
// Side effects:
//   - Mutates i.activeToolCall.
//   - May call i.view.FlushPartialResponse and i.view.AddMessage.
func (i *Intent) commitToolCall(msg StreamChunkMsg) (lastToolCall string, committedSkill bool) {
	if msg.ToolCallName != "" {
		i.activeToolCall = msg.ToolCallName
		committedSkill = strings.HasPrefix(msg.ToolCallName, "skill:")
		return "", committedSkill
	}
	if i.activeToolCall == "" {
		return "", false
	}
	lastToolCall = i.activeToolCall
	role := "tool_call"
	content := i.activeToolCall
	if strings.HasPrefix(i.activeToolCall, "skill:") {
		role = "skill_load"
		content = strings.TrimPrefix(i.activeToolCall, "skill:")
		committedSkill = true
	}
	i.view.FlushPartialResponse()
	i.view.AddMessage(chat.Message{Role: role, Content: content})
	i.activeToolCall = ""
	return lastToolCall, committedSkill
}

// accumulateResponseTokens counts tokens in the content fragment and adds them to the
// running response token total, enabling live status bar updates during streaming.
//
// Expected:
//   - content is a partial response fragment from the provider stream.
//
// Side effects:
//   - Increments i.responseTokenCount by the token count of the fragment.
func (i *Intent) accumulateResponseTokens(content string) {
	if content == "" {
		return
	}
	i.responseTokenCount += i.tokenCounter.Count(content)
}

// finaliseStreamIfDone sets the authoritative prompt token count when the stream ends.
//
// Expected:
//   - msg is the current streaming chunk.
//
// Side effects:
//   - Updates i.tokenCount from the engine context result when msg.Done is true.
func (i *Intent) finaliseStreamIfDone(msg StreamChunkMsg) {
	if !msg.Done || i.engine == nil {
		return
	}
	contextResult := i.engine.LastContextResult()
	i.tokenCount = contextResult.TokensUsed
}

// appendThinking accumulates streaming thinking content for later display.
//
// Expected:
//   - thinking is a partial reasoning fragment from the provider stream.
//
// Side effects:
//   - Appends the fragment to the active thinking buffer when present.
func (i *Intent) appendThinking(thinking string) {
	if thinking == "" {
		return
	}
	i.activeThinking += thinking
	// Mirror to the view so the live "💭 thinking..." block at the
	// top of the streaming pane updates as the agent reasons. The
	// final committed Role: "thinking" Message still fires from
	// flushThinking on Done so the reasoning persists in chat
	// history; this just makes the in-flight reasoning visible
	// while it's happening, matching Claude Code's pattern.
	i.view.SetActiveThinking(i.activeThinking)
}

// flushThinking commits accumulated thinking content when streaming ends.
//
// Expected:
//   - done is true when the stream has completed.
//
// Side effects:
//   - Adds a thinking message to the chat view and clears the buffer when done.
func (i *Intent) flushThinking(done bool) {
	if !done || i.activeThinking == "" {
		return
	}
	i.view.AddMessage(chat.Message{Role: "thinking", Content: i.activeThinking})
	i.activeThinking = ""
	// Clear the live thinking block on the view: the reasoning is
	// now committed in chat history, so the in-flight render at the
	// top of the streaming pane should disappear.
	i.view.SetActiveThinking("")
}

// handleStreamChunkMsg processes a StreamChunkMsg and returns the appropriate tea.Cmd.
//
// Expected:
//   - msg contains streaming data, completion status, and optional next command.
//
// Returns:
//   - A tea.Cmd that either batches the next command with spinner, saves the session, or ticks the spinner.
//
// Side effects:
//   - Calls handleStreamChunk and refreshViewport.
//   - Intercepts harness_retry events before standard chunk processing.
func (i *Intent) handleStreamChunkMsg(msg StreamChunkMsg) tea.Cmd {
	// D1 post-cancel suppression: once the user has triggered double-Esc,
	// handleEscapeKey has already set userCancelled, cancelled the stream
	// context, and flipped view.streaming off. Chunks that were already
	// buffered in the provider channel (or that race the cancel across the
	// Update loop) MUST NOT reach handleStreamChunk; otherwise view
	// content keeps accumulating and the user sees the model "still
	// continuing" after pressing Esc twice. Drop every chunk whilst
	// userCancelled is latched, never chain msg.Next (so the reader
	// goroutine stops pulling), and clear the flag only when a terminal
	// Done chunk arrives — at which point the turn is genuinely finished
	// and the next user prompt starts from a clean slate. resetTurnState
	// mirrors the normal Done path so the P7/C2 premature-delegation
	// detector sees a fresh turn on the next user message.
	if i.userCancelled {
		if msg.Done {
			i.userCancelled = false
			i.resetTurnState()
		}
		return nil
	}
	// P7/C2: inspect the chunk for the premature-delegation-misfire
	// signature before handing off to the main dispatch branches. The
	// detection is cheap (string scans + a registry lookup) and must
	// see every chunk so the "first chunk with no preceding text"
	// signal is accurate.
	i.maybeWarnPrematureDelegationMisfire(msg)
	// P12: inspect the chunk for a suggest_delegate tool_result payload
	// and surface a switch-agent notification when the model took the
	// legitimate escape hatch.
	i.maybeNotifySuggestDelegate(msg)
	// Track whether any text content has been emitted this turn so
	// subsequent tool_use chunks are not misattributed to the misfire
	// pattern. A chunk may carry Content, ToolCallName, or both; only
	// genuine text content flips the flag.
	if msg.Content != "" {
		i.turnHasText = true
	}

	switch msg.EventType {
	case "harness_retry":
		return i.handleHarnessRetry(msg)
	case "harness_attempt_start", "harness_complete", "harness_critic_feedback":
		return i.handleHarnessEvent(msg)
	case streaming.EventTypePlanArtifact, streaming.EventTypeReviewVerdict, streaming.EventTypeStatusTransition:
		appendedCmd := i.recordSwarmEvent(msg)
		return batchCmds(i.handleStreamingEvent(msg), appendedCmd)
	}
	appendedCmd := i.recordSwarmEvent(msg)
	// Capture the pre-handler "is a commit pending" signal — once
	// handleStreamChunk runs, i.activeToolCall is cleared by
	// commitToolCall, so the post-handler check would miss the case
	// where a chunk with empty fields triggers a tool_call commit.
	needsRefresh := i.shouldRefreshViewport(msg)
	i.handleStreamChunk(msg)
	if needsRefresh {
		i.refreshViewport()
	}
	if msg.Done {
		i.resetTurnState()
	}
	if !msg.Done && msg.Next != nil {
		return batchCmds(tea.Batch(msg.Next, i.ensureSpinnerActive()), appendedCmd)
	}
	if msg.Done {
		return batchCmds(i.saveSession(), appendedCmd)
	}
	return batchCmds(i.ensureSpinnerActive(), appendedCmd)
}

// maybeWarnPrematureDelegationMisfire adds a notification when the current
// chunk matches the P7/C2 misfire signature. Fires at most once per user
// turn (gated by prematureWarningFired).
//
// Expected:
//   - msg is the chunk being processed by handleStreamChunkMsg.
//
// Side effects:
//   - On detection, adds a warning Notification via the notification
//     manager and flips prematureWarningFired so subsequent chunks in
//     the same turn are silent.
func (i *Intent) maybeWarnPrematureDelegationMisfire(msg StreamChunkMsg) {
	if i.prematureWarningFired {
		return
	}
	warning := detectPrematureDelegationMisfire(
		i.agentID, i.agentRegistry, i.turnUserMessage, msg, i.turnHasText,
	)
	if warning == "" {
		return
	}
	if i.notificationManager == nil {
		return
	}
	i.notificationManager.Add(notification.Notification{
		ID:        "premature-delegation-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Title:     "Delegation mismatch",
		Message:   warning,
		Level:     notification.LevelWarning,
		Duration:  8 * time.Second,
		CreatedAt: time.Now(),
	})
	i.prematureWarningFired = true
}

// resetTurnState clears the per-turn detection fields at the end of a
// streaming turn so the next user prompt starts with a clean slate.
//
// Side effects:
//   - Clears turnUserMessage, turnHasText, and prematureWarningFired.
func (i *Intent) resetTurnState() {
	i.turnUserMessage = ""
	i.turnHasText = false
	i.prematureWarningFired = false
}

// beginTurn initialises all per-turn state for a fresh stream. Called by
// sendMessage before starting a new stream so every turn begins from a
// deterministic clean slate, regardless of how the previous turn ended.
//
// Expected:
//   - userMessage is the text the user just submitted; captured for the
//     P7/C2 premature-delegation-misfire detector.
//
// Side effects:
//   - Sets turnUserMessage to userMessage, clears turnHasText and
//     prematureWarningFired.
//   - Clears userCancelled. This closes the D1 stall-regression gap where
//     a cancelled turn whose Done chunk never reached handleStreamChunkMsg
//     (because cancelActiveStream nilled streamCtx, forcing readNextChunk
//     onto its non-ctx fallback that parks on a silent channel) left the
//     latch set, so every chunk on the NEXT user turn was silently dropped
//     by the post-cancel gate at handleStreamChunkMsg. A fresh user
//     message is an unambiguous turn boundary; the latch has no legitimate
//     role beyond it.
func (i *Intent) beginTurn(userMessage string) {
	i.turnUserMessage = userMessage
	i.turnHasText = false
	i.prematureWarningFired = false
	i.userCancelled = false
	// Capture the wall-clock turn-start so the streaming-time elapsed
	// indicator (rendered at the bottom of the streaming block) and
	// the final-message duration footer both have a reliable origin.
	now := time.Now()
	i.turnStartedAt = now
	i.view.SetTurnStartedAt(now)
}

// maybeNotifySuggestDelegate inspects a chunk for a suggest_delegate
// tool_result payload (Phase 12). When the payload carries the
// "switch_agent" suggestion marker, surface a one-line actionable
// notification prompting the user to switch to a delegating agent so the
// target can be reached. Malformed or unrelated payloads are ignored
// silently — the P7 warning path remains the defence-in-depth signal.
//
// Expected:
//   - msg is the chunk being processed by handleStreamChunkMsg.
//
// Side effects:
//   - On a valid switch_agent payload, adds an info-level Notification
//     via the notification manager.
func (i *Intent) maybeNotifySuggestDelegate(msg StreamChunkMsg) {
	if msg.ToolResult == "" {
		return
	}
	if msg.ToolCallName != "suggest_delegate" {
		return
	}
	if i.notificationManager == nil {
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(msg.ToolResult), &payload); err != nil {
		return
	}
	suggestion, ok := payload["suggestion"].(string)
	if !ok || suggestion != "switch_agent" {
		return
	}

	toAgent, ok := payload["to_agent"].(string)
	if !ok {
		toAgent = ""
	}
	targetAgent, ok := payload["target_agent"].(string)
	if !ok {
		targetAgent = ""
	}
	userPrompt, ok := payload["user_prompt"].(string)
	if !ok {
		userPrompt = ""
	}
	if userPrompt == "" {
		userPrompt = "Switch to " + toAgent + " to delegate to @" + targetAgent + "?"
	}

	i.notificationManager.Add(notification.Notification{
		ID:        "suggest-delegate-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Title:     "Switch agent suggested",
		Message:   userPrompt,
		Level:     notification.LevelInfo,
		Duration:  10 * time.Second,
		CreatedAt: time.Now(),
	})
}

// batchCmds merges a primary command with an optional follow-up command,
// returning the primary alone when the follow-up is nil and a tea.Batch
// otherwise. Extracted so the stream-chunk handler can splice in a
// SwarmEventAppendedMsg dispatch (P3 B7) without reshaping every return
// site into a conditional batch.
//
// Expected:
//   - primary is the main command the handler already returns.
//   - follow is an optional extra command to run alongside primary; may be nil.
//
// Returns:
//   - primary when follow is nil.
//   - tea.Batch(primary, follow) when both are non-nil.
//   - follow when primary is nil and follow is not.
//
// Side effects:
//   - None.
func batchCmds(primary, follow tea.Cmd) tea.Cmd {
	if follow == nil {
		return primary
	}
	if primary == nil {
		return follow
	}
	return tea.Batch(primary, follow)
}

// handleHarnessRetry commits any partial response to history, adds a system
// retry notice, resets streaming state, and continues reading from the same
// stream channel.
//
// Expected:
//   - msg.EventType is "harness_retry".
//   - msg.Content carries the human-readable retry notice.
//   - msg.Next is the continuation command for reading subsequent chunks.
//
// Returns:
//   - A tea.Cmd that batches the next chunk read with a spinner tick.
//
// Side effects:
//   - Commits accumulated partial text as an assistant message.
//   - Adds a system message with the retry notice.
//   - Resets the streaming buffer via StartStreaming.
//   - Refreshes the viewport to reflect new messages.
func (i *Intent) handleHarnessRetry(msg StreamChunkMsg) tea.Cmd {
	i.activeToolCall = ""
	if partial := i.view.Response(); partial != "" {
		i.view.AddMessage(chat.Message{Role: "assistant", Content: partial, AgentColor: i.resolveCurrentAgentColor(), ModelID: i.modelName})
	}
	i.view.AddMessage(chat.Message{Role: "system", Content: msg.Content})
	i.view.StartStreaming()
	i.refreshViewport()
	if msg.Next != nil {
		return tea.Batch(msg.Next, i.ensureSpinnerActive())
	}
	return i.ensureSpinnerActive()
}

// handleHarnessEvent silently consumes harness observability events.
// These events are for internal tracking and do not affect the session.
//
// Expected:
//   - msg is a StreamChunkMsg with a harness event type.
//
// Returns:
//   - The next command from msg.Next if present, or nil.
//
// Side effects:
//   - None.
func (i *Intent) handleHarnessEvent(msg StreamChunkMsg) tea.Cmd {
	if msg.Next != nil {
		return msg.Next
	}
	return nil
}

// handleStreamingEvent processes plan_artifact, review_verdict, and status_transition
// events by adding a notification and a system message to the chat view.
//
// Expected:
//   - msg.EventType is one of "plan_artifact", "review_verdict", or "status_transition".
//   - msg.Content carries the event description.
//
// Returns:
//   - A tea.Cmd that batches the next chunk read with a spinner tick if streaming continues.
//
// Side effects:
//   - Adds a notification to the notification component.
//   - Adds a system message to the chat view.
//   - Refreshes the viewport.
func (i *Intent) handleStreamingEvent(msg StreamChunkMsg) tea.Cmd {
	title, level := streamingEventMeta(msg.EventType)
	i.notificationManager.Add(notification.Notification{
		ID:        msg.EventType + "-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Title:     title,
		Message:   msg.Content,
		Level:     level,
		Duration:  5 * time.Second,
		CreatedAt: time.Now(),
	})
	i.view.AddMessage(chat.Message{Role: "system", Content: title + ": " + msg.Content})
	i.refreshViewport()
	if msg.Next != nil {
		return tea.Batch(msg.Next, i.ensureSpinnerActive())
	}
	return i.ensureSpinnerActive()
}

// recordSwarmEvent projects a StreamChunkMsg onto the swarm activity store
// when the chunk carries delegation, tool-call, plan-artefact, or
// review-verdict metadata. Chunks that do not map onto a SwarmEventType
// are ignored so the activity pane stays focused on high-signal events.
//
// When an event is appended, the returned tea.Cmd posts a
// SwarmEventAppendedMsg carrying the event's ID back into the Bubble Tea
// event loop (P3 B7). This closes the "idle update" gap where a
// background goroutine appending an event would not trigger a re-render
// until the next keystroke.
//
// Expected:
//   - msg is the current streaming chunk delivered to the Bubble Tea loop.
//
// Returns:
//   - A tea.Cmd resolving to SwarmEventAppendedMsg when an event was
//     appended, or nil when the chunk carried no actionable metadata or
//     the store is nil.
//
// Side effects:
//   - Appends a derived SwarmEvent to i.swarmStore under the store mutex.
func (i *Intent) recordSwarmEvent(msg StreamChunkMsg) tea.Cmd {
	if i.swarmStore == nil {
		return nil
	}
	ev, ok := swarmEventFromChunk(msg, i.agentID)
	if !ok {
		return nil
	}
	i.swarmStore.Append(ev)
	appended := SwarmEventAppendedMsg{ID: ev.ID}
	return func() tea.Msg { return appended }
}

// swarmEventFromChunk converts a StreamChunkMsg into a SwarmEvent suitable
// for the activity timeline.
//
// Expected:
//   - msg is the StreamChunkMsg under consideration.
//   - fallbackAgent is the chat intent's agent ID, used when the chunk
//     does not carry its own agent identity (for example plan/review
//     event types).
//
// Returns:
//   - The derived SwarmEvent.
//   - true if the chunk mapped onto a SwarmEventType; false when it
//     carried no actionable activity metadata.
//
// Side effects:
//   - None.
func swarmEventFromChunk(msg StreamChunkMsg, fallbackAgent string) (streaming.SwarmEvent, bool) {
	if msg.DelegationInfo != nil {
		return delegationSwarmEvent(msg, fallbackAgent), true
	}
	if msg.ToolCallName != "" || msg.ToolStatus != "" {
		return toolCallSwarmEvent(msg, fallbackAgent), true
	}
	if isToolResultChunk(msg) {
		return toolResultSwarmEvent(msg, fallbackAgent), true
	}
	switch msg.EventType {
	case streaming.EventTypePlanArtifact:
		return planOrReviewSwarmEvent(streaming.EventPlan, msg, fallbackAgent), true
	case streaming.EventTypeReviewVerdict:
		return planOrReviewSwarmEvent(streaming.EventReview, msg, fallbackAgent), true
	}
	return streaming.SwarmEvent{}, false
}

// delegationSwarmEvent constructs an EventDelegation SwarmEvent from a chunk
// carrying DelegationInfo.
//
// Expected:
//   - msg.DelegationInfo is non-nil.
//   - fallbackAgent is the chat intent's agent ID used when DelegationInfo
//     carries no TargetAgent.
//
// Returns:
//   - A populated SwarmEvent with EventDelegation type and the DelegationInfo
//     ChainID as its ID (generated UUID when ChainID is empty).
//
// Side effects:
//   - None.
func delegationSwarmEvent(msg StreamChunkMsg, fallbackAgent string) streaming.SwarmEvent {
	agentID := msg.DelegationInfo.TargetAgent
	if agentID == "" {
		agentID = fallbackAgent
	}
	return streaming.SwarmEvent{
		ID:            ensureEventID(msg.DelegationInfo.ChainID),
		Type:          streaming.EventDelegation,
		Status:        msg.DelegationInfo.Status,
		Timestamp:     time.Now().UTC(),
		AgentID:       agentID,
		SchemaVersion: streaming.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"source_agent": msg.DelegationInfo.SourceAgent,
			"description":  msg.DelegationInfo.Description,
		},
	}
}

// toolCallSwarmEvent constructs an EventToolCall SwarmEvent from a chunk
// carrying tool-call start/status metadata.
//
// Expected:
//   - msg carries a non-empty ToolCallName or ToolStatus.
//   - fallbackAgent is the chat intent's agent ID (used as AgentID).
//
// Returns:
//   - A SwarmEvent with EventToolCall type. ID is the upstream tool-use ID
//     when the provider surfaces one (msg.ToolCallID), otherwise a generated
//     UUID.
//
// Side effects:
//   - None.
func toolCallSwarmEvent(msg StreamChunkMsg, fallbackAgent string) streaming.SwarmEvent {
	status := msg.ToolStatus
	if status == "" {
		status = "started"
	}
	metadata := map[string]interface{}{
		"tool_name": msg.ToolCallName,
		"is_error":  msg.ToolIsError,
	}
	// Preserve the native provider-scoped id as metadata for the Ctrl+E
	// details modal's audit trail, even when the SwarmEvent.ID itself is
	// the FlowState-internal id (P14b). Empty ToolCallID is omitted so
	// persisted events stay clean.
	if msg.ToolCallID != "" {
		metadata["provider_tool_use_id"] = msg.ToolCallID
	}
	return streaming.SwarmEvent{
		ID:            ensureEventID(preferredCorrelationID(msg)),
		Type:          streaming.EventToolCall,
		Status:        status,
		Timestamp:     time.Now().UTC(),
		AgentID:       fallbackAgent,
		SchemaVersion: streaming.CurrentSchemaVersion,
		Metadata:      metadata,
	}
}

// isToolResultChunk reports whether a chunk carries a tool-result payload.
//
// A chunk is a tool_result when it either carries the explicit "tool_result"
// EventType (emitted by the engine after tool execution) or when the intent
// layer observes a ToolCallID plus ToolResult body without any tool-call
// start/status fields.
//
// Expected:
//   - msg is the chunk under consideration.
//
// Returns:
//   - true when the chunk should map to EventToolResult.
//
// Side effects:
//   - None.
func isToolResultChunk(msg StreamChunkMsg) bool {
	if msg.EventType == "tool_result" {
		return true
	}
	return msg.ToolCallID != "" && msg.ToolResult != ""
}

// toolResultSwarmEvent constructs an EventToolResult SwarmEvent from a chunk
// that carries the output (or error) of a completed tool call.
//
// The ID is the originating tool_call's ID (msg.ToolCallID) so the P3 coalesce
// state machine can pair call and result into a single pane line.
//
// Expected:
//   - msg carries a tool_result payload (see isToolResultChunk).
//   - fallbackAgent is the chat intent's agent ID (used as AgentID).
//
// Returns:
//   - A SwarmEvent with EventToolResult type. Status is "error" when
//     ToolIsError is true, otherwise "completed".
//
// Side effects:
//   - None.
func toolResultSwarmEvent(msg StreamChunkMsg, fallbackAgent string) streaming.SwarmEvent {
	status := "completed"
	if msg.ToolIsError {
		status = "error"
	}
	metadata := map[string]interface{}{
		"content":  msg.ToolResult,
		"is_error": msg.ToolIsError,
	}
	// Preserve the native provider-scoped id alongside the internal one
	// (P14b audit trail). The SwarmEvent.ID itself is the internal id so
	// coalesce pairs across a provider failover.
	if msg.ToolCallID != "" {
		metadata["provider_tool_use_id"] = msg.ToolCallID
	}
	return streaming.SwarmEvent{
		ID:            ensureEventID(preferredCorrelationID(msg)),
		Type:          streaming.EventToolResult,
		Status:        status,
		Timestamp:     time.Now().UTC(),
		AgentID:       fallbackAgent,
		SchemaVersion: streaming.CurrentSchemaVersion,
		Metadata:      metadata,
	}
}

// planOrReviewSwarmEvent constructs a plan or review SwarmEvent. Both share
// the same shape (a completed event carrying the content body) and the only
// difference is the discriminator.
//
// Expected:
//   - evType is either streaming.EventPlan or streaming.EventReview.
//   - msg.Content holds the plan/review body.
//   - fallbackAgent is the chat intent's agent ID (used as AgentID).
//
// Returns:
//   - A SwarmEvent of the supplied type with a generated UUID (since providers
//     do not surface an ID for these event kinds).
//
// Side effects:
//   - None.
func planOrReviewSwarmEvent(evType streaming.SwarmEventType, msg StreamChunkMsg, fallbackAgent string) streaming.SwarmEvent {
	return streaming.SwarmEvent{
		ID:            ensureEventID(""),
		Type:          evType,
		Status:        "completed",
		Timestamp:     time.Now().UTC(),
		AgentID:       fallbackAgent,
		SchemaVersion: streaming.CurrentSchemaVersion,
		Metadata:      map[string]interface{}{"content": msg.Content},
	}
}

// ensureEventID returns id when non-empty, otherwise a freshly generated UUID.
//
// P2 T3 invariant: every SwarmEvent produced by swarmEventFromChunk must have
// a non-empty ID so the P3 coalesce state machine and persistence round-trip
// never key on the empty string. Providers that surface an upstream tool-use
// ID (Anthropic block.ID, OpenAI tool_call.id) are passed through so tool_call
// and tool_result events share the same ID; delegation events reuse the
// DelegationInfo.ChainID; plan and review events get a generated UUID.
//
// Expected:
//   - id may be empty.
//
// Returns:
//   - id when non-empty, otherwise a new UUID v4 string.
//
// Side effects:
//   - None (uuid.NewString allocates a random UUID but has no I/O).
func ensureEventID(id string) string {
	if id != "" {
		return id
	}
	return uuid.NewString()
}

// preferredCorrelationID chooses the identifier to seed a tool-call or
// tool-result SwarmEvent.ID with. It prefers the FlowState-internal id
// stamped by the engine (P14a) so coalesce pairs a tool_call on provider
// A with its tool_result on provider B even when the providers mint
// disjoint native tool-use ids. The native ToolCallID is the fallback —
// used for pre-P14 chunk fixtures in tests and as a defensive path when
// the engine has not yet wired the correlator on a given code path.
//
// An empty return value is tolerated; ensureEventID mints a UUID when
// neither id was surfaced on the chunk (for example a provider that
// omits ids entirely, which P2 T3's invariant already covers).
//
// Expected:
//   - msg is any StreamChunkMsg whose ID fields may be empty.
//
// Returns:
//   - msg.InternalToolCallID when non-empty; otherwise msg.ToolCallID.
//
// Side effects:
//   - None.
func preferredCorrelationID(msg StreamChunkMsg) string {
	if msg.InternalToolCallID != "" {
		return msg.InternalToolCallID
	}
	return msg.ToolCallID
}

// streamingEventMeta returns the display title and notification level for a streaming event type.
//
// Expected:
//   - eventType is one of "plan_artifact", "review_verdict", or "status_transition".
//
// Returns:
//   - A human-readable title and notification Level.
//
// Side effects:
//   - None.
func streamingEventMeta(eventType string) (string, notification.Level) {
	switch eventType {
	case streaming.EventTypePlanArtifact:
		return "Plan Artifact", notification.LevelInfo
	case streaming.EventTypeReviewVerdict:
		return "Review Verdict", notification.LevelWarning
	case streaming.EventTypeStatusTransition:
		return "Status Transition", notification.LevelInfo
	default:
		return "Event", notification.LevelInfo
	}
}

// saveSession builds session metadata from the current engine state and persists
// the session asynchronously via a tea.Cmd.
//
// Returns:
//   - A tea.Cmd that writes the session to disk and returns a SessionSavedMsg.
//
// Side effects:
//   - None until the returned Cmd is executed by the Bubble Tea runtime.
func (i *Intent) saveSession() tea.Cmd {
	if i.sessionStore == nil || i.engine == nil {
		return nil
	}
	store := i.engine.ContextStore()
	if store == nil {
		return nil
	}
	sessionStore := i.sessionStore
	sessionID := i.sessionID
	loadedSkills := i.engine.LoadedSkills()
	skillNames := make([]string, 0, len(loadedSkills))
	for i := range loadedSkills {
		skillNames = append(skillNames, loadedSkills[i].Name)
	}
	meta := contextpkg.SessionMetadata{
		AgentID:      i.agentID,
		SystemPrompt: i.engine.BuildSystemPrompt(),
		LoadedSkills: skillNames,
	}
	// Capture swarm events outside the closure so All() is called on the
	// Bubble Tea goroutine (same as other field reads above). The store's
	// mutex ensures a consistent snapshot even when stream workers append
	// concurrently.
	var swarmEvents []streaming.SwarmEvent
	if i.swarmStore != nil {
		swarmEvents = i.swarmStore.All()
	}
	return func() tea.Msg {
		saveErr := sessionStore.Save(sessionID, store, meta)
		// Persist swarm events alongside the session when the store
		// supports it. Errors are intentionally swallowed: event loss
		// is tolerable, but a failed session save is not.
		if ep, ok := sessionStore.(SwarmEventPersister); ok && len(swarmEvents) > 0 {
			//nolint:errcheck // Event persistence is best-effort; session save must not fail for events.
			ep.SaveEvents(sessionID, swarmEvents)
		}
		return SessionSavedMsg{Err: saveErr}
	}
}

// syncStatusBar updates the StatusBar with the current intent state.
//
// Side effects:
//   - Updates the StatusBar with provider, model, and combined token information.
func (i *Intent) syncStatusBar() {
	i.statusBar.Update(layout.StatusBarMsg{
		Provider:    i.providerName,
		Model:       i.modelName,
		AgentID:     i.agentID,
		TokensUsed:  i.tokenCount + i.responseTokenCount,
		TokenBudget: i.tokenBudget,
	})
}

// refreshViewport rebuilds the message viewport content and conditionally scrolls to the bottom.
// The viewport height is recalculated to account for any active notification overlay.
//
// Side effects:
//   - Adjusts msgViewport.Height for active notifications.
//   - Updates msgViewport content and scrolls to latest message if atBottom is true.
func (i *Intent) refreshViewport() {
	if !i.vpReady || i.msgViewport == nil {
		return
	}
	i.msgViewport.Height = i.computeViewportHeight()
	i.view.SetDimensions(i.width, i.msgViewport.Height)
	content := i.view.RenderContent(i.width)
	i.msgViewport.SetContent(content)
	if i.atBottom {
		i.msgViewport.GotoBottom()
	}
}

// atMentionPattern matches @-mentions for agent names in a user message.
// It captures the name token immediately after "@", allowing letters,
// digits, underscores, and hyphens (the character set used in agent IDs
// and aliases across the manifest corpus). The leading boundary is
// enforced with a non-alphanumeric preceding character OR start-of-line so
// "email@example.com" does not match.
var atMentionPattern = regexp.MustCompile(`(?:^|[^a-zA-Z0-9_-])@([a-zA-Z0-9_][a-zA-Z0-9_-]*)`)

// extractAtMentions returns the set of @-mentioned names found in the
// given message, preserving their original casing. The names are filtered
// downstream via the agent registry's GetByNameOrAlias helper.
//
// Expected:
//   - message is the raw user prompt.
//
// Returns:
//   - A slice of mention tokens without the leading "@"; empty if none.
//
// Side effects:
//   - None.
func extractAtMentions(message string) []string {
	matches := atMentionPattern.FindAllStringSubmatch(message, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) > 1 && m[1] != "" {
			out = append(out, m[1])
		}
	}
	return out
}

// detectPrematureDelegationMisfire returns a user-facing warning message
// when the P7/C2 signature is detected: an agent whose manifest sets
// can_delegate:false emits a bare tool_use as the very first content of
// its reply, yet the user's prompt referenced another agent via an
// @<name> mention that matches a known registry entry.
//
// The detection is intentionally conservative — all three signals must
// align. A return value of "" means the current chunk is not a misfire.
//
// Expected:
//   - agentID is the currently active agent; may be empty (no detection).
//   - reg is the agent registry; when nil, detection is skipped.
//   - userMsg is the current turn's user prompt.
//   - msg is the chunk under inspection.
//   - hasText is true when some text content has already been emitted
//     during this turn (i.e. the chunk is not the first content).
//
// Returns:
//   - A short warning message when the misfire pattern matches.
//   - "" in every other case.
//
// Side effects:
//   - None.
func detectPrematureDelegationMisfire(
	agentID string,
	reg *agent.Registry,
	userMsg string,
	msg StreamChunkMsg,
	hasText bool,
) string {
	// Signal 1: first content of the turn must be a tool_use start.
	if msg.ToolCallName == "" || hasText {
		return ""
	}
	// Signal 2: current agent must be in the registry and unable to delegate.
	if reg == nil || agentID == "" {
		return ""
	}
	manifest, ok := reg.Get(agentID)
	if !ok || manifest == nil || manifest.Delegation.CanDelegate {
		return ""
	}
	// Signal 3: user message must reference a known agent via @-mention.
	mentions := extractAtMentions(userMsg)
	if len(mentions) == 0 {
		return ""
	}
	var target string
	for _, name := range mentions {
		if _, found := reg.GetByNameOrAlias(name); found {
			target = name
			break
		}
	}
	if target == "" {
		return ""
	}
	return "Agent " + agentID + " cannot delegate but your prompt mentioned @" + target +
		" — this tool call may be off-target. Switch to a delegating agent to route the request."
}

// detectAgentFromInput examines the message for planner or executor keywords and returns the matching agent.
//
// Expected:
//   - message is the raw user input string.
//
// Returns:
//   - "planner" if any planner keywords are found (takes priority).
//   - "executor" if any executor keywords are found.
//   - "" if no keywords match.
//
// Side effects:
//   - None.
func detectAgentFromInput(message string) string {
	lower := strings.ToLower(message)

	plannerKeywords := []string{
		"create a plan", "let's plan", "i want to build", "i need to",
		"how do i", "what should", "help me",
		"plan", "design", "architect", "strategy",
	}
	for _, kw := range plannerKeywords {
		if strings.Contains(lower, kw) {
			return "planner"
		}
	}

	executorKeywords := []string{
		"run the plan", "start execution", "begin execution",
		"run it", "do it",
		"execute", "implement",
	}
	for _, kw := range executorKeywords {
		if strings.Contains(lower, kw) {
			return "executor"
		}
	}

	return ""
}

// cancelActiveStream cancels the context of the current streaming producer, if any.
//
// Side effects:
//   - Calls the stored cancel function and clears both streamCancel and
//     streamCtx so a subsequent readNextChunk sees a nil context and
//     falls back to its channel-only receive path.
func (i *Intent) cancelActiveStream() {
	if i.streamCancel != nil {
		i.streamCancel()
		i.streamCancel = nil
	}
	i.streamCtx = nil
}

// cancelActiveTool implements the Ctrl+K tool-cancel key (P17.S2).
//
// The engine does not currently expose a tool-scoped cancel API, so the
// honest behaviour is: if a tool is mid-execution, cancel the entire
// turn via the same path double-Esc takes. When no tool is running the
// key is a no-op — we deliberately do not cancel pure text streams
// because users already have double-Esc for that.
//
// A notification fires on success so users understand Ctrl+K did
// something even though the UI may take a tick to settle back into the
// idle state.
//
// Expected:
//   - Called from handleInputKey on tea.KeyCtrlK.
//
// Returns:
//   - Always nil — cancellation is a side effect; no follow-up Cmd is
//     required because cancelActiveStream wakes the stream consumer and
//     the existing stream-end path handles the rest.
//
// Side effects:
//   - When a tool is active: sets i.userCancelled, cancels the stream
//     context, clears i.view's streaming state, and surfaces a
//     "Tool execution cancelled." info notification.
//   - When no tool is active: does nothing.
func (i *Intent) cancelActiveTool() tea.Cmd {
	if i.activeToolCall == "" {
		return nil
	}
	// Mark this as a user cancel so handleStreamChunk does not surface
	// the inevitable context.Canceled error as a provider failure.
	i.userCancelled = true
	i.cancelActiveStream()
	i.view.SetStreaming(false, "")
	i.activeToolCall = ""
	if i.notificationManager != nil {
		i.notificationManager.Add(notification.Notification{
			ID:        "tool-cancel-" + strconv.FormatInt(time.Now().UnixNano(), 10),
			Title:     "Tool cancelled",
			Message:   "Tool execution cancelled.",
			Level:     notification.LevelInfo,
			Duration:  4 * time.Second,
			CreatedAt: time.Now(),
		})
	}
	i.refreshViewport()
	return nil
}

// doubleEscWindow is the maximum interval between two Esc presses that still
// counts as a double-press for cancelling a streaming response.
const doubleEscWindow = 500 * time.Millisecond

// handleEscapeKey implements the double-Esc interrupt for a streaming response.
//
// Expected:
//   - Called only via handleInputKey, after the session viewer and modal
//     handlers have declined the key.
//
// Returns:
//   - Always nil — cancellation is a side effect and produces no Cmd.
//
// Side effects:
//   - When not streaming: no-op.
//   - First Esc while streaming: records the timestamp (arms the interrupt).
//   - Second Esc within doubleEscWindow: cancels the active stream, clears the
//     partial response via the view, resets lastEscTime, and refreshes the
//     viewport so the user sees the streaming state end.
//   - Esc outside the window: treated as a fresh first press.
func (i *Intent) handleEscapeKey() tea.Cmd {
	if !i.view.IsStreaming() {
		return nil
	}
	now := time.Now()
	if !i.lastEscTime.IsZero() && now.Sub(i.lastEscTime) <= doubleEscWindow {
		// Mark this cancellation as user-initiated BEFORE invoking cancel so
		// handleStreamChunk can distinguish it from an upstream context.Canceled
		// (network timeout, deadline exceeded) and avoid surfacing a spurious
		// error message to the user.
		i.userCancelled = true
		i.cancelActiveStream()
		i.view.SetStreaming(false, "")
		i.lastEscTime = time.Time{}
		i.refreshViewport()
		return nil
	}
	i.lastEscTime = now
	return nil
}

// sendMessage appends the current input to messages and streams a response from the engine.
//
// Returns:
//   - A tea.Cmd that starts the stream and reads the first chunk.
//
// Side effects:
//   - Appends the input to messages as a user message, clears input, sets streaming to true, and resets scroll to bottom.
func (i *Intent) sendMessage() tea.Cmd {
	userMessage := i.input
	i.input = ""
	i.updateViewportForInput()

	if strings.HasPrefix(userMessage, "/") {
		return i.handleSlashCommand(userMessage)
	}

	if detected := detectAgentFromInput(userMessage); detected != "" && detected != i.agentID {
		if i.agentRegistry != nil {
			if manifest, found := i.agentRegistry.Get(detected); found {
				i.engine.SetManifest(*manifest)
				i.agentID = detected
				i.syncStatusBar()
			}
		}
	}

	// P7/C2 + D1: reset all per-turn state via the shared beginTurn helper
	// so the new stream starts with a clean slate — including a cleared
	// userCancelled latch so a prior double-Esc does not trap this turn's
	// chunks at the gate in handleStreamChunkMsg.
	i.beginTurn(userMessage)

	i.view.AddMessage(chat.Message{Role: "user", Content: userMessage})
	i.view.StartStreaming()
	i.atBottom = true
	i.responseTokenCount = 0
	i.refreshViewport()

	// A fresh user message resets the autonomous re-prompt budget so the
	// orchestrator can re-prompt up to maxRePrompts times per user turn.
	if i.completionOrchestrator != nil {
		i.completionOrchestrator.ResetRePromptCount(i.sessionID)
	}

	i.cancelActiveStream()
	ctx, cancel := context.WithCancel(context.Background())
	i.streamCancel = cancel
	i.streamCtx = ctx

	return func() tea.Msg {
		var stream <-chan provider.StreamChunk
		var err error
		if i.sessionManager != nil {
			i.sessionManager.EnsureSession(i.sessionID, i.agentID)
			stream, err = i.sessionManager.SendMessage(ctx, i.sessionID, userMessage)
		} else {
			stream, err = i.streamer.Stream(ctx, i.agentID, userMessage)
		}
		if err != nil {
			return StreamChunkMsg{Content: "", Error: err, Done: true}
		}
		return i.readNextChunkFrom(stream)
	}
}

// readNextChunk reads one chunk from the active stream channel.
//
// Returns:
//   - A StreamChunkMsg with the next chunk's content, error, and done state.
//   - If the channel is closed, returns StreamChunkMsg{Done: true}.
//   - If the stream context is cancelled (P1/D1), returns
//     StreamChunkMsg{Done: true, Error: ctx.Err()} immediately — even when
//     the provider goroutine keeps emitting chunks. formatStreamError
//     already suppresses display of context.Canceled whilst userCancelled
//     is set, so a user-initiated double-Esc cancel does not surface as
//     an error.
//
// Side effects:
//   - Blocks until either a chunk is available on the stream channel or
//     the stream context is cancelled, whichever happens first.
func (i *Intent) readNextChunk() tea.Msg {
	// P1/D1: watch ctx.Done() alongside the channel receive so a
	// double-Esc cancellation actually unblocks the reader goroutine.
	// The previous naked `<-i.streamChan` parked forever even after
	// streamCancel fired, since the provider goroutine kept producing
	// chunks into the buffered channel without honouring ctx at every
	// send site.
	if ctx := i.streamCtx; ctx != nil {
		select {
		case chunk, ok := <-i.streamChan:
			if !ok {
				return StreamChunkMsg{Done: true}
			}
			return buildStreamChunkMsg(i, chunk)
		case <-ctx.Done():
			return StreamChunkMsg{Done: true, Error: ctx.Err()}
		}
	}
	chunk, ok := <-i.streamChan
	if !ok {
		return StreamChunkMsg{Done: true}
	}
	return buildStreamChunkMsg(i, chunk)
}

// buildStreamChunkMsg converts a provider.StreamChunk into a
// StreamChunkMsg, wiring up the Next continuation so the Tea loop
// schedules a follow-up readNextChunk call whenever the chunk is not the
// final one. Extracted from readNextChunk so the ctx-aware select and
// the non-ctx fallback produce identical messages.
//
// Expected:
//   - i is the owning Intent; used only to thread a continuation closure
//     back into i.readNextChunk.
//   - chunk is the raw chunk just received from the stream channel.
//
// Returns:
//   - A fully populated StreamChunkMsg including Next when !chunk.Done.
//
// Side effects:
//   - None. The continuation captured in msg.Next does not run until the
//     Tea loop schedules it.
func buildStreamChunkMsg(i *Intent, chunk provider.StreamChunk) StreamChunkMsg {
	toolCallName, toolStatus := extractToolInfo(chunk.ToolCall)

	msg := StreamChunkMsg{
		Content:            chunk.Content,
		Error:              chunk.Error,
		Done:               chunk.Done,
		EventType:          chunk.EventType,
		ToolCallName:       toolCallName,
		ToolCallArgs:       toolCallArgs(chunk.ToolCall),
		ToolStatus:         toolStatus,
		DelegationInfo:     chunk.DelegationInfo,
		Thinking:           chunk.Thinking,
		ModelID:            chunk.ModelID,
		ToolCallID:         chunk.ToolCallID,
		InternalToolCallID: chunk.InternalToolCallID,
	}

	if chunk.ToolResult != nil {
		msg.ToolResult = chunk.ToolResult.Content
		msg.ToolIsError = chunk.ToolResult.IsError
	}

	if !chunk.Done {
		msg.Next = func() tea.Msg {
			return i.readNextChunk()
		}
	}

	return msg
}

// readNextChunkFrom stores the stream channel and reads the first chunk.
//
// Expected:
//   - stream is a non-nil channel from engine.Stream.
//
// Returns:
//   - A StreamChunkMsg with the first chunk's content, error, and done state.
//
// Side effects:
//   - Stores the stream channel in i.streamChan for subsequent reads.
func (i *Intent) readNextChunkFrom(stream <-chan provider.StreamChunk) tea.Msg {
	i.streamChan = stream
	return i.readNextChunk()
}

// readStreamChunk reads one chunk from the given stream channel and returns a StreamChunkMsg.
//
// Expected:
//   - stream is a non-nil channel from engine.Stream.
//
// Returns:
//   - A StreamChunkMsg with content, error, done state, and a Next closure for the following chunk.
//   - If the channel is closed, returns StreamChunkMsg{Done: true} with nil Next.
//
// Side effects:
//   - Blocks until a chunk is available on the stream channel.
func readStreamChunk(stream <-chan provider.StreamChunk) StreamChunkMsg {
	chunk, ok := <-stream
	if !ok {
		return StreamChunkMsg{Done: true}
	}

	toolCallName, toolStatus := extractToolInfo(chunk.ToolCall)

	msg := StreamChunkMsg{
		Content:            chunk.Content,
		Error:              chunk.Error,
		Done:               chunk.Done,
		EventType:          chunk.EventType,
		ToolCallName:       toolCallName,
		ToolCallArgs:       toolCallArgs(chunk.ToolCall),
		ToolStatus:         toolStatus,
		DelegationInfo:     chunk.DelegationInfo,
		Thinking:           chunk.Thinking,
		ModelID:            chunk.ModelID,
		ToolCallID:         chunk.ToolCallID,
		InternalToolCallID: chunk.InternalToolCallID,
	}

	if chunk.ToolResult != nil {
		msg.ToolResult = chunk.ToolResult.Content
		msg.ToolIsError = chunk.ToolResult.IsError
	}

	if !chunk.Done {
		msg.Next = func() tea.Msg {
			return readStreamChunk(stream)
		}
	}

	return msg
}

// toolCallArgs returns the raw arguments map carried by a provider.ToolCall,
// or nil when the tool call is nil. Centralised so buildStreamChunkMsg and
// readStreamChunk apply the same nil-safety contract when forwarding args
// onto StreamChunkMsg.ToolCallArgs.
//
// Expected:
//   - tc may be nil.
//
// Returns:
//   - tc.Arguments verbatim, or nil when tc is nil.
//
// Side effects:
//   - None.
func toolCallArgs(tc *provider.ToolCall) map[string]any {
	if tc == nil {
		return nil
	}
	return tc.Arguments
}

// View renders the chat interface as a string.
//
// Returns:
//   - A rendered chat view with messages in a persistent viewport and input in the footer.
//
// Side effects:
//   - Syncs streaming state into the StatusBar.
//   - Updates status indicator based on streaming state.
func (i *Intent) View() string {
	if i.sessionViewerActive {
		return i.renderSessionViewerFullScreen()
	}

	i.statusBar.SetStreaming(i.view.IsStreaming(), i.tickFrame)
	i.updateStatusIndicator()

	var content string
	if i.vpReady {
		content = i.msgViewport.View()
	}

	if notifView := i.notifications.View(); notifView != "" {
		content = notifView + "\n" + content
	}

	// Prepend the session trail breadcrumb when the ancestry walk produced
	// a non-empty trail. The trail is rendered at the primary pane width
	// (70% in dual-pane, full in single-pane) and styled faint so it reads
	// as navigation context rather than primary content. It consumes one
	// row from the content area (accounted for in computeViewportHeight).
	if trailLine := i.renderSessionTrailLine(); trailLine != "" {
		content = trailLine + "\n" + content
	}

	var inputLine string
	switch {
	case i.pendingPermission != nil:
		// P17.S3: prompt includes the new 's' key. "[y] approve once",
		// "[n] deny", "[s] approve + remember" matches the handler
		// semantics in handlePermissionKey so the UI stays truthful.
		inputLine = fmt.Sprintf(
			"[PERMISSION] Allow tool %q? [y] approve once  [n] deny  [s] approve + remember",
			i.pendingPermission.ToolName,
		)
	default:
		inputLine = i.renderInputLine()
	}

	status := i.renderStatusString()

	if i.cachedScreenLayout == nil {
		i.cachedScreenLayout = layout.NewScreenLayout(&terminal.Info{Width: i.width, Height: i.height}).
			WithBreadcrumbs(i.breadcrumbPath).
			WithFooterSeparator(true)
	}

	sl := i.cachedScreenLayout
	sl.WithContent(content).
		WithInput(inputLine).
		WithStatusBar(i.statusBar.RenderContent(i.width)).
		WithHelp(status + chatHintSuffix)

	i.applySecondaryPaneContent(sl)

	return i.renderModalOverlay(sl.Render())
}

// applySecondaryPaneContent populates the cached ScreenLayout's secondary
// pane with the swarm activity timeline when the layout has room. Below
// the dual-pane width threshold it explicitly clears secondaryContent so
// the single-pane fallback fires rather than reusing stale content from
// a previous visible render.
//
// Expected:
//   - sl is the non-nil cached ScreenLayout already populated with content,
//     input, status-bar, and help strings.
//
// Side effects:
//   - Calls WithSecondaryContent on sl.
func (i *Intent) applySecondaryPaneContent(sl *layout.ScreenLayout) {
	if i.swarmActivity == nil || i.width < layout.DualPaneMinWidth {
		sl.WithSecondaryContent("")
		return
	}
	contentHeight := sl.GetAvailableContentHeight()
	var swarmEvents []streaming.SwarmEvent
	if i.swarmStore != nil {
		swarmEvents = i.swarmStore.All()
	}
	// P1/A2: render the activity pane at the secondary-pane width
	// (~30% of i.width) rather than the full terminal width. Passing
	// i.width caused long lines to render at terminal width and then be
	// cropped by the composite layout, masking truncation bugs and
	// breaking the pane's own overflow arithmetic.
	_, secondaryWidth := layout.SplitPaneWidths(i.width)
	// P3 A3: the chat intent is the authoritative source of visibility.
	// Reassert the map on every render so a transient mutation on the
	// pane cannot leave non-tool_call types silently hidden across
	// renders. P11 also wires the active filter-profile label through so
	// the pane can surface the current preset to the user.
	sl.WithSecondaryContent(
		i.swarmActivity.
			WithEvents(swarmEvents).
			WithVisibleTypes(i.swarmVisibleTypes).
			WithProfileName(swarmFilterProfileName(i.swarmFilterProfile)).
			Render(secondaryWidth, contentHeight),
	)
}

// renderModalOverlay applies loading or error modal overlays to the base view.
//
// Expected:
//   - baseView is the fully rendered chat view string.
//
// Returns:
//   - The base view with any active modal overlaid, or the base view unchanged.
//
// Side effects:
//   - None.
func (i *Intent) renderModalOverlay(baseView string) string {
	if i.delegationPickerModal != nil {
		modalContent := i.delegationPickerModal.Render(i.width, i.height)
		return feedback.RenderOverlay(baseView, modalContent, i.width, i.height, themes.NewDefaultTheme())
	}
	if i.loadingModal != nil {
		modalContent := i.loadingModal.Render(i.width, i.height)
		return feedback.RenderOverlay(baseView, modalContent, i.width, i.height, themes.NewDefaultTheme())
	}
	if i.errorModal != nil {
		modalContent := i.errorModal.Render(i.width, i.height)
		return feedback.RenderOverlay(baseView, modalContent, i.width, i.height, themes.NewDefaultTheme())
	}
	return baseView
}

// renderSessionViewerFullScreen renders the session viewer as a full-screen view,
// replacing the chat entirely.
//
// Expected:
//   - i.sessionViewerActive is true and i.sessionViewport is non-nil.
//
// Returns:
//   - A full-screen ScreenLayout render with breadcrumb and help bar.
//
// Side effects:
//   - None.
func (i *Intent) renderSessionViewerFullScreen() string {
	var content string
	if i.sessionViewport != nil {
		content = i.sessionViewport.View()
	}
	sl := layout.NewScreenLayout(&terminal.Info{Width: i.width, Height: i.height}).
		WithBreadcrumbs(i.breadcrumbPath).
		WithContent(content).
		WithHelp("↑/↓ k/j PgUp/PgDn: scroll  ·  Home/End  ·  Esc: back to chat  ·  Ctrl+C: quit").
		WithFooterSeparator(true)
	return sl.Render()
}

// renderInputLine renders the current input with a "> " prompt on the first line
// and "  " indent on continuation lines for multiline inputs.
//
// Returns:
//   - The formatted input string with prompts.
//
// Side effects:
//   - None.
func (i *Intent) renderInputLine() string {
	if !strings.Contains(i.input, "\n") {
		return "> " + i.input
	}
	lines := strings.Split(i.input, "\n")
	rendered := make([]string, len(lines))
	for idx, line := range lines {
		if idx == 0 {
			rendered[idx] = "> " + line
		} else {
			rendered[idx] = "  " + line
		}
	}
	return strings.Join(rendered, "\n")
}

// updateStatusIndicator updates the status indicator based on streaming state.
//
// Side effects:
//   - Updates the status indicator active state and advances frame if streaming.
func (i *Intent) updateStatusIndicator() {
	if i.view.IsStreaming() {
		i.statusIndicator.SetActive(true)
		i.statusIndicator.SetFrame(i.tickFrame)
	} else {
		i.statusIndicator.SetActive(false)
	}
}

// renderStatusString returns the current status as a display string.
//
// Returns:
//   - "Thinking..." with spinner when streaming, "Ready" when idle.
//
// Side effects:
//   - None.
func (i *Intent) renderStatusString() string {
	if i.view.IsStreaming() {
		return i.statusIndicator.Render()
	}
	return "Ready"
}

// Result returns the current outcome state of the chat intent.
//
// Returns:
//   - The current IntentResult, or nil if no result has been set.
//
// Side effects:
//   - None.
func (i *Intent) Result() *tuiintents.IntentResult {
	return i.result
}

// handleModelsCommand processes the /models command.
//
// Returns:
//   - A response message string listing available models.
//
// Side effects:
//   - None.
func (i *Intent) handleModelsCommand() string {
	availableModels, err := i.engine.ListAvailableModels()
	if err != nil {
		return "Error listing models: " + err.Error()
	}
	if len(availableModels) == 0 {
		return "No models available"
	}
	var sb strings.Builder
	sb.WriteString("Available models:\n")
	for _, m := range availableModels {
		fmt.Fprintf(&sb, "  • %s (%s, %d tokens)\n", m.ID, m.Provider, m.ContextLength)
	}
	return sb.String()
}

// handleModelCommand processes the /model command.
//
// Expected:
//   - args is in the format "provider/model".
//
// Returns:
//   - A response message string.
//
// Side effects:
//   - Updates providerName and modelName if valid format.
//   - Calls engine.SetModelPreference if valid format.
func (i *Intent) handleModelCommand(args string) string {
	if args == "" {
		return "Usage: /model <provider>/<model-name>\nExample: /model ollama/llama2"
	}
	parts := strings.Split(args, "/")
	if len(parts) != 2 {
		return "Usage: /model <provider>/<model>"
	}
	providerName := strings.TrimSpace(parts[0])
	model := strings.TrimSpace(parts[1])
	i.engine.SetModelPreference(providerName, model)
	i.providerName = providerName
	i.modelName = model
	i.tokenBudget = i.engine.ModelContextLimit()
	i.syncStatusBar()
	i.syncViewAgentMeta()
	return "Switched to model: " + providerName + "/" + model
}

// handleAgentCommand processes the /agent command.
//
// Expected:
//   - args is the agent ID to switch to.
//
// Returns:
//   - A response message string.
//
// Side effects:
//   - Updates agentID and syncs status bar if agent is found.
//   - Calls engine.SetManifest if agent is found.
func (i *Intent) handleAgentCommand(args string) string {
	if args == "" {
		return "Usage: /agent <agent-id>\nExample: /agent planner"
	}
	if i.agentRegistry == nil {
		return "No agent registry available"
	}
	agentID := strings.TrimSpace(args)
	manifest, found := i.agentRegistry.Get(agentID)
	if !found {
		return "Unknown agent: " + agentID
	}
	i.engine.SetManifest(*manifest)
	i.agentID = agentID
	i.tokenBudget = i.engine.ModelContextLimit()
	i.syncStatusBar()
	i.syncViewAgentMeta()
	return "Switched to agent: " + agentID
}

// handleAgentsCommand processes the /agents command.
//
// Returns:
//   - A response message string listing available agents.
//
// Side effects:
//   - None.
func (i *Intent) handleAgentsCommand() string {
	if i.agentRegistry == nil {
		return "No agent registry available"
	}
	agents := i.agentRegistry.List()
	if len(agents) == 0 {
		return "No agents available"
	}
	var sb strings.Builder
	sb.WriteString("Available agents:\n")
	for _, m := range agents {
		fmt.Fprintf(&sb, "  • %s\n", m.ID)
	}
	return sb.String()
}

// handleSlashCommand processes a slash command and returns a Cmd.
//
// Expected:
//   - cmd is a non-empty string starting with "/".
//
// Returns:
//   - A tea.Cmd that appends a system message and refreshes the viewport.
//
// Side effects:
//   - Parses the command and executes its logic.
//   - Appends system messages to the message list.
//   - May update model preference via SetModelPreference.
func (i *Intent) handleSlashCommand(cmd string) tea.Cmd {
	return func() tea.Msg {
		parts := strings.SplitN(strings.TrimPrefix(cmd, "/"), " ", 2)
		command := parts[0]
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}

		var response string
		switch command {
		case "models":
			response = i.handleModelsCommand()

		case "model":
			response = i.handleModelCommand(args)

		case "agent":
			response = i.handleAgentCommand(args)

		case "agents":
			response = i.handleAgentsCommand()

		case "help":
			response = "Available slash commands:\n" +
				"  /models - List all available models\n" +
				"  /model <provider>/<model> - Switch to a model\n" +
				"  /agent <agent-id> - Switch to an agent\n" +
				"  /agents - List all available agents\n" +
				"  /help - Show this help message\n" +
				"\n" +
				"Keybindings:\n" +
				"  Enter        - Send message\n" +
				"  Alt+Enter    - New line\n" +
				"  Tab          - Toggle active agent\n" +
				"  Esc          - Dismiss modal / picker / session viewer\n" +
				"  Ctrl+C       - Cancel stream, save session, and quit\n" +
				"  Ctrl+D       - Open delegation picker\n" +
				"  Ctrl+A       - Open agent picker\n" +
				"  Ctrl+P       - Open model selector\n" +
				"  Ctrl+S       - Open session browser (may freeze on some terminals; try stty -ixon)\n" +
				"  Ctrl+G       - Open session tree\n" +
				"  Ctrl+E       - Open event details (may shadow terminal-muxer/IDE bindings)\n" +
				"  Ctrl+T       - Cycle activity-timeline filter profile (may shadow terminal-muxer/IDE bindings)\n" +
				"  Up/Down      - Scroll viewport line by line\n" +
				"  PgUp/PgDn    - Scroll viewport or event-details modal by page\n" +
				"  Home/End     - Jump to top / bottom of viewport or event-details modal\n" +
				"\n" +
				"See docs/design/keybindings.md for a note on Ctrl+T / Ctrl+E\n" +
				"collisions with tmux, screen, and common IDEs."

		default:
			response = "Unknown command: /" + command
		}

		i.view.AddMessage(chat.Message{Role: "system", Content: response})
		i.refreshViewport()
		return nil
	}
}

// handleToolPermission processes a tool permission request. When the
// tool's name already appears in the session-scoped approval cache
// (P17.S3), the prompt is suppressed and the caller is immediately sent
// an Approved+Remember=true decision on the response channel — the user
// pressed 's' earlier in the session and has authorised future uses of
// this tool. Otherwise the intent enters permission mode and stores the
// request for the handlePermissionKey handler.
//
// Expected:
//   - msg contains tool details and a response channel. The channel
//     must have at least capacity-1 buffering OR a consumer goroutine,
//     otherwise the auto-approve send will block and deadlock the
//     intent — callers own this contract.
//
// Side effects:
//   - Cache hit: sends ToolPermissionDecision{Approved:true,Remember:true}
//     on msg.Response and clears the pending permission (no prompt is
//     shown to the user).
//   - Cache miss: stores msg on i.pendingPermission and enters
//     permission mode so View() renders the prompt.
func (i *Intent) handleToolPermission(msg ToolPermissionMsg) {
	if _, remembered := i.sessionApprovedTools[msg.ToolName]; remembered {
		if msg.Response != nil {
			msg.Response <- ToolPermissionDecision{Approved: true, Remember: true}
		}
		i.pendingPermission = nil
		return
	}
	i.pendingPermission = &msg
}

// openModelSelector creates and shows the model selector as a modal overlay.
//
// Returns:
//   - A tea.Cmd that emits a ShowModalMsg to display the model selector.
//
// Side effects:
//   - None.
func (i *Intent) openModelSelector() tea.Cmd {
	return func() tea.Msg {
		if i.app == nil {
			return nil
		}
		modelIntent := models.NewIntent(models.IntentConfig{
			AppShell:         i.app,
			ProviderRegistry: i.app,
			OnSelect: func(provider, model string) {
				i.engine.SetModelPreference(provider, model)
				i.providerName = provider
				i.modelName = model
				i.tokenBudget = i.engine.ModelContextLimit()
				i.syncStatusBar()
			},
		})
		return tuiintents.ShowModalMsg{Modal: modelIntent}
	}
}

// openAgentPicker creates and shows the agent picker as a modal overlay.
//
// Returns:
//   - A tea.Cmd that emits a ShowModalMsg to display the agent picker.
//
// Side effects:
//   - None.
func (i *Intent) openAgentPicker() tea.Cmd {
	return func() tea.Msg {
		if i.agentRegistry == nil {
			return nil
		}
		agents := i.agentRegistry.List()
		entries := make([]agentpicker.AgentEntry, len(agents))
		for idx := range agents {
			entries[idx] = agentpicker.AgentEntry{
				ID:   agents[idx].ID,
				Name: agents[idx].Name,
			}
		}
		pickerIntent := agentpicker.NewIntent(agentpicker.IntentConfig{
			Agents: entries,
			OnSelect: func(agentID string) {
				manifest, found := i.agentRegistry.Get(agentID)
				if !found {
					return
				}
				i.engine.SetManifest(*manifest)
				i.agentID = agentID
				i.tokenBudget = i.engine.ModelContextLimit()
				i.syncStatusBar()
			},
		})
		return tuiintents.ShowModalMsg{Modal: pickerIntent}
	}
}

// openSessionBrowser creates and shows the session browser as a modal overlay.
//
// Returns:
//   - A tea.Cmd that emits a ShowModalMsg to display the session browser.
//
// Side effects:
//   - None.
func (i *Intent) openSessionBrowser() tea.Cmd {
	return func() tea.Msg {
		if i.sessionStore == nil {
			return nil
		}
		sessions := i.sessionStore.List()
		entries := make([]sessionbrowser.SessionEntry, len(sessions))
		for idx := range sessions {
			s := &sessions[idx]
			title := s.Title
			if title == "" {
				switch {
				case !s.LastActive.IsZero():
					title = "Session — " + s.LastActive.Format("2 Jan 2006 15:04")
				case s.MessageCount > 0:
					title = fmt.Sprintf("Session (%d messages)", s.MessageCount)
				default:
					title = "Session " + s.ID[:8]
				}
			}
			entries[idx] = sessionbrowser.SessionEntry{
				ID:           s.ID,
				Title:        title,
				MessageCount: s.MessageCount,
				LastActive:   s.LastActive,
			}
		}
		browserIntent := sessionbrowser.NewIntent(sessionbrowser.IntentConfig{
			Sessions:        entries,
			Deleter:         i.sessionStore,
			Forker:          i.sessionStore,
			ActiveSessionID: i.sessionID,
		})
		return tuiintents.ShowModalMsg{Modal: browserIntent}
	}
}

// openSessionTree creates and shows the session tree as a modal overlay.
//
// Returns:
//   - A tea.Cmd that emits a ShowModalMsg to display the session tree, or nil
//     if no child session lister is available or listing fails.
//
// Side effects:
//   - None.
func (i *Intent) openSessionTree() tea.Cmd {
	if i.childSessionLister == nil {
		return nil
	}
	sessions, err := i.childSessionLister.AllSessions()
	if err != nil {
		return nil
	}
	nodes := make([]sessiontree.SessionNode, len(sessions))
	for idx, s := range sessions {
		nodes[idx] = sessiontree.SessionNode{
			SessionID: s.ID,
			AgentID:   s.AgentID,
			ParentID:  s.ParentID,
		}
	}
	tree := sessiontree.New(i.sessionID, nodes)
	return func() tea.Msg {
		return tuiintents.ShowModalMsg{Modal: tree}
	}
}

// openEventDetails creates and shows the event details modal for the most
// recent SwarmEvent in the swarm store.
//
// Returns:
//   - A tea.Cmd that emits a ShowModalMsg to display the event details, or
//     nil if the swarm store is empty. In the empty case a short-lived
//     informational notification is added so the user sees feedback rather
//     than the key being silently dropped (P1/B11).
//
// Side effects:
//   - When the store is empty, appends a notification via the notification
//     manager and refreshes the viewport so it renders immediately.
func (i *Intent) openEventDetails() tea.Cmd {
	allEvents := i.swarmStore.All()
	if len(allEvents) == 0 {
		// P1/B11: surface user feedback on an empty timeline. Previously
		// this path returned nil, so Ctrl+E appeared to do nothing.
		if i.notificationManager != nil {
			i.notificationManager.Add(notification.Notification{
				ID:        "event-details-empty-" + strconv.FormatInt(time.Now().UnixNano(), 10),
				Title:     "Activity Timeline",
				Message:   "No activity events to inspect yet",
				Level:     notification.LevelInfo,
				Duration:  3 * time.Second,
				CreatedAt: time.Now(),
			})
			i.refreshViewport()
		}
		return nil
	}
	latest := allEvents[len(allEvents)-1]
	detail := eventdetails.New(latest)
	return func() tea.Msg {
		return tuiintents.ShowModalMsg{Modal: detail}
	}
}

// handleSessionTreeSelection processes a session tree selection by switching
// to the chosen session and refreshing the ancestry trail.
//
// Expected:
//   - msg.SessionID is a non-empty string matching an existing session.
//
// Returns:
//   - A tea.Cmd from switchToSession that loads the session asynchronously.
//
// Side effects:
//   - Updates sessionID and triggers async session load.
//   - Refreshes the session trail via the loaded-session handler chain.
func (i *Intent) handleSessionTreeSelection(msg sessiontree.SelectedMsg) tea.Cmd {
	i.sessionID = msg.SessionID
	i.refreshSessionTrail()
	return i.switchToSession(msg.SessionID)
}

// openDelegationPicker opens the delegation list modal.
//
// Expected:
//   - None.
//
// Returns:
//   - A Cmd that opens the modal, or nil if no session manager available.
//
// Side effects:
//   - Sets delegationPickerModal on the intent.
func (i *Intent) openDelegationPicker() tea.Cmd {
	var sessions []*session.Session
	if i.childSessionLister != nil {
		var err error
		sessions, err = i.childSessionLister.AllSessions()
		if err != nil {
			sessions = nil
		}
	}
	modal := chat.NewDelegationPickerModal(sessions, i.width, i.height)
	i.delegationPickerModal = modal
	return nil
}

// handleSessionResult dispatches the session browser outcome to the
// appropriate handler based on whether a new or existing session was chosen.
//
// Expected:
//   - msg is a SessionSelectedMsg from the session browser intent.
//
// Returns:
//   - A tea.Cmd, or nil when no further action is needed.
//
// Side effects:
//   - Delegates to createNewSession or switchToSession.
func (i *Intent) handleSessionResult(msg sessionbrowser.SessionSelectedMsg) tea.Cmd {
	if msg.IsNew {
		return i.createNewSession()
	}
	return i.switchToSession(msg.SessionID)
}

// createNewSession resets the chat to a fresh session with a new ID.
//
// Returns:
//   - nil (no async command needed).
//
// Side effects:
//   - Generates a new session ID, resets the chat view, and syncs the status bar.
func (i *Intent) createNewSession() tea.Cmd {
	i.sessionID = uuid.New().String()
	i.engine.SetContextStore(recall.NewEmptyContextStore(""), i.sessionID)
	i.view = chat.NewView()
	i.syncViewAgentMeta()
	i.refreshViewport()
	i.syncStatusBar()
	return nil
}

// switchToSession shows a loading modal and kicks off an async session load.
//
// Expected:
//   - sessionID is a non-empty string matching an existing session file.
//
// Returns:
//   - A tea.Cmd that loads the session from disk asynchronously.
//
// Side effects:
//   - Sets the loading modal and syncs the status bar.
func (i *Intent) switchToSession(sessionID string) tea.Cmd {
	i.sessionID = sessionID
	i.loadingModal = feedback.NewLoadingModal("Loading session\u2026", false)
	i.syncStatusBar()
	return tea.Batch(i.ensureSpinnerActive(), i.loadSessionAsync(sessionID))
}

// loadSessionAsync returns a command that loads a session from disk.
//
// Expected:
//   - sessionID is a non-empty string matching an existing session file.
//
// Returns:
//   - A tea.Cmd that sends a SessionLoadedMsg when the load completes.
//
// Side effects:
//   - None (I/O happens inside the returned command).
func (i *Intent) loadSessionAsync(sessionID string) tea.Cmd {
	store := i.sessionStore
	return func() tea.Msg {
		loadedStore, err := store.Load(sessionID)
		return sessionbrowser.SessionLoadedMsg{
			SessionID: sessionID,
			Store:     loadedStore,
			Err:       err,
		}
	}
}

// handleSessionDeleted surfaces the outcome of a session delete as a toast
// notification. Success emits a short info-level message; failure emits an
// error-level message carrying the underlying error text. The in-memory
// session list inside the browser has already been updated at this point —
// this handler's only job is user feedback.
//
// Expected:
//   - msg is a sessionbrowser.SessionDeletedMsg from the browser intent.
//
// Returns:
//   - nil (no follow-up command).
//
// Side effects:
//   - Adds a Notification to notificationManager when one is configured.
func (i *Intent) handleSessionDeleted(msg sessionbrowser.SessionDeletedMsg) tea.Cmd {
	if i.notificationManager == nil {
		return nil
	}
	if msg.Err != nil {
		i.notificationManager.Add(notification.Notification{
			ID:        "session-delete-error-" + strconv.FormatInt(time.Now().UnixNano(), 10),
			Title:     "Delete failed",
			Message:   msg.Err.Error(),
			Level:     notification.LevelError,
			Duration:  8 * time.Second,
			CreatedAt: time.Now(),
		})
		return nil
	}
	i.notificationManager.Add(notification.Notification{
		ID:        "session-delete-ok-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Title:     "Session deleted",
		Message:   msg.SessionID,
		Level:     notification.LevelSuccess,
		Duration:  4 * time.Second,
		CreatedAt: time.Now(),
	})
	return nil
}

// handleSessionForked reacts to the browser's fork outcome (P18b).
//
// Success path: switch the chat to the newly-forked session by loading
// its persisted store and surfacing a toast so the user understands the
// shift. Failure path: surface an error toast and leave the chat on the
// current session — the browser has already dismissed itself before we
// reach this handler, so the user remains in the chat view.
//
// Expected:
//   - msg is a sessionbrowser.SessionForkedMsg produced by the browser
//     after the user pressed 'f'. On success NewSessionID is non-empty.
//
// Returns:
//   - A tea.Cmd that loads the forked session asynchronously on success.
//   - nil on failure (the toast is pushed synchronously).
//
// Side effects:
//   - Adds a Notification via notificationManager when configured.
//   - On success: updates i.sessionID, resets the view, and kicks off a
//     tea.Batch(tickSpinner, loadSessionAsync) so the loading modal is
//     animated while the fork hydrates.
func (i *Intent) handleSessionForked(msg sessionbrowser.SessionForkedMsg) tea.Cmd {
	if msg.Err != nil {
		if i.notificationManager != nil {
			i.notificationManager.Add(notification.Notification{
				ID:        "session-fork-error-" + strconv.FormatInt(time.Now().UnixNano(), 10),
				Title:     "Fork failed",
				Message:   msg.Err.Error(),
				Level:     notification.LevelError,
				Duration:  8 * time.Second,
				CreatedAt: time.Now(),
			})
		}
		return nil
	}

	if i.notificationManager != nil {
		i.notificationManager.Add(notification.Notification{
			ID:        "session-fork-ok-" + strconv.FormatInt(time.Now().UnixNano(), 10),
			Title:     "Forked session",
			Message:   msg.NewSessionID,
			Level:     notification.LevelSuccess,
			Duration:  4 * time.Second,
			CreatedAt: time.Now(),
		})
	}

	return i.switchToSession(msg.NewSessionID)
}

// toolCallSummary extracts the primary argument from a tool call and formats it as "toolName: arg".
//
// Expected:
//   - name is a valid tool name.
//   - args contains the tool call arguments.
//
// Returns:
//   - A formatted string "toolName: arg" if a primary argument is found.
//   - Just the tool name if no primary argument is found.
//   - For bash commands longer than 80 characters, truncates and appends "...".
//
// Side effects:
//   - None.
func toolCallSummary(name string, args map[string]interface{}) string {
	return tooldisplay.Summary(name, args)
}

// extractToolInfo extracts the tool call name and status from a provider.ToolCall.
//
// Expected:
//   - toolCall may be nil.
//
// Returns:
//   - toolCallName: "skill:name" for skill_load, "tool: args" for other tools, or "" if toolCall is nil.
//   - toolStatus: "running" if toolCall is not nil, or "" otherwise.
//
// Side effects:
//   - None.
func extractToolInfo(toolCall *provider.ToolCall) (string, string) {
	if toolCall == nil {
		return "", ""
	}

	var toolCallName string
	if toolCall.Name == "skill_load" {
		toolCallName = "skill_load"
		if name, ok := toolCall.Arguments["name"].(string); ok && name != "" {
			toolCallName = "skill:" + name
		}
	} else {
		toolCallName = toolCallSummary(toolCall.Name, toolCall.Arguments)
	}

	return toolCallName, "running"
}

// isReadToolCall reports whether the given tool call name refers to the read tool.
//
// Expected:
//   - name is a raw tool name (e.g. "read") or a formatted summary (e.g. "read: /path").
//
// Returns:
//   - true when name is "read" or begins with "read: ".
//   - false for all other names, including empty strings.
//
// Side effects:
//   - None.
func isReadToolCall(name string) bool {
	return name == "read" || strings.HasPrefix(name, "read: ")
}

// toolResultMessage builds a chat.Message for a tool result, selecting the
// appropriate role and formatting the content based on the tool name.
//
// Expected:
//   - toolName is the name of the tool that produced the result, optionally in
//     "name: primary-input" format (e.g. "bash: ls -la").
//   - result is the raw string output from the tool.
//   - isError indicates whether the tool execution produced an error.
//
// Returns:
//   - A chat.Message with role "tool_error" for errors, "todo_update" for todowrite results,
//     or "tool_result" for all other tools. ToolName holds the raw name and ToolInput holds
//     the primary argument when present.
//
// Side effects:
//   - None.
func toolResultMessage(toolName, result string, isError bool) chat.Message {
	if isError {
		return chat.Message{Role: "tool_error", Content: result}
	}
	rawName, toolInput := splitToolSummary(toolName)
	if rawName == "todowrite" {
		return chat.Message{Role: "todo_update", Content: widgets.FormatTodoList(result)}
	}
	return chat.Message{Role: "tool_result", Content: result, ToolName: rawName, ToolInput: toolInput}
}

// splitToolSummary parses a formatted tool summary into a raw tool name and primary input.
//
// Expected:
//   - summary is a string in the format "toolname: argument" or just "toolname".
//
// Returns:
//   - name is the tool name (the part before ": ", or the whole string if no separator).
//   - input is the primary argument (the part after ": ", or empty if no separator).
//
// Side effects:
//   - None.
func splitToolSummary(summary string) (name, input string) {
	parts := strings.SplitN(summary, ": ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return summary, ""
}

// renderSessionContent renders the messages of a child session into a displayable string.
//
// Expected:
//   - sess is a non-nil session with populated Messages.
//
// Returns:
//   - A rendered string of all session messages using the chat view pipeline.
//
// Side effects:
//   - None.
func (i *Intent) renderSessionContent(sess *session.Session) string {
	v := chat.NewView()
	v.SetDimensions(i.width, i.height)
	for _, msg := range sess.Messages {
		switch msg.Role {
		case "tool_result":
			v.AddMessage(chat.Message{
				Role:      "tool_result",
				Content:   msg.Content,
				ToolName:  msg.ToolName,
				ToolInput: msg.ToolInput,
			})
		case "tool_call":
			summary := msg.ToolName
			if msg.ToolInput != "" {
				summary = msg.ToolName + ": " + msg.ToolInput
			}
			v.AddMessage(chat.Message{Role: "tool_call", Content: summary})
		case "tool_error":
			v.AddMessage(chat.Message{Role: "tool_error", Content: msg.Content})
		case "thinking":
			v.AddMessage(chat.Message{Role: "thinking", Content: msg.Content})
		case "delegation":
			v.AddMessage(chat.Message{Role: "system", Content: msg.Content})
		case "skill_load":
			v.AddMessage(chat.Message{Role: "skill_load", Content: msg.Content})
		case "todo_update":
			v.AddMessage(chat.Message{Role: "todo_update", Content: msg.Content})
		default:
			v.AddMessage(chat.Message{Role: msg.Role, Content: msg.Content})
		}
	}
	return v.RenderContent(i.width)
}

// resetAndRestoreSwarmEvents clears the in-memory swarm event store and
// re-populates it from the session's persisted WAL on session switch.
//
// P5/B2 contract:
//  1. Clear() runs unconditionally — this severs any events that belonged to
//     the previously active session so they cannot leak into the new session's
//     timeline or, under the P4 append-on-write WAL, into the new session's
//     on-disk file.
//  2. Restore routes through streaming.EventRestorer when the store supports
//     it, bypassing the WAL AppendFunc. The events were just read from disk;
//     re-appending via the WAL path would double the on-disk file on every
//     session switch and quickly corrupt long-lived sessions.
//  3. Stores that do not satisfy EventRestorer fall back to Append — best-
//     effort for legacy wrappers; the default stores all support restore.
//
// Expected:
//   - sessionID identifies the session being loaded.
//
// Side effects:
//   - Mutates i.swarmStore via Clear + restore.
//   - Reads the persisted WAL via i.sessionStore.LoadEvents when available.
func (i *Intent) resetAndRestoreSwarmEvents(sessionID string) {
	if i.swarmStore == nil {
		return
	}
	i.swarmStore.Clear()
	ep, ok := i.sessionStore.(SwarmEventPersister)
	if !ok {
		return
	}
	restored, err := ep.LoadEvents(sessionID)
	if err != nil || len(restored) == 0 {
		return
	}
	if restorer, ok := i.swarmStore.(streaming.EventRestorer); ok {
		restorer.RestoreEvents(restored)
		return
	}
	// Fallback for stores that do not implement the restore contract. The
	// default stores (MemorySwarmStore, persistedSwarmStore) both satisfy
	// EventRestorer, so this branch is reserved for future wrappers.
	for idx := range restored {
		i.swarmStore.Append(restored[idx])
	}
}

// handleSessionLoaded processes the result of an async session load.
//
// Expected:
//   - msg contains the loaded FileContextStore, or an error.
//
// Returns:
//   - nil (no further command needed).
//
// Side effects:
//   - Clears the loading modal.
//   - On error, shows an error modal.
//   - On success, replaces the engine's context store with the loaded store,
//     populates the chat view, and refreshes the viewport.
func (i *Intent) handleSessionLoaded(msg sessionbrowser.SessionLoadedMsg) tea.Cmd {
	i.loadingModal = nil
	if msg.Err != nil {
		i.errorModal = feedback.NewErrorModal("Session Error", "Failed to load session: "+msg.Err.Error())
		return nil
	}
	if i.engine != nil {
		// The engine is optional in test-minimal constructions (BDD
		// fixtures that drive the intent without a full engine wired
		// in). Production callers always supply one.
		i.engine.SetContextStore(msg.Store, msg.SessionID)
	}
	i.view = chat.NewView()
	// P17.S3: session switch wipes the approval cache so 's'-granted
	// approvals cannot leak across sessions. Re-initialised as an
	// empty map to keep nil-vs-empty distinctions predictable in the
	// rest of the code (handleToolPermission iterates over it).
	i.sessionApprovedTools = map[string]struct{}{}
	i.syncViewAgentMeta()
	var lastToolCallName string
	for _, sm := range msg.Store.GetStoredMessages() {
		lastToolCallName = i.replayStoredMessage(sm, lastToolCallName)
	}
	i.resetAndRestoreSwarmEvents(msg.SessionID)
	i.atBottom = true
	i.refreshViewport()
	i.syncStatusBar()
	i.refreshSessionTrail()
	return nil
}

// refreshSessionTrail walks the ParentID chain via the session manager to
// rebuild the session-ancestry trail. A visited-set guards against circular
// parent references.
//
// Expected:
//   - sessionManager may be nil; the method returns silently in that case.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Replaces i.sessionTrail with a freshly computed trail in root-first order.
func (i *Intent) refreshSessionTrail() {
	if i.sessionManager == nil {
		return
	}
	var items []navigation.SessionTrailItem
	visited := map[string]bool{}
	id := i.sessionID
	for id != "" && !visited[id] {
		visited[id] = true
		sess, err := i.sessionManager.GetSession(id)
		if err != nil {
			break
		}
		items = append(items, navigation.SessionTrailItem{
			SessionID: sess.ID,
			AgentID:   sess.AgentID,
			Label:     sess.AgentID,
		})
		id = sess.ParentID
	}
	// Reverse to root-first order.
	for l, r := 0, len(items)-1; l < r; l, r = l+1, r-1 {
		items[l], items[r] = items[r], items[l]
	}
	i.sessionTrail = i.sessionTrail.WithItems(items)
}

// replayStoredMessage adds a single stored message to the chat view during session restore.
//
// Expected:
//   - sm is a stored message from the context store.
//   - lastToolCallName is the name of the most recently replayed tool call, if any.
//
// Returns:
//   - The updated lastToolCallName after processing sm.
//
// Side effects:
//   - May call i.view.AddMessage.
func (i *Intent) replayStoredMessage(sm recall.StoredMessage, lastToolCallName string) string {
	switch {
	case sm.Message.Role == "assistant" && len(sm.Message.ToolCalls) > 0:
		// Assistant messages can carry BOTH free-form content AND
		// tool_calls in the same turn (e.g. "I'll read that file
		// for you." followed by a read tool_call). The previous
		// branch only fired when Content was empty and dropped the
		// tool_calls when both were present, which made replay lose
		// every tool block whose assistant turn had any preamble
		// text — and broke lastToolCallName tracking so the
		// subsequent tool_result was misrouted too. Handle both:
		// commit the content as a mid-turn assistant partial
		// (without ModelID — mirrors FlushPartialResponse, so the
		// model + duration footer renders only on the FINAL
		// assistant message of the turn), then commit each tool_call.
		if sm.Message.Content != "" {
			i.view.AddMessage(chat.Message{
				Role:       "assistant",
				Content:    sm.Message.Content,
				AgentColor: i.resolveCurrentAgentColor(),
			})
		}
		for _, tc := range sm.Message.ToolCalls {
			lastToolCallName = tc.Name
			role := "tool_call"
			content := toolCallSummary(tc.Name, tc.Arguments)
			if tc.Name == "skill_load" {
				role = "skill_load"
			}
			i.view.AddMessage(chat.Message{Role: role, Content: content})
		}
	case sm.Message.Role == "tool":
		isError := strings.HasPrefix(strings.ToLower(sm.Message.Content), "error:")
		if !isReadToolCall(lastToolCallName) || isError {
			i.view.AddMessage(toolResultMessage(lastToolCallName, sm.Message.Content, isError))
		}
	default:
		i.view.AddMessage(i.buildAssistantViewMessage(sm))
	}
	return lastToolCallName
}

// buildAssistantViewMessage constructs a chat.Message for a stored assistant or user message.
//
// Expected:
//   - sm.Message.Role is "assistant" or another non-tool role.
//
// Returns:
//   - A chat.Message populated with role, content, and assistant-specific metadata.
//
// Side effects:
//   - None.
func (i *Intent) buildAssistantViewMessage(sm recall.StoredMessage) chat.Message {
	msg := chat.Message{
		Role:    sm.Message.Role,
		Content: sm.Message.Content,
	}
	if sm.Message.Role != "assistant" {
		return msg
	}
	msg.AgentColor = i.resolveCurrentAgentColor()
	if sm.Message.ModelID == "" {
		msg.ModelID = i.modelName
	} else {
		msg.ModelID = sm.Message.ModelID
	}
	return msg
}

// resolveCurrentAgentColor returns the colour for the current agent manifest.
//
// Expected:
//   - i.agentRegistry may be nil.
//
// Returns:
//   - A lipgloss.Color from the manifest or zero value if unavailable.
//
// Side effects:
//   - None.
func (i *Intent) resolveCurrentAgentColor() lipgloss.Color {
	if i.agentRegistry == nil {
		return lipgloss.Color("")
	}
	manifest, ok := i.agentRegistry.Get(i.agentID)
	if !ok {
		return lipgloss.Color("")
	}
	return theme.ResolveAgentColor(*manifest, 0, theme.Default())
}

// syncViewAgentMeta updates the view with current agent colour and model ID.
//
// Side effects:
//   - Sets agentColor and modelID on the chat view.
func (i *Intent) syncViewAgentMeta() {
	i.view.SetAgentColor(i.resolveCurrentAgentColor())
	i.view.SetModelID(i.modelName)
}

// toggleAgent alternates the active agent between "planner" and "executor".
//
// Expected:
//   - i.agentRegistry is non-nil.
//   - Both "planner" and "executor" manifests exist in the registry.
//
// Returns:
//   - nil (no async command needed — switch is synchronous).
//
// Side effects:
//   - Updates i.agentID, i.engine manifest, and status bar.
func (i *Intent) toggleAgent() tea.Cmd {
	if i.agentRegistry == nil {
		return nil
	}
	next := "planner"
	if i.agentID == "planner" {
		next = "executor"
	}
	manifest, found := i.agentRegistry.Get(next)
	if !found {
		return nil
	}
	i.engine.SetManifest(*manifest)
	i.agentID = next
	i.tokenBudget = i.engine.ModelContextLimit()
	i.syncStatusBar()
	i.syncViewAgentMeta()
	return nil
}

// handlePermissionKey processes key input during permission mode. It
// accepts three runes (case-insensitive):
//
//   - 'y': approve this one invocation only.
//   - 'n': deny.
//   - 's': approve AND remember for the rest of the session — the
//     tool's name is inserted into sessionApprovedTools so
//     handleToolPermission can auto-approve future identical requests
//     without prompting.
//
// Any other rune is ignored (permission mode stays active).
//
// Expected:
//   - msg is a tea.KeyMsg while in permission mode.
//
// Returns:
//   - Always nil; cancellation is a side effect.
//
// Side effects:
//   - Calls resolvePermission with the approval and remember flags
//     corresponding to the key.
//   - On 's', inserts the pending tool name into
//     sessionApprovedTools before resolvePermission clears the pending
//     request.
func (i *Intent) handlePermissionKey(msg tea.KeyMsg) tea.Cmd {
	if msg.Type != tea.KeyRunes || len(msg.Runes) == 0 {
		return nil
	}

	// Normalise to lowercase so 'Y'/'N'/'S' work the same as y/n/s.
	// Shift-capitalised presses are a common user mistake and there's
	// no conflicting binding in permission mode.
	r := msg.Runes[0]
	switch r {
	case 'Y':
		r = 'y'
	case 'N':
		r = 'n'
	case 'S':
		r = 's'
	}

	switch r {
	case 'y':
		i.resolvePermission(true, false)
	case 'n':
		i.resolvePermission(false, false)
	case 's':
		if i.pendingPermission != nil {
			i.sessionApprovedTools[i.pendingPermission.ToolName] = struct{}{}
		}
		i.resolvePermission(true, true)
	}
	return nil
}

// resolvePermission sends the user's decision and exits permission mode.
//
// Expected:
//   - approved indicates whether the user accepted the tool call.
//   - remember indicates whether the approval should be cached for the
//     rest of the session (only meaningful when approved is true; 's'
//     is the only path that sets it to true).
//
// Side effects:
//   - Sends a ToolPermissionDecision on the pending permission's
//     response channel when one is attached.
//   - Clears the pending permission and returns the intent to normal
//     input mode.
func (i *Intent) resolvePermission(approved, remember bool) {
	if i.pendingPermission != nil && i.pendingPermission.Response != nil {
		i.pendingPermission.Response <- ToolPermissionDecision{
			Approved: approved,
			Remember: remember,
		}
	}
	i.pendingPermission = nil
}

// Input returns the current input text.
//
// Returns:
//   - The current input text.
//
// Side effects:
//   - None.
func (i *Intent) Input() string {
	return i.input
}

// Messages returns all messages in the chat history.
//
// Returns:
//   - A slice of all messages in the chat.
//
// Side effects:
//   - None.
func (i *Intent) Messages() []chat.Message {
	var result []chat.Message
	for _, msg := range i.view.Messages() {
		if msg.Role == "assistant" {
			result = append(result, msg)
		}
	}
	return result
}

// Response returns the current streaming response content.
//
// Returns:
//   - The partial response string accumulated during streaming.
//
// Side effects:
//   - None.
func (i *Intent) Response() string {
	return i.view.Response()
}

// SpinnerFrame returns the current spinner animation frame as a string.
//
// Returns:
//   - The braille spinner character for the current tick frame.
//
// Side effects:
//   - None.
func (i *Intent) SpinnerFrame() string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[i.tickFrame%len(frames)]
}

// TickFrame returns the current tick frame counter for testing.
//
// Returns:
//   - The current integer tick frame index.
//
// Side effects:
//   - None.
func (i *Intent) TickFrame() int {
	return i.tickFrame
}

// IsStreaming returns whether the intent is currently streaming a response.
//
// Returns:
//   - true if streaming, false otherwise.
//
// Side effects:
//   - None.
func (i *Intent) IsStreaming() bool {
	return i.view.IsStreaming()
}

// Width returns the current terminal width.
//
// Returns:
//   - The current terminal width in columns.
//
// Side effects:
//   - None.
func (i *Intent) Width() int {
	return i.width
}

// Height returns the current terminal height.
//
// Returns:
//   - The current terminal height in rows.
//
// Side effects:
//   - None.
func (i *Intent) Height() int {
	return i.height
}

// TokenCount returns the combined prompt and response token count.
//
// Returns:
//   - The sum of prompt tokens (set from engine at stream completion) and response
//     tokens accumulated during streaming.
//
// Side effects:
//   - None.
func (i *Intent) TokenCount() int {
	return i.tokenCount + i.responseTokenCount
}

// SetApp sets the TUI app shell reference for navigation.
//
// Expected:
//   - appShell is a non-nil reference to the TUI app shell.
//
// Side effects:
//   - Sets the internal app reference used for intent switching.
func (i *Intent) SetApp(appShell AppShell) {
	i.app = appShell
}

// AgentIDForTest returns the current agent ID for testing purposes.
//
// Returns:
//   - The current agent ID.
//
// Side effects:
//   - None.
func (i *Intent) AgentIDForTest() string {
	return i.agentID
}

// SetAgentIDForTest sets the agent ID for testing purposes.
//
// Expected:
//   - id is a non-empty string matching a known agent ID.
//
// Side effects:
//   - Sets the internal agentID field.
func (i *Intent) SetAgentIDForTest(id string) {
	i.agentID = id
}

// MessagesForTest returns all messages including system and user roles.
//
// Returns:
//   - A slice of all messages in the chat, unfiltered by role.
//
// Side effects:
//   - None.
func (i *Intent) MessagesForTest() []chat.Message {
	return i.view.Messages()
}

// SetBreadcrumbPath sets the breadcrumb navigation path shown in the chat header.
//
// Expected:
//   - path is a non-empty string like "Chat" or "Chat > qa-agent".
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Updates the breadcrumb display on next View() call.
func (i *Intent) SetBreadcrumbPath(path string) {
	i.breadcrumbPath = path
	i.cachedScreenLayout = nil
}
