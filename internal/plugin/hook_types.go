package plugin

import (
	"context"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// HookType identifies a plugin hook category.
type HookType string

// Hook type identifiers.
const (
	ChatParams      HookType = "chat.params"
	ContextAssembly HookType = "context.assembly"
	EventType       HookType = "event"
	ToolExecBefore  HookType = "tool.execute.before"
	ToolExecAfter   HookType = "tool.execute.after"
)

// Event represents a plugin event payload.
type Event interface {
	// Type returns the plugin event type identifier.
	Type() string
	// Timestamp returns when the event was emitted.
	Timestamp() time.Time
	// Data returns the event payload.
	Data() any
}

// ChatParamsHook mutates or validates chat request parameters.
type ChatParamsHook func(ctx context.Context, req *provider.ChatRequest) error

// EventHook handles emitted plugin events.
type EventHook func(ctx context.Context, event Event) error

// ToolExecHook handles tool execution lifecycle events.
type ToolExecHook func(ctx context.Context, toolName string, args map[string]any) error

// ContextAssemblyPayload carries context data for the context.assembly hook.
// It allows hooks to enrich the context window with observations before assembly.
type ContextAssemblyPayload struct {
	SessionID     string
	AgentID       string
	UserMessage   string
	TokenBudget   int
	SearchResults []recall.SearchResult
}

// ContextAssemblyHook fires before context window assembly.
// Hooks receive a mutable ContextAssemblyPayload and may populate SearchResults
// to enrich the context window before WindowBuilder.BuildContextResult is called.
type ContextAssemblyHook func(ctx context.Context, payload *ContextAssemblyPayload) error
