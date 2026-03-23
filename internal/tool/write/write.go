package write

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// Tool implements file write operations with path validation.
type Tool struct{}

// New creates a new write tool instance.
//
// Returns:
//   - A configured write Tool instance.
//
// Side effects:
//   - None.
func New() *Tool {
	return &Tool{}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "write".
//
// Side effects:
//   - None.
func (t *Tool) Name() string {
	return "write"
}

// Description returns a human-readable description of the write tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return "Write content to files with path validation"
}

// Schema returns the JSON schema for the write tool inputs.
//
// Returns:
//   - A tool.Schema describing the path and content properties.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"path": {
				Type:        "string",
				Description: "File path to write to",
			},
			"content": {
				Type:        "string",
				Description: "Content to write to the file",
			},
		},
		Required: []string{"path"},
	}
}

// Execute performs the file write operation specified in input.
//
// Expected:
//   - input contains a "path" string argument and an optional "content" string argument.
//
// Returns:
//   - A tool.Result containing a success message with byte count.
//   - An error if the path argument is missing.
//
// Side effects:
//   - Creates parent directories and writes to the filesystem.
func (t *Tool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	path, ok := input.Arguments["path"].(string)
	if !ok || path == "" {
		return tool.Result{}, errors.New("path argument is required")
	}

	content, ok := input.Arguments["content"].(string)
	if !ok {
		content = ""
	}

	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return tool.Result{Error: errors.New("path traversal not allowed")}, nil
	}

	if err := os.MkdirAll(filepath.Dir(cleaned), 0o755); err != nil {
		return tool.Result{Error: fmt.Errorf("mkdir failed: %w", err)}, nil
	}

	if err := os.WriteFile(cleaned, []byte(content), 0o600); err != nil {
		return tool.Result{Error: fmt.Errorf("write failed: %w", err)}, nil
	}

	return tool.Result{Output: fmt.Sprintf("wrote %d bytes to %s", len(content), cleaned)}, nil
}
