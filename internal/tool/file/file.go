// Package file provides a file system tool for reading and writing files.
package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// Tool implements file read/write operations with path validation.
type Tool struct{}

// New creates a new file tool instance.
func New() *Tool {
	return &Tool{}
}

// Name returns the tool identifier.
func (t *Tool) Name() string {
	return "file"
}

// Description returns a human-readable description.
func (t *Tool) Description() string {
	return "Read or write files with path validation"
}

// Schema returns the JSON schema for tool inputs.
func (t *Tool) Schema() tool.ToolSchema {
	return tool.ToolSchema{
		Type: "object",
		Properties: map[string]tool.Property{
			"operation": {
				Type:        "string",
				Description: "Operation to perform: read or write",
				Enum:        []string{"read", "write"},
			},
			"path": {
				Type:        "string",
				Description: "File path to read from or write to",
			},
			"content": {
				Type:        "string",
				Description: "Content to write (required for write operation)",
			},
		},
		Required: []string{"operation", "path"},
	}
}

// Execute performs the file operation specified in input.
func (t *Tool) Execute(_ context.Context, input tool.ToolInput) (tool.ToolResult, error) {
	operation, ok := input.Arguments["operation"].(string)
	if !ok || operation == "" {
		return tool.ToolResult{}, errors.New("operation argument is required")
	}

	path, ok := input.Arguments["path"].(string)
	if !ok || path == "" {
		return tool.ToolResult{}, errors.New("path argument is required")
	}

	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return tool.ToolResult{Error: errors.New("path traversal not allowed")}, nil
	}

	switch operation {
	case "read":
		return t.executeRead(cleaned)
	case "write":
		content, ok := input.Arguments["content"].(string)
		if !ok {
			content = ""
		}
		return t.executeWrite(cleaned, content)
	default:
		return tool.ToolResult{}, fmt.Errorf("unknown operation: %s", operation)
	}
}

func (t *Tool) executeRead(path string) (tool.ToolResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return tool.ToolResult{Error: fmt.Errorf("read failed: %w", err)}, nil
	}
	return tool.ToolResult{Output: string(data)}, nil
}

func (t *Tool) executeWrite(path, content string) (tool.ToolResult, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return tool.ToolResult{Error: fmt.Errorf("mkdir failed: %w", err)}, nil
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return tool.ToolResult{Error: fmt.Errorf("write failed: %w", err)}, nil
	}
	return tool.ToolResult{Output: fmt.Sprintf("wrote %d bytes to %s", len(content), path)}, nil
}
