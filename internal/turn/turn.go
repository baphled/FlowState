// Package turn is the canonical Turn resource for FlowState's
// post-then-poll wire shape ("Turn-Based Post-Then-Poll Architecture
// (May 2026)"). Each user-message-driven streaming pass produces ONE
// Turn — a first-class entity with a stable UUID id, a transient
// status (`running` → `completed` | `failed`), a snapshot of the
// model/provider pair the turn ran under, and the list of
// engine-emitted messages persisted during the turn (assistant,
// thinking, tool_call, tool_result, delegation rows).
//
// Phase 1 (this commit) ships the in-memory `Registry` plus context-
// propagation primitives (`TurnIDFromContext` / `WithTurnID`) so the
// dispatcher can mint a turn_id at POST-handler entry and the
// accumulator can append engine-emitted messages onto the
// `Turn.MessagesAdded` slice as they persist. Phase 2 adds the HTTP
// surface (`POST /messages` returns `{turn_id, snapshot}`,
// `GET /turns/{turn_id}` reads turn state); Phase 3 migrates the
// frontend off the SSE active-send path onto polling. Phase 4 decides
// SSE-keep-vs-remove.
//
// Live-package boundary: this package is referenced by both
// `internal/dispatch` (Start + WithTurnID at POST entry) and
// `internal/session` (accumulator's TurnIDFromContext + Append at
// each persisted message). Splitting it out of `internal/session`
// avoids a circular import — `dispatch` already imports `session`,
// so the registry has to live elsewhere. Keeping it under
// `internal/turn/` also lets Phase 2's HTTP handler import the
// registry without dragging the full `session` surface.
//
// Concurrency: `Registry` is goroutine-safe under a single
// `sync.Mutex`. Every method acquires the mutex for the duration of
// the call; copies are returned by value so callers never observe
// shared slice/map state. The expected per-Registry call rate is at
// most a few per second (one per POST handler entry + a few per
// streaming chunk), so mutex contention is not a concern at v1
// scale. Per-session sharding is a v2 concern if the chunk-rate ever
// pushes the mutex into a hot path.
//
// Persistence: in-memory only at v1. Turns predating server restart
// return `ErrTurnNotFound` from `Get`. The Phase 2 HTTP handler maps
// that error to 404. Persistence across restarts is out of scope per
// the plan's Constraints section.
package turn

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/session"
)

// Status is the lifecycle stage of a Turn. Transitions are
// monotonic: a Turn starts in StatusRunning and ends in either
// StatusCompleted (clean stream completion) or StatusFailed
// (provider error / engine error). Once terminal, the Turn is
// frozen — subsequent Append / Complete / Fail calls return an
// error rather than silently mutating the row.
type Status string

const (
	// StatusRunning is the initial state. Set by Start, cleared by
	// Complete or Fail. Polls in this state return MessagesAdded as
	// it grows; the client SHOULD keep polling.
	StatusRunning Status = "running"
	// StatusCompleted is the clean-completion terminal state. Set by
	// Complete. The Turn's CompletedAt is non-nil; Error is empty.
	StatusCompleted Status = "completed"
	// StatusFailed is the error terminal state. Set by Fail. The
	// Turn's CompletedAt is non-nil; Error carries the engine /
	// provider failure message that surfaced the failover-or-otherwise
	// terminal failure. Per the plan's acceptance criterion #7,
	// mid-stream provider FAILOVER is NOT a Failed turn — only a
	// genuine engine error after failover exhaustion is.
	StatusFailed Status = "failed"
)

// ModelInfo carries the (provider, model) pair the Turn ran under.
// Populated when Complete fires so the frontend can label the turn
// with the actual model that produced it (e.g. "glm-4.6 via z.ai").
// Empty during StatusRunning.
type ModelInfo struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// ContextUsage mirrors the engine's context_usage chunk payload (engine.go
// contextUsagePayload at engine.go:3237) onto the Turn registry so the
// long-poll wire surfaces the live context-window saturation figure.
// Phase-5 §1c-β replaces the SSE side-channel for the chat-UI's usage chip.
//
// The struct is defined HERE rather than imported from internal/api because
// the api package imports internal/turn (handleGetTurn reads turnResponse
// off the Turn); a reverse import would cycle. The field names + JSON tags
// mirror sseContextUsage at internal/api/sse_writers.go:142-150 exactly so
// the FE parser sees the same wire shape whether the payload comes from the
// turn endpoint or the SSE channel.
//
// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-β.
type ContextUsage struct {
	InputTokens   int    `json:"input_tokens"`
	OutputReserve int    `json:"output_reserve"`
	Limit         int    `json:"limit"`
	Percentage    int    `json:"percentage"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
}

// ProviderQuotaSnapshot mirrors the engine's provider_quota chunk payload
// (sseProviderQuota at internal/api/sse_writers.go:176-189) onto the Turn
// registry so the long-poll wire surfaces per-provider quota state.
// Phase-5 §1c-β — replaces the SSE side-channel for the toolbar quota chip.
//
// Multi-value semantics: Turn.ProviderQuotas is a slice rather than a single
// snapshot because a long stream can carry multiple partitions (anthropic +
// zai after a failover, anthropic + openai across @-mention swarm hops). The
// partition key is `Provider:AccountHash:Model` (snapshotKey on the FE side).
// UpsertProviderQuota replaces an existing partition's snapshot, appends a new
// one, mirroring the FE's quotaStore.snapshots map shape — Option B in the
// 1c-β brief.
//
// Same import-cycle reasoning as ContextUsage: types live here so the turn
// package owns the wire shape without dragging internal/api.
//
// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-β.
type ProviderQuotaSnapshot struct {
	Provider      string                     `json:"provider"`
	AccountHash   string                     `json:"account_hash"`
	Model         string                     `json:"model,omitempty"`
	ObservedAt    string                     `json:"observed_at"`
	Stale         bool                       `json:"stale,omitempty"`
	StoreBackend  string                     `json:"store_backend,omitempty"`
	PricingSource string                     `json:"pricing_source,omitempty"`
	Variant       string                     `json:"variant"`
	RateLimit     *ProviderQuotaRateLimit    `json:"rate_limit,omitempty"`
	TokenSpend    *ProviderQuotaTokenSpend   `json:"token_spend,omitempty"`
	NotConfigured *ProviderQuotaNotConfig    `json:"not_configured,omitempty"`
}

// ProviderQuotaRateLimit mirrors sseProviderQuotaRateLimit at
// internal/api/sse_writers.go:193-200.
type ProviderQuotaRateLimit struct {
	Requests                 ProviderQuotaWindow `json:"requests"`
	Tokens                   ProviderQuotaWindow `json:"tokens"`
	Input                    ProviderQuotaWindow `json:"input"`
	Output                   ProviderQuotaWindow `json:"output"`
	TightestPercentRemaining int                 `json:"tightest_percent_remaining"`
	TightestResetAt          string              `json:"tightest_reset_at,omitempty"`
}

// ProviderQuotaWindow mirrors sseQuotaWindow at
// internal/api/sse_writers.go:204-208. -1 sentinel means "not provided".
type ProviderQuotaWindow struct {
	Limit     int    `json:"limit"`
	Remaining int    `json:"remaining"`
	Reset     string `json:"reset,omitempty"`
}

// ProviderQuotaTokenSpend mirrors sseProviderQuotaTokenSpend at
// internal/api/sse_writers.go:212-223.
type ProviderQuotaTokenSpend struct {
	SpentMinor     int64  `json:"spent_minor"`
	SpentCurrency  string `json:"spent_currency"`
	SpentUSDMinor  int64  `json:"spent_usd_minor"`
	CapMinor       int64  `json:"cap_minor,omitempty"`
	CapCurrency    string `json:"cap_currency,omitempty"`
	Period         string `json:"period"`
	PeriodStart    string `json:"period_start"`
	PeriodEnd      string `json:"period_end"`
	ThresholdAmber int    `json:"threshold_amber"`
	ThresholdRed   int    `json:"threshold_red"`
}

// ProviderQuotaNotConfig mirrors sseProviderQuotaNotConfig at
// internal/api/sse_writers.go:227-229. Reason is operator-visible verbatim.
type ProviderQuotaNotConfig struct {
	Reason string `json:"reason"`
}

// partitionKey returns the partition-key string for a quota snapshot —
// matches snapshotKey() on the FE side (quotaStore.ts:56-58). The
// per-partition slice semantics in UpsertProviderQuota dedup on this key.
func (s ProviderQuotaSnapshot) partitionKey() string {
	return s.Provider + ":" + s.AccountHash + ":" + s.Model
}

// Turn is the first-class per-streaming-pass resource. One Turn is
// minted per POST /messages call (Phase 2 will wire the handler);
// the engine pipeline tags chunks with the Turn's ID via context.
// Every message the accumulator persists during the turn lands in
// MessagesAdded, in arrival order, and grows monotonically until the
// turn reaches a terminal state.
//
// MessagesAdded MUST hold engine-emitted rows only (assistant,
// thinking, tool_call, tool_result, delegation). The user message
// that triggered the turn is NOT duplicated here — it lives in the
// POST response's `snapshot` field (Phase 2 wires this). Polling on
// `GET /turns/{turn_id}` returns MessagesAdded as it stands at poll
// time; clients append the delta to local state.
type Turn struct {
	ID            string            `json:"id"`
	SessionID     string            `json:"session_id"`
	Status        Status            `json:"status"`
	StartedAt     time.Time         `json:"started_at"`
	CompletedAt   *time.Time        `json:"completed_at,omitempty"`
	Model         ModelInfo         `json:"model"`
	Error         string            `json:"error,omitempty"`
	MessagesAdded []session.Message `json:"messages_added"`
	// Phase is the most-recent streaming-heartbeat phase observed for
	// this Turn ("queued" | "thinking" | "generating"). Populated by
	// SetHeartbeat from the engine's `events.EventStreamingHeartbeat`
	// bus subscription so `GET /turns/{id}` can surface live progress
	// without an SSE side-channel. Empty during the brief window
	// between Start and the first heartbeat; frozen at the last value
	// once the Turn reaches a terminal state.
	//
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Turn-Based Post-Then-Poll Architecture (May 2026).md §4d
	//   Commit 1 (heartbeat-on-turn).
	Phase string `json:"phase,omitempty"`
	// TokenCount mirrors the in-flight turn's cumulative output_tokens
	// as reported by the provider's most recent UsageDelta (Anthropic
	// message_delta, openaicompat trailing-chunk usage). Populated by
	// SetHeartbeat alongside Phase. Zero is the legitimate pre-first-
	// UsageDelta value; the chat UI's live-counter chrome gates the
	// render on >0 so a fresh turn does not flash a misleading "0
	// tokens".
	TokenCount int `json:"token_count,omitempty"`
	// CurrentProvider mirrors the provider id the engine is CURRENTLY
	// streaming under (e.g. "anthropic", "zai", "openai"). Distinct from
	// Model.Provider — Model is the post-Complete frozen snapshot stamped
	// by the wrap goroutine's terminal call, whereas CurrentProvider
	// surfaces the live pair WHILE the Turn is Running so long-poll
	// consumers (the chat UI's toolbar chip) can react to a mid-stream
	// failover without waiting for the terminal transition.
	//
	// Populated by SetProviderModel, wired off the dispatcher's chunk-tap
	// for `provider_changed` and `model_active` event chunks. Empty during
	// the brief window between Start and the first model_active chunk;
	// frozen at the last live value once the Turn reaches a terminal state.
	//
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-α.
	CurrentProvider string `json:"current_provider,omitempty"`
	// CurrentModel mirrors the model id paired with CurrentProvider. Same
	// lifecycle semantics as CurrentProvider — populated by SetProviderModel,
	// read on every poll, frozen at the last live value post-terminal.
	CurrentModel string `json:"current_model,omitempty"`
	// ContextUsage mirrors the most recent `context_usage` chunk payload the
	// engine emitted during this Turn. Populated by SetContextUsage, wired
	// off the dispatcher's wrapWithTurnLifecycle chunk-tap on `context_usage`
	// events. nil during the brief window between Start and the first
	// context_usage chunk; frozen at the last value once the Turn reaches a
	// terminal state (per the same Phase + TokenCount + CurrentProvider
	// "frozen post-terminal" pattern).
	//
	// Pointer-typed so the absent state is unambiguously nil (vs a
	// zero-valued struct which the JSON-marshalled wire would emit with
	// all-zero fields — confusing to the FE parser that gates on
	// "non-empty payload"). The omitempty tag drops the field entirely
	// when nil.
	//
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-β.
	ContextUsage *ContextUsage `json:"context_usage,omitempty"`
	// ProviderQuotas mirrors the cumulative set of `provider_quota` chunk
	// payloads the engine emitted during this Turn, partitioned by
	// `Provider:AccountHash:Model`. Populated by UpsertProviderQuota,
	// wired off the dispatcher's chunk-tap on `provider_quota` events.
	// Multi-value because a single stream can carry multiple partitions
	// (anthropic + zai after failover, anthropic + openai across @-mention
	// swarm hops); each partition's most-recent snapshot wins.
	//
	// Empty slice (nil) during the brief window between Start and the
	// first provider_quota chunk; entries accumulate as the engine emits
	// per-provider snapshots; frozen at the last set once the Turn
	// reaches a terminal state.
	//
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-β.
	ProviderQuotas []ProviderQuotaSnapshot `json:"provider_quotas,omitempty"`
}

// ErrTurnConflict fires when Start is called on a session that
// already has a Turn in StatusRunning. Phase 2's HTTP handler maps
// this to 409 Conflict; the wire contract from the plan's "User-
// chosen design" section is explicit on this: only one in-flight
// turn per session at v1. Multi-turn parallelism is v2.
var ErrTurnConflict = errors.New("turn: conflict — a turn is already running for this session")

// ErrTurnNotFound fires when Get is called for a turn_id that the
// registry does not hold. Phase 2's HTTP handler maps this to 404.
// At v1, turn_ids predating server restart return this error.
var ErrTurnNotFound = errors.New("turn: not found")

// ErrTurnTerminal fires when Append / Complete / Fail is called on
// a turn that has already reached StatusCompleted or StatusFailed.
// Indicates a producer-side ordering bug (e.g. the accumulator
// appending after the dispatcher's wrap goroutine already called
// Complete). Surfaced rather than silently swallowed so the bug is
// observable in test output.
var ErrTurnTerminal = errors.New("turn: already in terminal state")

// turnIDKey is the unexported context-key type for turn_id
// propagation. Per the Go context-key convention (an unexported
// zero-sized type prevents key collisions across packages), only
// this package can write the value — callers use WithTurnID /
// TurnIDFromContext as the typed gate.
type turnIDKey struct{}

// WithTurnID returns a context carrying the supplied turn_id.
// Called by the dispatcher at POST-handler entry — the resulting
// context is then handed to the streamer so every downstream
// consumer (engine, accumulator) can read the turn_id with
// TurnIDFromContext without any explicit plumbing.
//
// Expected:
//   - parent is the caller's context.
//   - id is the freshly-minted turn UUID. Empty strings are stored
//     verbatim — a downstream TurnIDFromContext call surfaces the
//     empty string and ok=false, mirroring an absent value.
//
// Returns:
//   - A derived context carrying the turn_id under turnIDKey{}.
//
// Side effects:
//   - None.
func WithTurnID(parent context.Context, id string) context.Context {
	return context.WithValue(parent, turnIDKey{}, id)
}

// TurnIDFromContext extracts the turn_id stored under turnIDKey{}.
// Returns ("", false) when no turn_id is set OR when an empty
// string was stored (treated as absent for symmetry with the
// "value not present" path).
//
// Expected:
//   - ctx is any context — typically the streamer ctx threaded
//     through the engine into the accumulator.
//
// Returns:
//   - id is the turn_id; "" when absent.
//   - ok is true iff a non-empty turn_id was stored.
//
// Side effects:
//   - None.
func TurnIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v := ctx.Value(turnIDKey{})
	if v == nil {
		return "", false
	}
	id, _ := v.(string)
	if id == "" {
		return "", false
	}
	return id, true
}

// Registry is the in-memory store of live + terminal turns. A
// single Registry instance is shared across the Dispatcher and the
// accumulator (wired in app construction). All state lives under a
// single mutex; the value semantics on Get/Snapshot mean callers
// never observe shared internal state.
//
// Internal layout:
//   - byID maps turn_id → *Turn for O(1) Get/Append/Complete/Fail
//     lookup.
//   - byActiveSession maps sessionID → turn_id, populated when a
//     turn is StatusRunning and cleared on Complete/Fail. Used by
//     Start to detect the ErrTurnConflict case.
type Registry struct {
	mu              sync.Mutex
	byID            map[string]*Turn
	byActiveSession map[string]string
	// idGen mints fresh turn UUIDs. Unexported so production
	// always uses google/uuid.NewString while tests can swap in
	// a deterministic generator via NewRegistryWithIDGen.
	idGen func() string
	// clock returns the current time. Unexported so tests can
	// pin StartedAt / CompletedAt without sleeping. Production
	// uses time.Now.
	clock func() time.Time
	// changeCh is the registry-wide change-broadcast channel for
	// long-poll waiters. Closed-and-replaced on every Append /
	// SetHeartbeat / Complete / Fail so concurrent WaitForChange
	// callers all wake on a single mutation (close broadcasts to
	// every receiver, unlike a buffered-send which would round-robin
	// across goroutines).
	//
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Turn-Based Post-Then-Poll Architecture (May 2026).md §4d
	//   Commit 1b (long-poll endpoint).
	//
	// Concurrency: read + replaced under r.mu. Callers capture the
	// channel pointer under lock, drop the lock, then range over
	// the captured reference. A subsequent mutation re-points
	// r.changeCh to a fresh channel — the previous channel is
	// closed and the captured reference fires, then becomes
	// permanently unreachable once all waiters drop their copy.
	changeCh chan struct{}
}

// NewRegistry constructs a Registry wired to production-grade
// dependencies (google/uuid for turn ids; time.Now for timestamps).
// Test fakes use NewRegistryWithIDGen to inject a deterministic
// generator and clock.
//
// Returns:
//   - A configured Registry. The zero value is NOT usable —
//     internal maps must be initialised, which only this
//     constructor (and NewRegistryWithIDGen) does.
//
// Side effects:
//   - None.
func NewRegistry() *Registry {
	return NewRegistryWithIDGen(defaultIDGen, time.Now)
}

// NewRegistryWithIDGen constructs a Registry with a custom id
// generator + clock. Phase 1 callers in production use NewRegistry;
// tests use this variant so turn ids are predictable and CompletedAt
// stamps line up with assertions. nil-tolerant: a nil idGen falls
// back to the production generator; a nil clock falls back to
// time.Now.
//
// Returns:
//   - A configured Registry.
//
// Side effects:
//   - None.
func NewRegistryWithIDGen(idGen func() string, clock func() time.Time) *Registry {
	if idGen == nil {
		idGen = defaultIDGen
	}
	if clock == nil {
		clock = time.Now
	}
	return &Registry{
		byID:            make(map[string]*Turn),
		byActiveSession: make(map[string]string),
		idGen:           idGen,
		clock:           clock,
		changeCh:        make(chan struct{}),
	}
}

// broadcastChangeLocked closes the current change-notification channel
// and replaces it with a fresh one so subsequent WaitForChange callers
// receive a new wait token. MUST be called with r.mu held — the close
// + reassignment must be atomic w.r.t. peer mutations and w.r.t. the
// snapshot-then-capture-channel sequence inside WaitForChange.
//
// Side effects:
//   - Closes the existing changeCh. Every waiter blocked on a receive
//     from that channel wakes simultaneously (the canonical
//     "broadcast" idiom for close-of-channel).
//   - Replaces changeCh with a freshly-allocated chan struct{} so the
//     next WaitForChange call gets a live token.
func (r *Registry) broadcastChangeLocked() {
	close(r.changeCh)
	r.changeCh = make(chan struct{})
}

// Start mints a fresh turn_id for the supplied sessionID, records
// the turn in StatusRunning, and returns the new id. When the
// session already has a Running turn, returns ErrTurnConflict —
// the Phase 2 HTTP handler maps that to 409 Conflict per the
// plan's "v1 supports ONE in-flight turn per session" rule.
//
// Expected:
//   - sessionID is non-empty. An empty sessionID is accepted
//     verbatim (the registry doesn't validate) but the conflict
//     check still applies — two empty-sessionID Starts will
//     conflict.
//
// Returns:
//   - turnID is the freshly-minted UUID. Empty on error.
//   - err is ErrTurnConflict when the session already has a
//     Running turn; nil otherwise.
//
// Side effects:
//   - Inserts a fresh Turn into byID and byActiveSession.
func (r *Registry) Start(sessionID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existingID, ok := r.byActiveSession[sessionID]; ok {
		// Verify the existing turn is genuinely Running — if a
		// terminal-transition raced an active-map cleanup we should
		// not block the new Start. Defence in depth; the production
		// path always clears byActiveSession on Complete/Fail.
		if existing, found := r.byID[existingID]; found && existing.Status == StatusRunning {
			return "", ErrTurnConflict
		}
		// Stale entry — clear it so a clean Start can proceed.
		delete(r.byActiveSession, sessionID)
	}

	id := r.idGen()
	t := &Turn{
		ID:            id,
		SessionID:     sessionID,
		Status:        StatusRunning,
		StartedAt:     r.clock(),
		MessagesAdded: []session.Message{},
	}
	r.byID[id] = t
	r.byActiveSession[sessionID] = id
	return id, nil
}

// Append records a message persisted during the turn into
// MessagesAdded, in arrival order. No-op when turnID is empty
// (matches the "no turn_id in ctx" path so the accumulator can
// call Append unconditionally without a nil check at every site).
// Returns ErrTurnNotFound when the turnID is unknown and
// ErrTurnTerminal when the turn has already transitioned to
// Completed / Failed.
//
// Expected:
//   - turnID is the turn_id from TurnIDFromContext. Empty is a
//     no-op (returns nil).
//   - msg is the engine-emitted message the accumulator just
//     persisted. The plan's "MessagesAdded precise definition"
//     section restricts this to assistant / thinking / tool_call /
//     tool_result / delegation rows — the accumulator already
//     filters by chunk type before calling Append, so the registry
//     does not re-check the Role field.
//
// Returns:
//   - nil on success or empty turnID.
//   - ErrTurnNotFound when turnID is unknown.
//   - ErrTurnTerminal when the turn is in a terminal state.
//
// Side effects:
//   - Appends msg to the turn's MessagesAdded slice.
func (r *Registry) Append(turnID string, msg session.Message) error {
	if turnID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.byID[turnID]
	if !ok {
		return ErrTurnNotFound
	}
	if t.Status != StatusRunning {
		return ErrTurnTerminal
	}
	t.MessagesAdded = append(t.MessagesAdded, msg)
	// Wake any long-poll waiters parked on changeCh — MessagesAdded
	// grew past their captured baseline.
	r.broadcastChangeLocked()
	return nil
}

// Complete transitions a Running turn to StatusCompleted, stamps
// CompletedAt, captures the (provider, model) pair, and clears
// the byActiveSession entry so the next Start for this sessionID
// proceeds without an ErrTurnConflict. No-op on empty turnID for
// symmetric ergonomics with Append.
//
// Expected:
//   - turnID is the turn_id from TurnIDFromContext.
//   - info is the (provider, model) pair the turn ran under.
//     Empty fields are tolerated — the frontend renders them as
//     "unknown" when missing.
//
// Returns:
//   - nil on success.
//   - ErrTurnNotFound when turnID is unknown.
//   - ErrTurnTerminal when the turn is already in a terminal state.
//
// Side effects:
//   - Mutates the Turn's Status, CompletedAt, Model.
//   - Removes the byActiveSession entry for this turn's sessionID.
func (r *Registry) Complete(turnID string, info ModelInfo) error {
	if turnID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.byID[turnID]
	if !ok {
		return ErrTurnNotFound
	}
	if t.Status != StatusRunning {
		return ErrTurnTerminal
	}
	now := r.clock()
	t.Status = StatusCompleted
	t.CompletedAt = &now
	t.Model = info
	if active, found := r.byActiveSession[t.SessionID]; found && active == turnID {
		delete(r.byActiveSession, t.SessionID)
	}
	// Wake any long-poll waiters — the Turn just reached a terminal
	// state and they must observe it without waiting out the timeout.
	r.broadcastChangeLocked()
	return nil
}

// Fail transitions a Running turn to StatusFailed, stamps
// CompletedAt, captures the failure cause as Error, and clears
// the byActiveSession entry. Mirrors Complete's semantics —
// either Complete or Fail fires exactly once per turn.
//
// Expected:
//   - turnID is the turn_id from TurnIDFromContext.
//   - cause is the engine / provider error. A nil cause is
//     tolerated (records an empty Error); the typical wire path
//     always carries a non-nil cause.
//
// Returns:
//   - nil on success.
//   - ErrTurnNotFound when turnID is unknown.
//   - ErrTurnTerminal when the turn is already in a terminal state.
//
// Side effects:
//   - Mutates the Turn's Status, CompletedAt, Error.
//   - Removes the byActiveSession entry for this turn's sessionID.
func (r *Registry) Fail(turnID string, cause error) error {
	if turnID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.byID[turnID]
	if !ok {
		return ErrTurnNotFound
	}
	if t.Status != StatusRunning {
		return ErrTurnTerminal
	}
	now := r.clock()
	t.Status = StatusFailed
	t.CompletedAt = &now
	if cause != nil {
		t.Error = cause.Error()
	}
	if active, found := r.byActiveSession[t.SessionID]; found && active == turnID {
		delete(r.byActiveSession, t.SessionID)
	}
	// Wake any long-poll waiters — terminal-state transition; same
	// reasoning as Complete.
	r.broadcastChangeLocked()
	return nil
}

// FindActiveBySession returns the turn_id of the currently-Running
// turn for the supplied sessionID, or ("", false) when no Running
// turn exists. Backed by the existing byActiveSession O(1) map — no
// map scan. Used by the Phase-4-Commit-1 wire surface to project
// `activeTurnId` onto session list responses (server.go's
// handleListV1Sessions) so the chat UI can resolve "is there a
// live turn for this session and what is its id" in one round-trip.
//
// Defence in depth: even though byActiveSession is cleared on
// Complete/Fail, this method re-verifies the looked-up Turn's
// Status == StatusRunning before returning. A terminal Turn whose
// byActiveSession entry survived a Complete/Fail race (shouldn't
// happen given the lock holds the cleanup atomic, but treat as a
// stale-entry case) surfaces as ("", false).
//
// Concurrency: acquires r.mu via Lock — the Registry's mutex is a
// sync.Mutex, NOT an RWMutex, so callers serialise against every
// peer method (Start, Append, Complete, Fail, Get, SetHeartbeat).
// Upgrading to RWMutex would be gratuitous scope; the registry's
// expected call rate is at most a few per second per session.
//
// Expected:
//   - sessionID is non-empty. An empty sessionID is accepted
//     verbatim and looked up; absent entries simply return
//     ("", false).
//
// Returns:
//   - turnID is the Turn UUID of the running turn for this session,
//     or "" when none exists.
//   - ok is true iff a running turn exists for this session.
//
// Side effects:
//   - None — read-only on the byActiveSession + byID maps.
func (r *Registry) FindActiveBySession(sessionID string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	id, ok := r.byActiveSession[sessionID]
	if !ok {
		return "", false
	}
	t, found := r.byID[id]
	if !found || t.Status != StatusRunning {
		return "", false
	}
	return id, true
}

// SetHeartbeat records the most-recent streaming-heartbeat phase +
// cumulative output_tokens onto a Running Turn. Wired to the engine's
// `events.EventStreamingHeartbeat` bus subscription so `GET /turns/{id}`
// can surface live progress (the chat UI's chip + live token counter)
// without an SSE side-channel. Per the plan's Phase-4-Commit-1 spec,
// this is the polling-side replacement for `internal/api/event_bridge.go:46`
// — both subscribers ship together; the SSE-side bridge is retired in
// Commit 2.
//
// No-op semantics:
//   - empty turnID — silent return. Bus subscribers derive turnID
//     from sessionID via FindActiveBySession; a race against
//     Complete/Fail can surface "" and must not crash.
//   - unknown turnID — silent return. Same race shape as above; the
//     registry's byID entry may have been cleared between the
//     FindActive lookup and this call.
//   - non-Running turnID — silent return. Late heartbeat firing
//     after the wrap goroutine called Complete/Fail; the terminal-
//     state Turn's Phase + TokenCount stay frozen at their last
//     Running values, which is the correct user-facing semantics
//     (the chip should not "resume" on a terminal turn).
//
// Concurrency: acquires r.mu via Lock. The Phase + TokenCount pair
// is written under the same lock peer methods use, so readers
// (Get / FindActiveBySession) never observe a torn pair.
//
// Expected:
//   - turnID is the Turn UUID. Empty is a silent no-op.
//   - phase is the streaming phase ("queued" | "thinking" |
//     "generating"). The registry does not validate the closed
//     vocabulary — the engine's events package owns that contract.
//   - tokenCount is the cumulative output_tokens from the provider's
//     most recent UsageDelta. Zero is the legitimate pre-first-
//     UsageDelta value.
//
// Returns:
//   - None. No error path — every condition that would otherwise
//     surface an error (absent turn, terminal turn, empty id) is a
//     race the bus subscriber cannot reasonably handle, so the
//     registry absorbs them silently.
//
// Side effects:
//   - Mutates the Turn's Phase + TokenCount when the Turn is
//     Running. Otherwise no observable side effect.
func (r *Registry) SetHeartbeat(turnID, phase string, tokenCount int) {
	if turnID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.byID[turnID]
	if !ok {
		return
	}
	if t.Status != StatusRunning {
		return
	}
	// Only broadcast when an observable field actually changes.
	// Spurious heartbeat ticks (engine writes the same phase + tokens
	// twice during a quiet streaming gap) must NOT wake long-poll
	// waiters, otherwise the FE long-poll loop spins on no-op snapshots
	// and the perceived-cadence promise degrades. WaitForChange's
	// recheck-after-wake path handles spurious wakes correctly even
	// without this gate, but skipping the broadcast saves wake churn.
	changed := t.Phase != phase || t.TokenCount != tokenCount
	t.Phase = phase
	t.TokenCount = tokenCount
	if changed {
		r.broadcastChangeLocked()
	}
}

// SetProviderModel records the engine's live (provider, model) pair onto
// a Running Turn. Wired off the dispatcher's wrapWithTurnLifecycle chunk-
// tap so `provider_changed` and `model_active` chunks land on the Turn's
// `CurrentProvider` + `CurrentModel` fields, which the long-poll wire
// surfaces as `current_provider` + `current_model`. The FE's poll-diff
// loop reads those fields and pivots the chat-UI's toolbar chip without
// waiting for the SSE side-channel.
//
// No-op semantics (mirrors SetHeartbeat — the chunk-tap fires from a
// goroutine that can race the wrap's Complete/Fail):
//   - empty turnID — silent return.
//   - unknown turnID — silent return.
//   - non-Running turnID — silent return (Phase + TokenCount + CurrentX
//     all freeze at their last Running values).
//
// Broadcast gate: this method only broadcasts when the (provider, model)
// pair ACTUALLY moves past what the registry already holds. Every chunk
// in a long stream carries ProviderID/ModelID; an unconditional broadcast
// would degrade the long-poll's perceived-cadence promise to spin on
// every chunk. The change-gate matches SetHeartbeat's identical pattern
// at the Phase + TokenCount fields.
//
// Concurrency: acquires r.mu via Lock. The pair is written under the
// same lock peer methods use so readers (Get / WaitForChange) never
// observe a torn pair.
//
// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-α.
func (r *Registry) SetProviderModel(turnID, provider, model string) {
	if turnID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.byID[turnID]
	if !ok {
		return
	}
	if t.Status != StatusRunning {
		return
	}
	changed := t.CurrentProvider != provider || t.CurrentModel != model
	t.CurrentProvider = provider
	t.CurrentModel = model
	if changed {
		r.broadcastChangeLocked()
	}
}

// SetContextUsage records the most-recent context_usage payload onto a
// Running Turn. Wired off the dispatcher's wrapWithTurnLifecycle chunk-tap
// on `context_usage` chunks (the engine emits these as the first artefact
// of every Stream that has enough information to compute the figure —
// see engine.go:3262 buildContextUsageChunk). The long-poll wire then
// surfaces ContextUsage onto the GET /turns/{id} response so the FE's
// chat-UI usage chip pivots without an SSE side-channel.
//
// No-op semantics mirror SetProviderModel — the chunk-tap fires from a
// goroutine that can race the wrap's terminal Complete/Fail:
//   - empty turnID — silent return.
//   - unknown turnID — silent return.
//   - non-Running turnID — silent return (ContextUsage frozen at the
//     last Running-state value, matching CurrentProvider/CurrentModel).
//   - nil cu — silent return (defensive — the dispatcher tap only calls
//     this with a non-nil pointer when the chunk payload parses; the
//     guard absorbs a future producer-side bug).
//
// Broadcast gate: only fires the change broadcast when the payload's
// field values DIFFER from the existing stored snapshot. Pointer
// equality is NOT the gate — a fresh allocation with the same field
// values must be a no-op so engine restamps during quiet streaming gaps
// do not spin the long-poll perceived-cadence promise. The dereference-
// then-equal pattern matches sseContextUsage's six primitive fields
// (input_tokens, output_reserve, limit, percentage, provider, model).
//
// Concurrency: acquires r.mu via Lock. The pointer write is under the
// same lock peer methods use so readers (Get / WaitForChange) never
// observe a torn ContextUsage value.
//
// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-β.
func (r *Registry) SetContextUsage(turnID string, cu *ContextUsage) {
	if turnID == "" || cu == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.byID[turnID]
	if !ok {
		return
	}
	if t.Status != StatusRunning {
		return
	}
	// Change-gate: dereference both sides for field-by-field equality so
	// a fresh allocation with identical primitives stays a no-op. nil-old
	// vs non-nil-new always counts as a change (the empty → real
	// transition the chip's first-render path waits on).
	changed := t.ContextUsage == nil || *t.ContextUsage != *cu
	// Copy the pointee so the registry owns its memory — callers can
	// mutate their input after the call without poisoning the snapshot.
	cuCopy := *cu
	t.ContextUsage = &cuCopy
	if changed {
		r.broadcastChangeLocked()
	}
}

// UpsertProviderQuota records a provider_quota snapshot onto a Running
// Turn's ProviderQuotas slice. Multi-value with partition-key dedup
// semantics (Option B in the 1c-β brief): if a snapshot with the same
// `Provider:AccountHash:Model` partition key already exists in the
// slice, REPLACE it (newest wins); otherwise APPEND. Mirrors the FE
// quotaStore's snapshots map shape so the FE consumer sees the same
// data whether it routes via SSE or via this long-poll surface.
//
// No-op semantics mirror SetContextUsage / SetProviderModel:
//   - empty turnID — silent return.
//   - unknown turnID — silent return.
//   - non-Running turnID — silent return.
//
// Broadcast gate: only fires when the snapshot DIFFERS from the existing
// entry for the same partition (or no entry exists, which is an append-
// new-partition transition the FE diff loop must observe). The variant
// payloads (RateLimit / TokenSpend / NotConfigured) are pointer fields
// so the deep-equal check must reach inside them — implemented via
// helper providerQuotaEqual.
//
// Concurrency: acquires r.mu via Lock. The slice mutation is under the
// same lock peer methods use so readers (Get / WaitForChange) never
// observe a torn slice (mid-append) or a torn snapshot entry.
//
// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-β.
func (r *Registry) UpsertProviderQuota(turnID string, snap ProviderQuotaSnapshot) {
	if turnID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.byID[turnID]
	if !ok {
		return
	}
	if t.Status != StatusRunning {
		return
	}
	// Deep-copy the snapshot so the registry owns its memory — callers
	// can mutate variant pointer payloads after the call without
	// poisoning the stored snapshot.
	stored := deepCopyProviderQuota(snap)
	key := stored.partitionKey()
	changed := true
	replaced := false
	for i := range t.ProviderQuotas {
		if t.ProviderQuotas[i].partitionKey() == key {
			if providerQuotaEqual(t.ProviderQuotas[i], stored) {
				changed = false
			}
			t.ProviderQuotas[i] = stored
			replaced = true
			break
		}
	}
	if !replaced {
		t.ProviderQuotas = append(t.ProviderQuotas, stored)
		// Append always counts as a change — new partition the FE
		// hasn't observed.
		changed = true
	}
	if changed {
		r.broadcastChangeLocked()
	}
}

// deepCopyProviderQuota produces an independent copy of snap including
// each variant payload. Required so a caller's mutation of e.g.
// snap.TokenSpend after the Upsert call does not leak into the registry's
// stored snapshot.
func deepCopyProviderQuota(snap ProviderQuotaSnapshot) ProviderQuotaSnapshot {
	out := snap
	if snap.RateLimit != nil {
		rl := *snap.RateLimit
		out.RateLimit = &rl
	}
	if snap.TokenSpend != nil {
		ts := *snap.TokenSpend
		out.TokenSpend = &ts
	}
	if snap.NotConfigured != nil {
		nc := *snap.NotConfigured
		out.NotConfigured = &nc
	}
	return out
}

// providerQuotaEqual reports field-equality across two snapshots,
// including the variant payloads. nil-vs-nil counts as equal; nil-vs-
// non-nil counts as different. Used by the UpsertProviderQuota broadcast
// gate to suppress spurious wakes during quiet streaming gaps.
func providerQuotaEqual(a, b ProviderQuotaSnapshot) bool {
	if a.Provider != b.Provider ||
		a.AccountHash != b.AccountHash ||
		a.Model != b.Model ||
		a.ObservedAt != b.ObservedAt ||
		a.Stale != b.Stale ||
		a.StoreBackend != b.StoreBackend ||
		a.PricingSource != b.PricingSource ||
		a.Variant != b.Variant {
		return false
	}
	if (a.RateLimit == nil) != (b.RateLimit == nil) {
		return false
	}
	if a.RateLimit != nil && *a.RateLimit != *b.RateLimit {
		return false
	}
	if (a.TokenSpend == nil) != (b.TokenSpend == nil) {
		return false
	}
	if a.TokenSpend != nil && *a.TokenSpend != *b.TokenSpend {
		return false
	}
	if (a.NotConfigured == nil) != (b.NotConfigured == nil) {
		return false
	}
	if a.NotConfigured != nil && *a.NotConfigured != *b.NotConfigured {
		return false
	}
	return true
}

// contextUsageDiffers reports whether the live ContextUsage value differs
// from the caller's baseline. nil-vs-nil is no-difference; nil-vs-non-nil
// is a difference (the empty → real transition the FE's first-render path
// waits on); non-nil-vs-non-nil dereferences both sides for field equality.
// Used by WaitForChange's predicate.
func contextUsageDiffers(live, baseline *ContextUsage) bool {
	if live == nil && baseline == nil {
		return false
	}
	if live == nil || baseline == nil {
		return true
	}
	return *live != *baseline
}

// providerQuotasDiffer reports whether the live ProviderQuotas slice
// differs from the caller's baseline. Comparison is element-by-element
// in slice order — UpsertProviderQuota preserves slice order under
// replace-in-place, so a baseline captured at moment T against a live
// snapshot at moment T+1 reads the same indices for the same partitions
// (a fresh partition appends, never inserts mid-slice). Length change OR
// any element differing by providerQuotaEqual counts as a difference.
// Used by WaitForChange's predicate.
func providerQuotasDiffer(live, baseline []ProviderQuotaSnapshot) bool {
	if len(live) != len(baseline) {
		return true
	}
	for i := range live {
		if !providerQuotaEqual(live[i], baseline[i]) {
			return true
		}
	}
	return false
}

// WaitForChange is the Phase-4-Commit-1b long-poll primitive. Returns
// when ANY of the following becomes true:
//   - len(turn.MessagesAdded) > sinceMsgCount
//   - turn.Phase != lastPhase
//   - turn.TokenCount != lastTokens
//   - turn.CurrentProvider != lastProvider (Phase-5 §1c-α)
//   - turn.CurrentModel != lastModel (Phase-5 §1c-α)
//   - turn.ContextUsage differs from lastContextUsage by value (Phase-5 §1c-β)
//   - turn.ProviderQuotas differs from lastProviderQuotas by length OR
//     any element's value (Phase-5 §1c-β)
//   - turn.Status != StatusRunning (terminal-state reached)
//   - timeout elapses (returns the current snapshot with changed=false)
//   - ctx is cancelled (returns the zero snapshot with changed=false)
//
// Implementation pattern: each iteration takes r.mu, evaluates the
// watched fields against the caller's baseline, captures the registry's
// current changeCh reference, then releases r.mu and parks on a select
// over (changeCh, ctx.Done(), timer.C). When changeCh fires (a peer
// mutation closed it), the loop re-acquires r.mu and re-evaluates the
// predicate. The "capture under lock then wait" sequence is the
// canonical "wait for a new state under fine-grained locking" pattern
// — it avoids the lost-wakeup race that a "check then wait" without
// the captured-channel intermediate would exhibit.
//
// Expected:
//   - ctx — caller's context. Cancellation surfaces (zero snapshot,
//     false). Typically the HTTP handler's r.Context() so a client
//     disconnect aborts the wait promptly.
//   - turnID — the Turn UUID. An unknown id returns (zero snapshot,
//     false) synchronously (no waiting); the handler maps to 404.
//   - sinceMsgCount — caller's last-observed len(MessagesAdded). Wake
//     when the registry's len > sinceMsgCount.
//   - lastPhase — caller's last-observed Phase. Wake when the
//     registry's Phase != lastPhase.
//   - lastTokens — caller's last-observed TokenCount. Wake when the
//     registry's TokenCount != lastTokens.
//   - lastProvider — caller's last-observed CurrentProvider. Wake when
//     the registry's CurrentProvider != lastProvider (Phase-5 §1c-α).
//   - lastModel — caller's last-observed CurrentModel. Wake when the
//     registry's CurrentModel != lastModel (Phase-5 §1c-α).
//   - lastContextUsage — caller's last-observed ContextUsage. nil
//     baseline + non-nil registry value counts as a change (Phase-5
//     §1c-β); equal values are a no-wake.
//   - lastProviderQuotas — caller's last-observed ProviderQuotas slice.
//     A change is len() growth OR any per-partition snapshot differing
//     from baseline (Phase-5 §1c-β).
//   - timeout — max wait duration. A zero or negative timeout means
//     "evaluate the predicate once and return immediately".
//
// Returns:
//   - snap — fresh value-typed snapshot of the Turn at the wake
//     moment. Zero-valued struct when turnID is unknown OR ctx
//     cancelled. The MessagesAdded slice is copy-safe (a deep copy of
//     the registry's internal slice).
//   - changed — true iff the wake came from a real predicate-hit
//     (mutation OR terminal OR baseline-already-exceeded). False on
//     timeout, ctx-cancel, or unknown-turnID.
//
// Side effects:
//   - None on the registry. Acquires r.mu briefly on each iteration.
func (r *Registry) WaitForChange(
	ctx context.Context,
	turnID string,
	sinceMsgCount int,
	lastPhase string,
	lastTokens int,
	lastProvider string,
	lastModel string,
	lastContextUsage *ContextUsage,
	lastProviderQuotas []ProviderQuotaSnapshot,
	timeout time.Duration,
) (Turn, bool) {
	// Wall-clock deadline (NOT r.clock()) — the test fakes r.clock to
	// pin StartedAt / CompletedAt onto deterministic timestamps, but
	// the timeout budget here must measure real elapsed time so the
	// long-poll wait actually wakes after the requested wall-clock
	// duration. r.clock is reserved for "what timestamp do we stamp
	// onto the Turn?", NOT "how long should we sleep?".
	deadline := time.Now().Add(timeout)
	for {
		r.mu.Lock()
		t, ok := r.byID[turnID]
		if !ok {
			r.mu.Unlock()
			return Turn{}, false
		}
		// Predicate: any baseline exceeded OR terminal status.
		if len(t.MessagesAdded) > sinceMsgCount ||
			t.Phase != lastPhase ||
			t.TokenCount != lastTokens ||
			t.CurrentProvider != lastProvider ||
			t.CurrentModel != lastModel ||
			contextUsageDiffers(t.ContextUsage, lastContextUsage) ||
			providerQuotasDiffer(t.ProviderQuotas, lastProviderQuotas) ||
			t.Status != StatusRunning {
			snap := r.snapshotLocked(t)
			r.mu.Unlock()
			return snap, true
		}
		// Capture the change channel BEFORE releasing the lock —
		// otherwise a mutation could fire between the predicate check
		// and the receive, leaving us asleep against a stale channel.
		ch := r.changeCh
		// Capture a fresh snapshot under lock too, so the timeout
		// path can return the latest observed state without re-locking.
		snapAtCheck := r.snapshotLocked(t)
		r.mu.Unlock()

		remaining := time.Until(deadline)
		if remaining <= 0 {
			// Caller asked for a zero / past-deadline wait. Return
			// the snapshot we captured under lock; changed=false.
			return snapAtCheck, false
		}

		timer := time.NewTimer(remaining)
		select {
		case <-ch:
			// Mutation broadcast — re-evaluate the predicate on the
			// next loop iteration. Stop the timer to release its slot.
			if !timer.Stop() {
				<-timer.C
			}
			continue
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return Turn{}, false
		case <-timer.C:
			return snapAtCheck, false
		}
	}
}

// snapshotLocked returns a value-typed copy of t with its slice / pointer
// fields deep-copied so the caller cannot race the next Append /
// Complete. MUST be called with r.mu held — the field reads inside
// would otherwise race a peer mutation.
//
// Side effects:
//   - None — read-only on the input.
func (r *Registry) snapshotLocked(t *Turn) Turn {
	out := *t
	out.MessagesAdded = append([]session.Message(nil), t.MessagesAdded...)
	if t.CompletedAt != nil {
		ct := *t.CompletedAt
		out.CompletedAt = &ct
	}
	// Phase-5 §1c-β: deep-copy ContextUsage pointer + ProviderQuotas slice
	// so callers cannot race the next SetContextUsage / UpsertProviderQuota.
	// The variant payloads inside each ProviderQuotaSnapshot are pointer
	// fields — deepCopyProviderQuota walks them.
	if t.ContextUsage != nil {
		cu := *t.ContextUsage
		out.ContextUsage = &cu
	}
	if len(t.ProviderQuotas) > 0 {
		copies := make([]ProviderQuotaSnapshot, len(t.ProviderQuotas))
		for i, snap := range t.ProviderQuotas {
			copies[i] = deepCopyProviderQuota(snap)
		}
		out.ProviderQuotas = copies
	}
	return out
}

// Get returns a value-typed snapshot of the turn at the moment of
// the call. The MessagesAdded slice is copied so the caller cannot
// race the next Append. Returns ErrTurnNotFound when turnID is
// unknown.
//
// Expected:
//   - turnID is a turn_id previously returned by Start.
//
// Returns:
//   - A copy of the Turn. Zero value on error.
//   - ErrTurnNotFound when turnID is unknown.
//
// Side effects:
//   - None.
func (r *Registry) Get(turnID string) (Turn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.byID[turnID]
	if !ok {
		return Turn{}, ErrTurnNotFound
	}
	return r.snapshotLocked(t), nil
}
