package edit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// Tool implements exact string replacement in files.
type Tool struct{}

// New creates a new edit tool instance.
//
// Returns:
//   - A Tool configured for exact string replacement.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func New() *Tool {
	return &Tool{}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "edit".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Name() string {
	return "edit"
}

// Description returns a human-readable description of the edit tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return "Replace exact strings in files"
}

// Schema returns the input schema for the edit tool.
//
// Returns:
//   - A schema describing file, old_string, and new_string arguments.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"file": {
				Type:        "string",
				Description: "File path to edit",
			},
			"old_string": {
				Type:        "string",
				Description: "Exact string to replace",
			},
			"new_string": {
				Type:        "string",
				Description: "Replacement string",
			},
		},
		Required: []string{"file", "old_string", "new_string"},
	}
}

// Execute performs the edit operation.
//
// Expected:
//   - input contains file, old_string, and new_string arguments.
//
// Returns:
//   - A tool.Result with the replacement summary or an error.
//
// Side effects:
//   - Reads and writes a file on disk.
func (t *Tool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	file, ok := input.Arguments["file"].(string)
	if !ok || file == "" {
		return tool.Result{}, errors.New("file argument is required")
	}

	oldString, ok := input.Arguments["old_string"].(string)
	if !ok || oldString == "" {
		return tool.Result{}, errors.New("old_string argument is required")
	}

	newString, ok := input.Arguments["new_string"].(string)
	if !ok {
		return tool.Result{}, errors.New("new_string argument is required")
	}

	rawPath := strings.TrimSpace(file)
	if !filepath.IsLocal(rawPath) {
		return tool.Result{Error: errors.New("path traversal not allowed")}, nil
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		return tool.Result{Error: fmt.Errorf("open root failed: %w", err)}, err
	}
	defer root.Close()

	data, err := root.ReadFile(rawPath)
	if err != nil {
		return tool.Result{Error: fmt.Errorf("read failed: %w", err)}, err
	}

	if !bytes.Contains(data, []byte(oldString)) {
		return tool.Result{Error: fmt.Errorf("string %q not found", oldString)}, nil
	}

	updated := bytes.Replace(data, []byte(oldString), []byte(newString), 1)
	if err := root.WriteFile(rawPath, updated, 0o600); err != nil {
		return tool.Result{Error: fmt.Errorf("write failed: %w", err)}, err
	}

	return tool.Result{Output: fmt.Sprintf("replaced %q with %q in %s", oldString, newString, rawPath)}, nil
}
