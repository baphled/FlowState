package engine

import "context"

// EnginePermissionPrompter is the seam the engine's runtime allowlist
// gate uses to escalate a denied tool dispatch to the operator when
// the session is in ModeAskUser. Permission Mode ModeAskUser Extension
// plan (May 2026) Slice 2.
//
// Contract:
//
//   - ctx is the dispatch ctx (carries session.IDKey, the active
//     permission mode via permissionmode.WithMode, and the per-turn
//     agent override). Implementations MUST detach the suspension
//     ctx from the parent via context.WithoutCancel so a tab-close
//     does not cancel the wait before the operator can grant
//     (memory: project_flowstate_streamer_request_lifetime_coupling).
//
//   - req carries the diagnostic metadata for the bus event payload:
//     ToolName, AgentName, Resource (the rejected tool's name —
//     the resource is the tool itself, not a path), DenialReason,
//     SessionID, Mode.
//
//   - Return value drives the engine effect. EnginePermissionGrant.Allowed
//     controls whether the suspended call resumes (true) or surfaces
//     the existing "not available to agent" IsError tool_result (false).
//     Allowed=true does NOT mean "tool added to manifest" — the grant
//     is per-call (per-session for ScopeSession). The next out-of-set
//     call re-prompts unless the in-process allow set retains the
//     grant for the rest of the session.
type EnginePermissionPrompter interface {
	RequestToolPermission(ctx context.Context, req EnginePermissionRequest) EnginePermissionGrant
}

// EnginePermissionRequest is the payload handed to the prompter by
// the runtime allowlist gate. Distinct from pathguard.PermissionRequest
// to keep the two seams independently testable — the engine uses
// "tool not available to agent" semantics; pathguard uses "path
// denied" semantics. Both produce EventPermissionRequired on the bus
// via the prompter's implementation.
type EnginePermissionRequest struct {
	ToolName     string
	AgentName    string
	Resource     string
	DenialReason string
	SessionID    string
	Mode         string
}

// EnginePermissionGrant is the prompter's return shape. Allowed
// determines the gate effect; Scope is informational (logged by the
// caller) and mirrors permissionrequest.Scope's "once" / "session" /
// "forever" / "deny" vocabulary.
type EnginePermissionGrant struct {
	Allowed bool
	Scope   string
}
