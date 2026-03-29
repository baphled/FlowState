package plugin

import (
	"context"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// HookType identifies a plugin hook category.
type HookType string

// Hook type identifiers.
const (
	ChatParams     HookType = "chat.params"
	EventType      HookType = "event"
	ToolExecBefore HookType = "tool.execute.before"
	ToolExecAfter  HookType = "tool.execute.after"
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
