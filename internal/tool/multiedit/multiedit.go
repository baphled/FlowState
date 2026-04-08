package multiedit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// Tool applies multiple exact string replacements to a file.
type Tool struct{}

// New creates a new multiedit tool instance.
//
// Returns:
//   - A Tool configured for multiple exact replacements.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func New() *Tool { return &Tool{} }

// Name returns the tool identifier.
//
// Returns:
//   - The string "multiedit".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Name() string { return "multiedit" }

// Description returns a human-readable description of the multiedit tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Description() string { return "Apply multiple exact string replacements to one file" }

// Schema returns the input schema for the multiedit tool.
//
// Returns:
//   - A schema describing file_path and edits.
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
			"file_path": {Type: "string", Description: "File path to modify"},
			"edits": {
				Type:        "array",
				Description: "Ordered list of exact string replacements",
				Items: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"old_string": map[string]interface{}{"type": "string"},
						"new_string": map[string]interface{}{"type": "string"},
					},
					"required": []string{"old_string", "new_string"},
				},
			},
		},
		Required: []string{"file_path", "edits"},
	}
}

// Execute applies the configured edits to the target file.
//
// Expected:
//   - input contains a file_path and at least one edit entry.
//
// Returns:
//   - A tool.Result with the edit summary or an error.
//
// Side effects:
//   - Reads and writes one file on disk.
func (t *Tool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	filePath, ok := input.Arguments["file_path"].(string)
	if !ok || strings.TrimSpace(filePath) == "" {
		return tool.Result{}, errors.New("file_path argument is required")
	}

	rawEdits, ok := input.Arguments["edits"].([]any)
	if !ok || len(rawEdits) == 0 {
		return tool.Result{}, errors.New("edits argument is required")
	}

	rawPath := strings.TrimSpace(filePath)
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

	updated := string(data)
	for _, rawEdit := range rawEdits {
		edit, ok := rawEdit.(map[string]any)
		if !ok {
			return tool.Result{}, errors.New("each edit must be an object")
		}
		oldString, ok := edit["old_string"].(string)
		if !ok || oldString == "" {
			return tool.Result{}, errors.New("each edit must include old_string")
		}
		newString, ok := edit["new_string"].(string)
		if !ok {
			newString = ""
		}
		updated = strings.Replace(updated, oldString, newString, 1)
	}

	if err := root.WriteFile(rawPath, []byte(updated), 0o600); err != nil {
		return tool.Result{Error: fmt.Errorf("write failed: %w", err)}, err
	}

	return tool.Result{Output: fmt.Sprintf("applied %d edit(s) to %s", len(rawEdits), rawPath)}, nil
}
