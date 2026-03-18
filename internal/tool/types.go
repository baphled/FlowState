// Package tool provides interfaces and types for tool execution.
package tool

import (
	"context"
)

// Permission represents the permission level for a tool.
type Permission int

// Permission constants for tool access control.
const (
	Allow Permission = iota
	Ask
	Deny
)

// ToolInput represents input to a tool execution.
type ToolInput struct {
	Name      string
	Arguments map[string]interface{}
}

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	Output string
	Error  error
}

// ToolSchema describes the schema of a tool's input parameters.
type ToolSchema struct {
	Type       string
	Properties map[string]Property
	Required   []string
}

// Property describes a single property in a tool schema.
type Property struct {
	Type        string
	Description string
	Enum        []string
}

// Tool is the interface that all tools must implement.
type Tool interface {
	// Name returns the unique name of the tool.
	Name() string
	// Description returns a human-readable description of the tool.
	Description() string
	// Execute runs the tool with the given input.
	Execute(ctx context.Context, input ToolInput) (ToolResult, error)
	// Schema returns the input schema for the tool.
	Schema() ToolSchema
}
