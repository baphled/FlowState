package write

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/pathguard"
)

const (
	// maxContentBytes caps a single write payload at 100 KB. Larger content
	// is rejected with a structured error directing the model to split the
	// write into smaller chunks.
	maxContentBytes = 100 * 1024

	// toolTimeout gives the write tool its own execution budget so large
	// writes on slow filesystems do not hit the engine's shell-tool default
	// of 2 minutes.
	toolTimeout = 5 * time.Minute
)

// Tool implements file write operations with path validation.
type Tool struct {
	guard *pathguard.Guard
}

// New creates a new write tool instance.
func New() *Tool {
	return &Tool{}
}

// NewWithGuard creates a write tool that denies access to protected paths.
func NewWithGuard(g *pathguard.Guard) *Tool {
	return &Tool{guard: g}
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
//   - A string describing the tool's purpose and size limit.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return "Write content to files with path validation (max 100KB per call)"
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
				Description: "Content to write to the file (max 100KB). For content larger than 100KB, make multiple write calls — the tool will return a clear error directing you to split the content.",
			},
		},
		Required: []string{"path"},
	}
}

// Timeout returns the tool's own execution budget, overriding the engine
// default. Write operations are structurally different from shell tools:
// they may involve slow filesystems or large payloads that need more than
// 2 minutes to flush.
//
// Returns:
//   - 5 minutes, the dedicated budget for write tool execution.
//
// Side effects:
//   - None.
func (t *Tool) Timeout() time.Duration {
	return toolTimeout
}

// IsStateModifying returns true because write creates or overwrites a
// file on the filesystem.
func (t *Tool) IsStateModifying() bool { return true }

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
func (t *Tool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	path, ok := input.Arguments["path"].(string)
	if !ok || path == "" {
		return tool.Result{}, errors.New("path argument is required")
	}

	content, ok := input.Arguments["content"].(string)
	if !ok {
		content = ""
	}

	cleaned, resolveErr := pathguard.ResolvePath(path)
	if resolveErr != nil {
		return tool.Result{Error: resolveErr}, nil
	}

	if t.guard != nil {
		if err := t.guard.CheckForTool(ctx, "write", cleaned); err != nil {
			return tool.Result{Error: err}, nil
		}
	}

	if len(content) > maxContentBytes {
		approxChunks := (len(content) + maxContentBytes - 1) / maxContentBytes
		return tool.Result{
			IsError: true,
			Error: fmt.Errorf(
				"content too large: %d bytes (max %d). "+
					"Split this into approximately %d write calls of ~%d bytes each. "+
					"Example: write chunk 1, then write chunk 2, etc",
				len(content), maxContentBytes, approxChunks, maxContentBytes),
		}, nil
	}

	if err := os.MkdirAll(filepath.Dir(cleaned), 0o755); err != nil {
		return tool.Result{Error: fmt.Errorf("mkdir failed: %w", err)}, nil
	}

	if err := os.WriteFile(cleaned, []byte(content), 0o600); err != nil {
		return tool.Result{Error: fmt.Errorf("write failed: %w", err)}, nil
	}

	return tool.Result{Output: fmt.Sprintf("wrote %d bytes to %s", len(content), cleaned)}, nil
}
