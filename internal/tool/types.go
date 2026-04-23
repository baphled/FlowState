// Package tool provides interfaces and types for tool execution.
package tool

import (
	"context"
	"time"
)

// Permission represents the permission level for a tool.
type Permission int

// Permission constants for tool access control.
const (
	Allow Permission = iota
	Ask
	Deny
)

// Input represents input to a tool execution.
type Input struct {
	Name      string
	Arguments map[string]interface{}
}

// Result represents the result of a tool execution.
type Result struct {
	Output   string
	Error    error
	Title    string                 // Title is a human-readable label for UI display.
	Metadata map[string]interface{} // Metadata holds optional key-value data such as session ID and model name.
}

// Schema describes the schema of a tool's input parameters.
type Schema struct {
	Type       string
	Properties map[string]Property
	Required   []string
}

// Property describes a single property in a tool schema.
type Property struct {
	Type        string
	Description string
	Enum        []string
	// Items holds the JSON Schema definition for elements of an array-typed property.
	// When non-nil, it is propagated as the "items" key in the generated JSON Schema.
	Items map[string]interface{}
}

// PermissionRequest describes a tool invocation awaiting user approval.
type PermissionRequest struct {
	ToolName  string
	Arguments map[string]interface{}
}

// PermissionHandler is a callback invoked when a tool requires Ask permission.
// It returns true if the user approves the tool call, false to deny.
type PermissionHandler func(req PermissionRequest) (bool, error)

// Tool is the interface that all tools must implement.
type Tool interface {
	// Name returns the unique name of the tool.
	Name() string
	// Description returns a human-readable description of the tool.
	Description() string
	// Execute runs the tool with the given input.
	Execute(ctx context.Context, input Input) (Result, error)
	// Schema returns the input schema for the tool.
	Schema() Schema
}

// TimeoutOverrider is an optional interface tools may implement to override
// the engine's default per-tool execution timeout.
//
// Engines apply the following rule to a tool whose concrete type implements
// this interface:
//
//   - Timeout() > 0: the engine applies this duration as the per-tool
//     execution deadline instead of its configured default. The parent
//     context's own deadline still applies — whichever expires first wins.
//   - Timeout() == 0: the engine installs no deadline of its own and the
//     tool inherits the parent context unchanged. Parent cancellation
//     (Ctrl-C, chain teardown, etc.) still propagates.
//
// Intended for tools whose execution semantics do not fit a shell-style
// cap — e.g. the delegate tool runs a full multi-turn sub-agent
// conversation whose latency is provider/model-bounded, not shell-bounded.
// Short-running tools (bash, read, web, etc.) should not implement this
// and will keep using the engine default.
type TimeoutOverrider interface {
	// Timeout returns the per-tool execution budget the engine should
	// apply. See TimeoutOverrider doc for the semantics of the returned
	// value: >0 grants a dedicated budget, 0 inherits parent context.
	Timeout() time.Duration
}
