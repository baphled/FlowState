package tool

import (
	"context"
	"encoding/json"
)

// EventSink appends ordered session events; implemented by the
// store's single-writer EventWriter, kept abstract so the tool
// domain does not import the store adapter directly.
type EventSink interface {
	// Append records one event; implementors serialize per session.
	Append(ctx context.Context, actor, typ string, payload any) (int64, error)
}

// Loop executes tool invocations for one session against a registry,
// enforcing the per-session allowlist and recording ordered
// tool.call / tool.result events through the session's EventWriter.
type Loop struct {
	registry  *Registry
	writer    EventSink
	allowlist map[string]bool
	gate      CallGate
	required  SkillRequirement
}

// SkillRequirement is the seam of ADR-013 D10: after the registry and allowlist checks and before any hook, the
// loop asks it whether the session may run a tool other than skill.load, which is exempt so the model can always
// meet the requirement. It is implemented by the app package, which owns the session log and the discovery.
type SkillRequirement interface {
	// Unmet returns the name of the first required skill the session has not loaded at its current hash, or ""
	// when every one is loaded.
	Unmet() string
}

// SkillRequiredCode is the tool.result error code of a call the skill requirement refused (ADR-013 D10). It is
// a per-call code: the terminal reason of a session that ends unmet is a later packet.
const SkillRequiredCode = "skill_required"

// SkillRequiredError is returned by Loop.Invoke for a call the skill requirement refused. The loop has already
// recorded the call and its skill_required result; Name is the unmet skill, and Error is what the model reads.
type SkillRequiredError struct {
	// Name is the required skill that was not loaded.
	Name string
}

// CallGate is the hook seam of ADR-012: the loop asks Before after the registry and allowlist checks and
// before it writes tool.call, and After once the tool has returned and its result is recorded. It is
// implemented by the app package, which owns the hook set, so the tool domain imports nothing of it.
type CallGate interface {
	// Before runs the tool.before hooks. A result with DeniedBy set denies the call; otherwise Args are the
	// final arguments the tool runs with, after every transform hook. An error is an infrastructure failure
	// (an event append), wrapped with ErrEventAppend, that must end the run.
	Before(ctx context.Context, name string, args json.RawMessage) (GateResult, error)
	// After runs the tool.after hooks with the arguments the tool ran with and its outcome. It observes: it
	// cannot change the outcome. An error is an infrastructure failure that must end the run.
	After(ctx context.Context, name string, args json.RawMessage, outcome CallOutcome) error
}

// GateResult is the outcome of the tool.before hooks of one call.
type GateResult struct {
	// DeniedBy is the id of the hook that denied the call, or "" when the call may run.
	DeniedBy string
	// Args are the final arguments after the transform phase; empty means the model's own arguments.
	Args json.RawMessage
}

// CallOutcome is what a tool returned, as a tool.after hook sees it.
type CallOutcome struct {
	// OK is true when the tool returned a result.
	OK bool
	// Result is the tool's result when OK.
	Result any
	// Code and Message are the recorded error of a failed call.
	Code    string
	Message string
}

// HookDeniedError is returned by Loop.Invoke for a call a tool.before hook denied. The loop has already
// recorded the call and its hook_denied result; HookID is the only thing the model is told.
type HookDeniedError struct {
	// HookID is the id of the denying hook.
	HookID string
}

// HookDeniedCode is the tool.result error code of a call a hook denied.
const HookDeniedCode = "hook_denied"
