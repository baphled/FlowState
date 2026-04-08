package ls

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// Tool implements directory listing operations.
type Tool struct{}

// New creates a new ls tool instance.
//
// Returns:
//   - A Tool configured for directory listing.
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
//   - The string "ls".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Name() string {
	return "ls"
}

// Description returns a human-readable description of the ls tool.
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
	return "List files and directories in a path"
}

// Schema returns the input schema for the ls tool.
//
// Returns:
//   - A schema describing path and pattern arguments.
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
			"path": {
				Type:        "string",
				Description: "Directory path to list",
			},
			"pattern": {
				Type:        "string",
				Description: "Optional glob pattern to filter entries",
			},
		},
		Required: []string{"path"},
	}
}

// Execute runs the ls tool.
//
// Expected:
//   - input contains a path argument and an optional pattern.
//
// Returns:
//   - A tool.Result containing directory entries or an error.
//
// Side effects:
//   - Reads the filesystem.
func (t *Tool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	rawPath, ok := input.Arguments["path"].(string)
	if !ok || strings.TrimSpace(rawPath) == "" {
		return tool.Result{}, errors.New("path argument is required")
	}

	cleanPath := filepath.Clean(rawPath)
	info, err := os.Stat(cleanPath)
	if err != nil {
		return tool.Result{Error: fmt.Errorf("list failed: %w", err)}, err
	}
	if !info.IsDir() {
		return tool.Result{Error: fmt.Errorf("path %q is not a directory", cleanPath)}, nil
	}

	pattern := optionalString(input.Arguments, "pattern")
	entries, err := os.ReadDir(cleanPath)
	if err != nil {
		return tool.Result{Error: fmt.Errorf("list failed: %w", err)}, err
	}

	items := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if pattern != "" {
			matched, matchErr := filepath.Match(pattern, name)
			if matchErr != nil {
				return tool.Result{Error: fmt.Errorf("invalid pattern: %w", matchErr)}, nil
			}
			if !matched {
				continue
			}
		}
		if entry.IsDir() {
			name += "/"
		}
		items = append(items, name)
	}

	sort.Strings(items)
	return tool.Result{Output: strings.Join(items, "\n")}, nil
}

// optionalString returns a string argument when present.
//
// Expected:
//   - args may contain key as a string value.
//
// Returns:
//   - The string value or an empty string.
//
// Side effects:
//   - None.
func optionalString(args map[string]any, key string) string {
	value, ok := args[key].(string)
	if !ok {
		return ""
	}
	return value
}
