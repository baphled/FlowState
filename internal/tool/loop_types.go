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
}

// CallGate is the tool.before hook point of ADR-012: the loop asks it after the registry and
// allowlist checks and before it writes tool.call. It is implemented by the app package, which owns the
// hook set, so the tool domain imports nothing of it.
type CallGate interface {
	// Before returns the id of the hook that denied the call, or "" when the call may run. An error is an
	// infrastructure failure (an event append), wrapped with ErrEventAppend, that must end the run.
	Before(ctx context.Context, name string, args json.RawMessage) (denied string, err error)
}

// HookDeniedError is returned by Loop.Invoke for a call a tool.before hook denied. The loop has already
// recorded the call and its hook_denied result; HookID is the only thing the model is told.
type HookDeniedError struct {
	// HookID is the id of the denying hook.
	HookID string
}

// HookDeniedCode is the tool.result error code of a call a hook denied.
const HookDeniedCode = "hook_denied"
