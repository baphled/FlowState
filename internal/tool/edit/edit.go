package edit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/pathguard"
)

const maxNewStringBytes = 50 * 1024 // 50 KB

// Tool implements exact string replacement in files.
type Tool struct {
	guard *pathguard.Guard
}

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

// NewWithGuard creates an edit tool that denies access to protected paths.
//
// The guard is consulted with the resolved path before the file is read
// or written, mirroring read.NewWithGuard and write.NewWithGuard so the
// agent surface remains consistent across mutating file tools.
//
// Expected: parameters for NewWithGuard.
// Returns: result of NewWithGuard.
// Side effects: None.
func NewWithGuard(g *pathguard.Guard) *Tool {
	return &Tool{guard: g}
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
				Description: "Replacement string (max 50KB). For larger replacements, use the write tool to rewrite the entire file.",
			},
		},
		Required: []string{"file", "old_string", "new_string"},
	}
}

// IsStateModifying returns true because edit modifies a file on the
// filesystem.
//
// Expected: parameters for IsStateModifying.
// Returns: result of IsStateModifying.
// Side effects: None.
func (t *Tool) IsStateModifying() bool { return true }

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
func (t *Tool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
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

	if len(newString) > maxNewStringBytes {
		return tool.Result{
			IsError: true,
			Error: fmt.Errorf(
				"new_string too large: %d bytes (max %d). "+
					"For large replacements, use the write tool to rewrite the entire file "+
					"or split the edit into smaller sections",
				len(newString), maxNewStringBytes),
		}, nil
	}

	cleaned, resolveErr := pathguard.ResolvePath(file)
	if resolveErr != nil {
		return tool.Result{Error: resolveErr}, nil
	}
	if t.guard != nil {
		if err := t.guard.CheckForTool(ctx, "edit", cleaned); err != nil {
			return tool.Result{Error: err}, nil
		}
	}

	data, readErr := os.ReadFile(cleaned)
	if readErr != nil {
		return tool.Result{Error: fmt.Errorf("read failed: %w", readErr)}, readErr
	}

	if !bytes.Contains(data, []byte(oldString)) {
		return tool.Result{Error: fmt.Errorf("string %q not found", oldString)}, nil
	}

	updated := bytes.Replace(data, []byte(oldString), []byte(newString), 1)
	if err := os.WriteFile(cleaned, updated, 0o600); err != nil {
		return tool.Result{Error: fmt.Errorf("write failed: %w", err)}, err
	}

	return tool.Result{Output: fmt.Sprintf("replaced %q with %q in %s", oldString, newString, cleaned)}, nil
}
