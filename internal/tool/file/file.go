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
//
// Returns:
//   - A configured file Tool instance.
//
// Side effects:
//   - None.
func New() *Tool {
	return &Tool{}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "file".
//
// Side effects:
//   - None.
func (t *Tool) Name() string {
	return "file"
}

// Description returns a human-readable description of the file tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return "Read or write files with path validation"
}

// Schema returns the JSON schema for the file tool inputs.
//
// Returns:
//   - A tool.Schema describing the operation, path, and content properties.
//
// Side effects:
//   - None.
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
//
// Expected:
//   - input contains "operation" (read or write) and "path" string arguments.
//
// Returns:
//   - A tool.Result containing the read content or write confirmation.
//   - An error if required arguments are missing or the operation is unknown.
//
// Side effects:
//   - Reads from or writes to the filesystem depending on the operation.
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

// executeRead reads a file from the filesystem and returns its content.
//
// Expected:
//   - path is a validated file path.
//
// Returns:
//   - A tool.Result containing the file content as a string.
//   - An error wrapped in the Result if the read fails.
//
// Side effects:
//   - Reads from the filesystem.
func (t *Tool) executeRead(path string) (tool.Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return tool.Result{Error: fmt.Errorf("read failed: %w", err)}, nil
	}
	return tool.Result{Output: string(data)}, nil
}

// executeWrite writes content to a file, creating parent directories as needed.
//
// Expected:
//   - path is a validated file path.
//   - content is the string to write.
//
// Returns:
//   - A tool.Result containing a success message with byte count.
//   - An error wrapped in the Result if the write fails.
//
// Side effects:
//   - Creates parent directories and writes to the filesystem.
func (t *Tool) executeWrite(path, content string) (tool.Result, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return tool.Result{Error: fmt.Errorf("mkdir failed: %w", err)}, nil
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return tool.Result{Error: fmt.Errorf("write failed: %w", err)}, nil
	}
	return tool.Result{Output: fmt.Sprintf("wrote %d bytes to %s", len(content), path)}, nil
}
