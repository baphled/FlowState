package read

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/pathguard"
	"github.com/baphled/flowstate/internal/tool/truncate"
)

// Tool implements file read operations with path validation.
type Tool struct {
	guard *pathguard.Guard
}

// New creates a new read tool instance.
func New() *Tool {
	return &Tool{}
}

// NewWithGuard creates a read tool that denies access to protected paths.
func NewWithGuard(g *pathguard.Guard) *Tool {
	return &Tool{guard: g}
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
	return "Read file contents with optional 1-indexed line offset and line limit"
}

// Schema returns the JSON schema for the read tool inputs.
//
// Returns:
//   - A tool.Schema describing path, optional offset (1-indexed line),
//     and optional limit (max lines to return).
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
			"offset": {
				Type:        "integer",
				Description: "Optional 1-indexed line number to start reading from. Default: 1 (start of file).",
			},
			"limit": {
				Type:        "integer",
				Description: "Optional maximum number of lines to return. Default: unset (entire file from offset).",
			},
		},
		Required: []string{"path"},
	}
}

// Execute performs the file read operation specified in input.
//
// Expected:
//   - input contains a "path" string argument.
//   - input may include "offset" (1-indexed line number, default 1) and
//     "limit" (max lines, default unset = read to EOF).
//
// Returns:
//   - A tool.Result containing the file content (sliced when offset/limit
//     are provided), with a final truncation envelope applied.
//   - An error if the path argument is missing.
//
// Side effects:
//   - Reads from the filesystem.
//   - May write a session-scoped overflow file when output exceeds the
//     truncation budget.
func (t *Tool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	path, ok := input.Arguments["path"].(string)
	if !ok || path == "" {
		return tool.Result{}, errors.New("path argument is required")
	}

	offset, err := readIntArg(input.Arguments, "offset", 1)
	if err != nil {
		return tool.Result{}, err
	}
	if offset < 1 {
		return tool.Result{}, errors.New("offset must be >= 1")
	}

	limit, err := readIntArg(input.Arguments, "limit", 0)
	if err != nil {
		return tool.Result{}, err
	}
	if limit < 0 {
		return tool.Result{}, errors.New("limit must be >= 0")
	}

	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return tool.Result{Error: errors.New("path traversal not allowed")}, nil
	}

	if t.guard != nil {
		if err := t.guard.Check(cleaned); err != nil {
			return tool.Result{Error: err}, nil
		}
	}

	data, err := os.ReadFile(cleaned)
	if err != nil {
		return tool.Result{Error: fmt.Errorf("read failed: %w", err)}, nil
	}

	output := sliceLines(string(data), offset, limit)
	output = applyTruncation(ctx, output, "read")

	return tool.Result{Output: output}, nil
}

// sliceLines returns a 1-indexed [offset, offset+limit) slice of text.
// When offset is 1 and limit is 0, the original text is returned
// unchanged (preserving exact byte-for-byte content including a trailing
// newline). When offset > total line count, returns an empty string.
func sliceLines(text string, offset, limit int) string {
	if offset <= 1 && limit <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	startIdx := offset - 1
	if startIdx >= len(lines) {
		return ""
	}
	endIdx := len(lines)
	if limit > 0 {
		endIdx = startIdx + limit
		if endIdx > len(lines) {
			endIdx = len(lines)
		}
	}
	return strings.Join(lines[startIdx:endIdx], "\n")
}

// readIntArg coerces an integer-shaped argument from the validated input
// map. Both float64 (JSON-decoded) and int are accepted; missing or nil
// returns the supplied default.
func readIntArg(args map[string]interface{}, key string, def int) (int, error) {
	v, ok := args[key]
	if !ok || v == nil {
		return def, nil
	}
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		if n > math.MaxInt32 || n < math.MinInt32 {
			return 0, fmt.Errorf("%s out of range", key)
		}
		return int(n), nil
	case float64:
		if n != math.Trunc(n) {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return int(n), nil
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
}

// applyTruncation caps the output at the engine boundary using the
// truncate primitive. The session ID is read from ctx so the spill file
// lands in a session-scoped directory.
func applyTruncation(ctx context.Context, output, toolName string) string {
	sessionID, _ := ctx.Value(session.IDKey{}).(string)
	result := truncate.Apply(output, truncate.Options{
		SessionID: sessionID,
		ToolName:  toolName,
	})
	return result.Content
}
