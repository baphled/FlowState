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
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
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
func (t *Tool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	operation, ok := input.Arguments["operation"].(string)
	if !ok || operation == "" {
		return tool.Result{}, errors.New("operation argument is required")
	}

	path, ok := input.Arguments["path"].(string)
	if !ok || path == "" {
		return tool.Result{}, errors.New("path argument is required")
	}

	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return tool.Result{Error: errors.New("path traversal not allowed")}, nil
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
		return tool.Result{}, fmt.Errorf("unknown operation: %s", operation)
	}
}

func (t *Tool) executeRead(path string) (tool.Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return tool.Result{Error: fmt.Errorf("read failed: %w", err)}, nil
	}
	return tool.Result{Output: string(data)}, nil
}

func (t *Tool) executeWrite(path, content string) (tool.Result, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return tool.Result{Error: fmt.Errorf("mkdir failed: %w", err)}, nil
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return tool.Result{Error: fmt.Errorf("write failed: %w", err)}, nil
	}
	return tool.Result{Output: fmt.Sprintf("wrote %d bytes to %s", len(content), path)}, nil
}
