package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/permissionrequest"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/tool/pathguard"
	"github.com/baphled/flowstate/internal/tracer"
	"github.com/google/uuid"
)

// defaultPermissionTimeout is the default suspension window after which
// a permission request auto-Denies and the suspended goroutine
// resumes with the existing access-denied error path. Plan §3 + §5
// (Permission Mode ModeAskUser Extension, May 2026). Configurable via
// App construction options in a future slice; Slice 2 ships the
// constant.
const defaultPermissionTimeout = 5 * time.Minute

// permissionPrompter is the production implementation of both
// pathguard.PermissionPrompter and engine.EnginePermissionPrompter.
// Permission Mode ModeAskUser Extension plan (May 2026) Slice 2.
//
// Lifecycle on each RequestPermission call:
//  1. Mint a fresh request_id (UUID v4).
//  2. Register the request in the shared permissionrequest.Registry.
//  3. Increment the permission_pending gauge.
//  4. Publish EventPermissionRequired on the bus.
//  5. Detach the parent ctx via context.WithoutCancel (memory:
//     project_flowstate_streamer_request_lifetime_coupling) and apply
//     the 5-minute timeout.
//  6. Block on Registry.Wait until grant / cancel.
//  7. On timeout: publish EventPermissionTimeout, return GrantDeny.
//  8. On grant: return the matching pathguard/engine scope.
//
// The gauge's matching decrement lives on the bus subscriber in
// subscribePermissionGaugeHook, NOT here — the registry's HTTP-grant
// resolver path also fires the resolution event, so decrementing
// from the subscriber keeps a single source of truth.
type permissionPrompter struct {
	registry *permissionrequest.Registry
	bus      *eventbus.EventBus
	recorder tracer.Recorder
	timeout  time.Duration
}

// Registry returns the shared permissionrequest.Registry the prompter
// publishes into on each suspension. Exposed so the API server's
// permission-grant handler can resolve requests through the same
// instance the prompter waits on. Permission Mode ModeAskUser
// Extension plan (May 2026), Slice 3.
//
// Side effects:
//   - None — returns the field directly.
func (p *permissionPrompter) Registry() *permissionrequest.Registry {
	if p == nil {
		return nil
	}
	return p.registry
}

// newPermissionPrompter constructs a permissionPrompter wired to the
// shared registry, bus, and recorder. nil registry / bus is a wiring
// bug; recorder may be nil (the prompter falls back to a noop counter
// path). timeout <= 0 falls back to defaultPermissionTimeout.
func newPermissionPrompter(
	registry *permissionrequest.Registry,
	bus *eventbus.EventBus,
	recorder tracer.Recorder,
	timeout time.Duration,
) *permissionPrompter {
	if recorder == nil {
		recorder = &tracer.NoopRecorder{}
	}
	if timeout <= 0 {
		timeout = defaultPermissionTimeout
	}
	return &permissionPrompter{
		registry: registry,
		bus:      bus,
		recorder: recorder,
		timeout:  timeout,
	}
}

// RequestPermission implements pathguard.PermissionPrompter. Drives
// the path-denied seam — Resource is a filesystem path; AgentName is
// the per-turn agent override.
func (p *permissionPrompter) RequestPermission(ctx context.Context, req pathguard.PermissionRequest) pathguard.PermissionGrant {
	requestID := uuid.NewString()
	storedReq := permissionrequest.PermissionRequest{
		RequestID:    requestID,
		ToolName:     req.ToolName,
		AgentName:    req.AgentName,
		Resource:     req.Resource,
		DenialReason: req.DenialReason,
		SessionID:    req.SessionID,
		Mode:         req.Mode,
		CreatedAt:    time.Now(),
	}
	grant := p.suspendAndWait(ctx, storedReq)
	return pathguard.PermissionGrant{
		Scope: pathguardScope(grant.Scope),
	}
}

// RequestToolPermission implements engine.EnginePermissionPrompter.
// Drives the runtime-allowlist-gate seam — Resource is the rejected
// tool name; AgentName is the engine-resolved agent ID.
func (p *permissionPrompter) RequestToolPermission(ctx context.Context, req engine.EnginePermissionRequest) engine.EnginePermissionGrant {
	requestID := uuid.NewString()
	storedReq := permissionrequest.PermissionRequest{
		RequestID:    requestID,
		ToolName:     req.ToolName,
		AgentName:    req.AgentName,
		Resource:     req.Resource,
		DenialReason: req.DenialReason,
		SessionID:    req.SessionID,
		Mode:         req.Mode,
		CreatedAt:    time.Now(),
	}
	grant := p.suspendAndWait(ctx, storedReq)
	return engine.EnginePermissionGrant{
		Allowed: grant.Scope != permissionrequest.ScopeDeny,
		Scope:   string(grant.Scope),
	}
}

// suspendAndWait is the shared body of both prompter entry points.
// Returns the resolved permissionrequest.PermissionGrant — callers
// project it onto their seam's grant shape.
//
// Concurrency: the wait runs under context.WithoutCancel-detached
// ctx so a tab-close (which cancels the HTTP request ctx) does NOT
// cancel the suspended goroutine before the operator can answer
// (memory: project_flowstate_streamer_request_lifetime_coupling).
// The only termination paths are (a) Registry.Resolve from the
// operator-grant HTTP handler (Slice 3) and (b) the timeout timer.
func (p *permissionPrompter) suspendAndWait(parent context.Context, req permissionrequest.PermissionRequest) permissionrequest.PermissionGrant {
	if err := p.registry.Register(req); err != nil {
		// Duplicate request_id is exceedingly rare with UUID v4 but
		// fail closed if it happens — the model's tool call surfaces
		// the original denial rather than waiting on a phantom prompt.
		slog.Warn("permissionPrompter: registry Register failed",
			"request_id", req.RequestID,
			"error", err,
		)
		return permissionrequest.PermissionGrant{Scope: permissionrequest.ScopeDeny}
	}
	p.recorder.IncPermissionPending()
	p.publishRequired(req)

	// Detach from the parent ctx then apply the timeout. The Wait
	// returns ctx.Err() (not GrantDeny) on timeout so we can publish
	// EventPermissionTimeout distinctly from a manual deny.
	waitCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), p.timeout)
	defer cancel()
	grant, err := p.registry.Wait(waitCtx, req.RequestID)
	if err != nil {
		// Timeout or registry-not-found. Publish EventPermissionTimeout
		// for observability and surface a synthetic GrantDeny so the
		// caller's effect path treats it as "operator declined".
		slog.Info("permissionPrompter: suspension timed out",
			"request_id", req.RequestID,
			"tool", req.ToolName,
			"session_id", req.SessionID,
			"err", err,
		)
		p.publishTimeout(req)
		return permissionrequest.PermissionGrant{Scope: permissionrequest.ScopeDeny}
	}
	return grant
}

// publishRequired fires EventPermissionRequired with the registered
// request's metadata. Slice 3 will hook the SSE bridge to this event
// for the inline UI prompt; Slice 2 leaves the prompt rendering
// surface out of scope — the bus event is the wire-shape contract.
func (p *permissionPrompter) publishRequired(req permissionrequest.PermissionRequest) {
	if p.bus == nil {
		return
	}
	p.bus.Publish(events.EventPermissionRequired, events.NewPermissionRequiredEvent(events.PermissionRequiredEventData{
		RequestID:    req.RequestID,
		ToolName:     req.ToolName,
		AgentName:    req.AgentName,
		Resource:     req.Resource,
		DenialReason: req.DenialReason,
		SessionID:    req.SessionID,
		ChainID:      req.ChainID,
		Mode:         req.Mode,
	}))
}

// publishTimeout fires EventPermissionTimeout. The gauge subscriber
// in subscribePermissionGaugeHook decrements the permission_pending
// gauge on receipt so a timed-out request does not leave the gauge
// stuck above zero.
func (p *permissionPrompter) publishTimeout(req permissionrequest.PermissionRequest) {
	if p.bus == nil {
		return
	}
	p.bus.Publish(events.EventPermissionTimeout, events.NewPermissionTimeoutEvent(events.PermissionResolutionEventData{
		RequestID: req.RequestID,
		SessionID: req.SessionID,
		ToolName:  req.ToolName,
		AgentName: req.AgentName,
		Resource:  req.Resource,
		Mode:      req.Mode,
	}))
}

// pathguardScope projects a registry-layer Scope onto the pathguard-
// layer GrantScope enum. The two vocabularies are intentionally
// distinct so the registry stays consumer-agnostic, but the values
// align one-to-one.
func pathguardScope(s permissionrequest.Scope) pathguard.GrantScope {
	switch s {
	case permissionrequest.ScopeOnce:
		return pathguard.GrantOnce
	case permissionrequest.ScopeSession:
		return pathguard.GrantSession
	case permissionrequest.ScopeForever:
		return pathguard.GrantForever
	case permissionrequest.ScopeDeny:
		return pathguard.GrantDeny
	default:
		return pathguard.GrantDeny
	}
}

// subscribePermissionGaugeHook registers a subscriber on the
// permission-resolution events that decrements the permission_pending
// gauge. Permission Mode ModeAskUser Extension plan (May 2026) §11 R4:
// the gauge has a real subscriber, not a dead Publish.
//
// One subscriber covers all three terminal events because the gauge
// semantics are uniform — "one suspended request resolved" regardless
// of grant / deny / timeout. The fanout-to-three-handlers pattern
// (used by other observability sites in app.go) is not needed here:
// the recorder method is idempotent and side-effect-free.
//
// Memory: feedback_eventlogger_catalog_subscriber_is_dead_comment.
// The catalog claims subscribers for the three resolution events;
// this is the matching wire-up.
func subscribePermissionGaugeHook(bus *eventbus.EventBus, recorder tracer.Recorder) {
	if bus == nil || recorder == nil {
		return
	}
	handler := func(_ any) {
		recorder.DecPermissionPending()
	}
	bus.Subscribe(events.EventPermissionGranted, handler)
	bus.Subscribe(events.EventPermissionDenied, handler)
	bus.Subscribe(events.EventPermissionTimeout, handler)
}
