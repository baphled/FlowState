package read

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// Tool implements file read operations with path validation.
type Tool struct{}

// New creates a new read tool instance.
//
// Returns:
//   - A configured read Tool instance.
//
// Side effects:
//   - None.
func New() *Tool {
	return &Tool{}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "read".
//
// Side effects:
//   - None.
func (t *Tool) Name() string {
	return "read"
}

// Description returns a human-readable description of the read tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return "Read file contents with path validation"
}

// Schema returns the JSON schema for the read tool inputs.
//
// Returns:
//   - A tool.Schema describing the path property.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"path": {
				Type:        "string",
				Description: "File path to read",
			},
		},
		Required: []string{"path"},
	}
}

// Execute performs the file read operation specified in input.
//
// Expected:
//   - input contains a "path" string argument.
//
// Returns:
//   - A tool.Result containing the file content as a string.
//   - An error if the path argument is missing.
//
// Side effects:
//   - Reads from the filesystem.
func (t *Tool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	path, ok := input.Arguments["path"].(string)
	if !ok || path == "" {
		return tool.Result{}, errors.New("path argument is required")
	}

	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return tool.Result{Error: errors.New("path traversal not allowed")}, nil
	}

	data, err := os.ReadFile(cleaned)
	if err != nil {
		return tool.Result{Error: fmt.Errorf("read failed: %w", err)}, nil
	}

	return tool.Result{Output: string(data)}, nil
}
