// Package permissionrequest provides the in-process registry that holds
// suspended permission requests while ModeAskUser is the active permission
// mode for a session and a pathguard / runtime-gate denial has fired.
//
// The registry mirrors internal/turn.Registry's locked-pre-check + insert
// pattern (§17.3 of the Permission Mode ModeAskUser Extension plan, May
// 2026): single sync.Mutex, dual maps (byID + byActiveSession), sentinel
// ErrPermissionRequestExists on duplicate request_id. Wait blocks until
// a grant or context cancellation arrives.
//
// Cross-package design notes:
//
//   - The registry is intentionally consumer-agnostic. Pathguard and the
//     engine runtime gate both consult the same registry via the
//     PermissionPrompter interface defined in their respective packages;
//     the registry itself knows nothing about either site.
//
//   - Wait returns ctx.Err() on cancellation, NOT a synthetic GrantDeny.
//     Callers MUST distinguish "operator denied" from "context cancelled
//     because the timeout fired or the request was withdrawn" — collapsing
//     them at the registry layer would lose the signal the engine pause-
//     semantics tests rely on (plan §5).
//
//   - Suspension lifetime must NOT be tied to the HTTP request context
//     (memory: project_flowstate_streamer_request_lifetime_coupling). The
//     PermissionPrompter implementation in app.go calls Wait with a
//     context.WithoutCancel-derived ctx so a tab-close does not cancel
//     the suspended goroutine before the operator can grant.
package permissionrequest

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Scope is the per-grant persistence and effect class chosen by the operator
// in the inline permission prompt. Values mirror the four scope buttons
// defined in the plan §2 "Grant scopes" table.
type Scope string

const (
	// ScopeOnce resumes the suspended tool call only. No persistence.
	ScopeOnce Scope = "once"
	// ScopeSession adds the resource to the per-session in-memory allow
	// set and resumes the call. Persists until the session ends.
	ScopeSession Scope = "session"
	// ScopeForever appends a matching allow glob to permissions.yaml AND
	// resumes the call. Slice 4 wires the YAML writer; Slice 2 treats
	// Forever identically to Session (in-memory only) so the wire shape
	// is stable while the writer lands.
	ScopeForever Scope = "forever"
	// ScopeDeny resumes the call with the original access-denied error
	// path. No persistence.
	ScopeDeny Scope = "deny"
)

// PermissionRequest captures the metadata published with
// EventPermissionRequired and re-read by the operator-grant resolver.
// RequestID is unique across the running daemon — collisions return
// ErrPermissionRequestExists at Register time.
type PermissionRequest struct {
	RequestID    string
	ToolName     string
	AgentName    string
	Resource     string
	DenialReason string
	Provider     string
	Model        string
	SessionID    string
	ChainID      string
	Mode         string
	CreatedAt    time.Time
}

// PermissionGrant is the operator's resolution of a suspended request.
// The Scope field is the single source of truth — Wait callers branch
// on it to apply the matching pathguard / engine effect.
type PermissionGrant struct {
	RequestID string
	Scope     Scope
}

// ErrPermissionRequestExists is returned by Register when the supplied
// RequestID is already present in the byID map. Defends against two
// goroutines racing on the same (tool, resource) pair — plan §15.3
// reviewer condition. Sentinel so callers can errors.Is-match it.
var ErrPermissionRequestExists = errors.New("permissionrequest: duplicate request_id")

// ErrPermissionRequestNotFound is returned by Resolve when the
// supplied RequestID is unknown. Distinct sentinel so a forgotten /
// late-arriving grant from a stale tab can be distinguished from
// duplicate-register collisions.
var ErrPermissionRequestNotFound = errors.New("permissionrequest: request not found")

// pendingRequest carries the persisted PermissionRequest alongside the
// channel Wait blocks on. The channel is buffered with capacity 1 so a
// Resolve that arrives BEFORE Wait does not deadlock on the send — the
// grant lands in the buffer and the subsequent Wait receives it immediately.
type pendingRequest struct {
	req     PermissionRequest
	grantCh chan PermissionGrant
}

// Registry is the in-process store of suspended permission requests.
// Construct via NewRegistry; the zero value is NOT usable (the internal
// maps must be initialised).
//
// Concurrency: all state lives under a single mutex. The grant channel
// is captured under lock then released before the Wait blocks, matching
// the turn.Registry change-broadcast pattern (turn.go:511-541, 623-648).
type Registry struct {
	mu              sync.Mutex
	byID            map[string]*pendingRequest
	byActiveSession map[string][]string // sessionID → []requestID (multi: §5 concurrent prompts in one turn)
}

// NewRegistry constructs a Registry with the internal maps initialised.
// No options today; the constructor exists so a future test seam (e.g.
// injectable clock) lands without breaking the call sites that already
// wire NewRegistry in app.go.
func NewRegistry() *Registry {
	return &Registry{
		byID:            make(map[string]*pendingRequest),
		byActiveSession: make(map[string][]string),
	}
}

// Register inserts a fresh permission request into the registry.
// Returns ErrPermissionRequestExists when req.RequestID is already
// present in byID — the duplicate guard plan §15.3 mandates.
//
// Side effects:
//   - Allocates a buffered grant channel of size 1.
//   - Records req under byID[req.RequestID] and appends RequestID to
//     byActiveSession[req.SessionID].
func (r *Registry) Register(req PermissionRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.byID[req.RequestID]; exists {
		return ErrPermissionRequestExists
	}

	r.byID[req.RequestID] = &pendingRequest{
		req:     req,
		grantCh: make(chan PermissionGrant, 1),
	}
	r.byActiveSession[req.SessionID] = append(r.byActiveSession[req.SessionID], req.RequestID)
	return nil
}

// Resolve delivers the operator's grant decision to the suspended Wait
// caller (if any) by sending into the buffered grant channel and
// removing the pending entry. Returns ErrPermissionRequestNotFound when
// requestID is unknown — a late grant from a stale tab is a silent
// non-issue on the wire surface but the registry must signal it
// distinctly for observability.
//
// Side effects:
//   - Sends grant into the request's grantCh (non-blocking; channel is
//     buffered with capacity 1 so the send never blocks).
//   - Removes the request from byID and byActiveSession[sessionID].
func (r *Registry) Resolve(requestID string, grant PermissionGrant) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	pending, exists := r.byID[requestID]
	if !exists {
		return ErrPermissionRequestNotFound
	}

	// Stamp the request_id on the grant for the receiver — the operator
	// HTTP handler accepts the bare scope but Wait callers want both
	// fields populated so downstream logging / metrics can correlate.
	grant.RequestID = requestID

	// Buffered send (capacity 1) never blocks the holder of r.mu — the
	// channel is freshly allocated on Register and only ever receives
	// one value. Defensive: a non-blocking select catches the unexpected
	// "channel full" case (double-Resolve) without deadlocking under the
	// mutex.
	select {
	case pending.grantCh <- grant:
	default:
	}

	delete(r.byID, requestID)
	r.removeFromSessionLocked(pending.req.SessionID, requestID)
	return nil
}

// Wait blocks until a grant arrives via Resolve, or until ctx is
// cancelled. On context cancellation Wait returns the zero PermissionGrant
// and ctx.Err() — NOT a synthetic ScopeDeny — so the caller can
// distinguish "operator denied" from "request was withdrawn / timed out".
//
// Wait returns ErrPermissionRequestNotFound when requestID is unknown
// to the registry — Register must precede Wait. Callers should always
// pair Wait with a context that carries the configured 5-minute
// suspension timeout (plan §3, §5).
//
// Concurrency: the grant channel reference is captured under the
// registry mutex then Wait blocks without holding the mutex. Resolve
// sends into the buffered channel (capacity 1) under its own mutex
// hold — the send never blocks regardless of whether Wait has reached
// the receive yet. The pathguard / engine call flow always orders
// Register → Wait → Resolve, so the channel ref is live for the
// duration of the wait.
//
// Side effects:
//   - Blocks the calling goroutine until grant arrival or ctx cancel.
func (r *Registry) Wait(ctx context.Context, requestID string) (PermissionGrant, error) {
	r.mu.Lock()
	pending, exists := r.byID[requestID]
	r.mu.Unlock()
	if !exists {
		return PermissionGrant{}, ErrPermissionRequestNotFound
	}

	select {
	case grant := <-pending.grantCh:
		return grant, nil
	case <-ctx.Done():
		// Best-effort cleanup so a cancelled Wait doesn't leak the
		// pending entry. Tolerate a concurrent Resolve that already
		// removed the entry.
		r.cancelPending(requestID)
		return PermissionGrant{}, ctx.Err()
	}
}

// PendingForSession returns a snapshot of request IDs currently
// suspended for sessionID. Order-stable in arrival order. Returns a
// fresh slice so callers cannot mutate the registry's internal list.
//
// Exposed primarily for the observability layer (R4 permission_pending
// gauge cross-check) and for the long-poll API in Slice 3. The slice may
// be empty when no requests are pending; never returns nil.
func (r *Registry) PendingForSession(sessionID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	ids := r.byActiveSession[sessionID]
	out := make([]string, len(ids))
	copy(out, ids)
	return out
}

// Lookup returns the registered PermissionRequest for requestID. The
// second return value is false when the request_id is unknown (already
// resolved, never registered, or evicted). The returned struct is a
// VALUE copy — callers cannot mutate registry state through it.
//
// Used by handlePermissionGrant in Slice 4 to discover the (tool,
// resource) pair before invoking the permissions.yaml writer: the
// "forever" scope needs to know which tool's allow list to append to,
// and what glob to write. The grant payload only carries (request_id,
// scope) so the registry is the lookup site.
//
// Concurrency: takes the registry mutex internally; callers MUST NOT
// hold r.mu.
func (r *Registry) Lookup(requestID string) (PermissionRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending, ok := r.byID[requestID]
	if !ok {
		return PermissionRequest{}, false
	}
	return pending.req, true
}

// PendingCount returns the total number of in-flight permission
// requests across all sessions. Used by the R4 gauge cross-check tests
// (and by future ops dashboards) without exposing the internal maps.
func (r *Registry) PendingCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byID)
}

// cancelPending removes a request from the registry maps. Tolerates a
// concurrent Resolve that already removed the entry (no-op on missing).
// Acquires the registry mutex internally; callers must NOT hold r.mu.
func (r *Registry) cancelPending(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending, exists := r.byID[requestID]
	if !exists {
		return
	}
	delete(r.byID, requestID)
	r.removeFromSessionLocked(pending.req.SessionID, requestID)
}

// removeFromSessionLocked drops requestID from byActiveSession[sessionID].
// MUST be called with r.mu held.
func (r *Registry) removeFromSessionLocked(sessionID, requestID string) {
	ids := r.byActiveSession[sessionID]
	out := ids[:0]
	for _, id := range ids {
		if id == requestID {
			continue
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		delete(r.byActiveSession, sessionID)
		return
	}
	r.byActiveSession[sessionID] = out
}
