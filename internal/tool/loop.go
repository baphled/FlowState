// Package tool implements the M1-TOOLS tool execution loop: a
// provider-agnostic tool registry, a deny-by-default allowlist gate,
// and tool invocation eventing through the store's single event
// writer (tool.call / tool.result events per DATA-MODEL §2–3).
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/store"
)

// Event types emitted by the loop (DATA-MODEL §2 examples).
const (
	TypeToolCall   = "tool.call"
	TypeToolResult = "tool.result"
)

// ActorPrefix is the attribution prefix for tools: `tool:<name>`.
const ActorPrefix = "tool:"

// Loop executes tool invocations for one session against a registry,
// enforcing the per-session allowlist and recording ordered
// tool.call / tool.result events through the session's EventWriter.
type Loop struct {
	registry  *Registry
	writer    *store.EventWriter
	allowlist map[string]bool
}

// NewLoop builds the tool loop for one session. The allowlist is
// manifest-driven per CLI-AND-API-CONTRACT §4; an empty or nil
// allowlist denies every tool (deny-by-default).
func NewLoop(registry *Registry, writer *store.EventWriter, allowlist []string) (*Loop, error) {
	if registry == nil {
		return nil, errors.New("tool: registry must not be nil")
	}
	if writer == nil {
		return nil, errors.New("tool: event writer must not be nil")
	}
	l := &Loop{registry: registry, writer: writer, allowlist: make(map[string]bool, len(allowlist))}
	for _, name := range allowlist {
		l.allowlist[name] = true
	}
	return l, nil
}

// Allowed reports whether name is on the session allowlist.
func (l *Loop) Allowed(name string) bool {
	return l.allowlist[name]
}

// Invoke runs the named tool after the allowlist gate, recording a
// tool.call event before execution and a tool.result event after
// (success or failure), both under actor `tool:<name>`.
//
// Outcomes:
//   - unknown tool (not in registry): ErrUnknownTool, no events
//   - not allowlisted: ErrDenied plus a tool.result error event with
//     error.code=tool_denied
//   - allowlisted, tool returns error: the error, plus tool.result
//     error event with error.code=tool_error
//   - success: the tool's result, plus tool.result ok event
//
// An event-append failure is wrapped and returned with the original
// outcome where possible; the store's typed errors are preserved.
func (l *Loop) Invoke(ctx context.Context, name string, args json.RawMessage) (any, error) {
	if name == "" {
		return nil, ErrEmptyName
	}
	actor := ActorPrefix + name
	// A nil/empty RawMessage is not valid JSON under encoding/json v2;
	// normalise to null so the tool.call payload stays canonical.
	if len(args) == 0 {
		args = json.RawMessage("null")
	}
	t, err := l.registry.Get(name)
	if err != nil {
		return nil, err
	}
	if !l.Allowed(name) {
		return nil, l.recordDeny(ctx, actor, name)
	}
	if _, err := l.writer.Append(ctx, actor, TypeToolCall, map[string]any{
		"tool": name,
		"args": args,
	}); err != nil {
		return nil, fmt.Errorf("tool: append tool.call: %w", err)
	}
	result, invokeErr := t.Invoke(ctx, args)
	if invokeErr != nil {
		if _, aerr := l.writer.Append(ctx, actor, TypeToolResult, map[string]any{
			"tool": name,
			"ok":   false,
			"error": map[string]any{
				"code":    "tool_error",
				"message": invokeErr.Error(),
			},
		}); aerr != nil {
			return nil, errors.Join(fmt.Errorf("tool: append tool.result: %w", aerr), invokeErr)
		}
		return nil, fmt.Errorf("%w: %s: %w", ErrToolExecuted, name, invokeErr)
	}
	if _, aerr := l.writer.Append(ctx, actor, TypeToolResult, map[string]any{
		"tool":   name,
		"ok":     true,
		"result": result,
	}); aerr != nil {
		return nil, fmt.Errorf("tool: append tool.result: %w", aerr)
	}
	return result, nil
}

// ErrToolExecuted wraps errors returned by an allowlisted tool's own
// implementation, distinguishing them from gate failures.
var ErrToolExecuted = errors.New("tool: tool invocation failed")

// recordDeny persists the typed denial event for a non-allowlisted
// call (M1-TOOLS: denied calls produce typed error events) and
// returns ErrDenied (or the append failure).
func (l *Loop) recordDeny(ctx context.Context, actor, name string) error {
	_, aerr := l.writer.Append(ctx, actor, TypeToolResult, map[string]any{
		"tool": name,
		"ok":   false,
		"error": map[string]any{
			"code":    "tool_denied",
			"message": fmt.Sprintf("tool %q is not on the session allowlist", name),
		},
	})
	if aerr != nil {
		return fmt.Errorf("tool: append deny event: %w", aerr)
	}
	return fmt.Errorf("%w: %s", ErrDenied, name)
}
