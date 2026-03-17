package tool

import (
	"context"
)

type Permission int

const (
	Allow Permission = iota
	Ask
	Deny
)

type ToolInput struct {
	Name      string
	Arguments map[string]interface{}
}

type ToolResult struct {
	Output string
	Error  error
}

type ToolSchema struct {
	Type       string
	Properties map[string]Property
	Required   []string
}

type Property struct {
	Type        string
	Description string
	Enum        []string
}

type Tool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, input ToolInput) (ToolResult, error)
	Schema() ToolSchema
}
