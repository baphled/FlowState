package lsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/tool"
)

const (
	operationDiagnostics    = "diagnostics"
	operationSymbols        = "symbols"
	operationGoto           = "goto"
	operationFindReferences = "find-references"
)

// lspTool implements the Tool interface for LSP operations.
type lspTool struct{}

// New returns a new instance of the LSP tool.
//
// Returns:
//   - A Tool that handles LSP-style requests.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func New() tool.Tool {
	return &lspTool{}
}

// Name returns the tool's name.
//
// Returns:
//   - The string "lsp".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (l *lspTool) Name() string {
	return "lsp"
}

// Description returns a short description of the tool.
//
// Returns:
//   - A summary of the available LSP operations.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (l *lspTool) Description() string {
	return "Provides Language Server Protocol (LSP) operations: diagnostics, symbols, goto definition, and find references."
}

// Schema returns the tool's input schema.
//
// Returns:
//   - A schema describing LSP operation arguments.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (l *lspTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"operation": {
				Type:        "string",
				Description: "The LSP operation to perform",
				Enum:        []string{operationDiagnostics, operationSymbols, operationGoto, operationFindReferences},
			},
			"path": {
				Type:        "string",
				Description: "File or directory path for the LSP request",
			},
			"line": {
				Type:        "integer",
				Description: "Line number for goto definition requests",
			},
			"column": {
				Type:        "integer",
				Description: "Column number for goto definition requests",
			},
			"query": {
				Type:        "string",
				Description: "Search query for symbol lookups",
			},
			"context": {
				Type:        "string",
				Description: "Workspace root or additional context for the request",
			},
		},
		Required: []string{"operation", "path"},
	}
}

// Execute runs the requested LSP operation after validating the input.
//
// Expected:
//   - input contains a supported operation and required path data.
//
// Returns:
//   - A tool.Result containing stub LSP output or an error.
//
// Side effects:
//   - Reads the filesystem when resolving referenced paths.
func (l *lspTool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	operation, ok := input.Arguments["operation"].(string)
	if !ok {
		operationErr := errors.New("operation must be a string")
		return tool.Result{Error: operationErr}, nil
	}

	switch operation {
	case operationDiagnostics:
		return l.diagnostics(input.Arguments)
	case operationSymbols:
		return l.symbols(input.Arguments)
	case operationGoto:
		return l.gotoDefinition(input.Arguments)
	case operationFindReferences:
		return l.findReferences(input.Arguments)
	default:
		operationErr := fmt.Errorf("unsupported operation: %s", operation)
		return tool.Result{Error: operationErr}, nil
	}
}

// diagnostics returns a stub diagnostics response.
//
// Expected:
//   - args contains a valid path argument.
//
// Returns:
//   - A stub diagnostics response or an error.
//
// Side effects:
//   - None.
func (l *lspTool) diagnostics(args map[string]any) (tool.Result, error) {
	path, err := requirePath(args)
	if err != nil {
		return tool.Result{Error: err}, nil
	}

	return tool.Result{Output: "No diagnostics found for " + path}, nil
}

// symbols returns a stub symbol lookup response.
//
// Expected:
//   - args contains a valid path and optional query.
//
// Returns:
//   - A stub symbols response or an error.
//
// Side effects:
//   - None.
func (l *lspTool) symbols(args map[string]any) (tool.Result, error) {
	path, err := requirePath(args)
	if err != nil {
		return tool.Result{Error: err}, nil
	}

	if raw, exists := args["query"]; exists {
		if _, ok := raw.(string); !ok {
			queryErr := errors.New("query must be a string")
			return tool.Result{Error: queryErr}, nil
		}
	}

	query := ""
	if rawQuery, ok := args["query"].(string); ok {
		query = rawQuery
	}

	return tool.Result{Output: fmt.Sprintf("No symbols found for query %q in %s", query, path)}, nil
}

// gotoDefinition returns a stub goto-definition response.
//
// Expected:
//   - args contains a valid path, line, and column.
//
// Returns:
//   - A stub goto-definition response or an error.
//
// Side effects:
//   - None.
func (l *lspTool) gotoDefinition(args map[string]any) (tool.Result, error) {
	path, err := requirePath(args)
	if err != nil {
		return tool.Result{Error: err}, nil
	}

	line, col, err := requireCoordinates(args)
	if err != nil {
		return tool.Result{Error: err}, nil
	}

	return tool.Result{Output: fmt.Sprintf("No definition found at %s:%d:%d", path, line, col)}, nil
}

// findReferences returns a stub find-references response.
//
// Expected:
//   - args contains a valid path, line, and column.
//
// Returns:
//   - A stub find-references response or an error.
//
// Side effects:
//   - Reads the filesystem to confirm the path exists.
func (l *lspTool) findReferences(args map[string]any) (tool.Result, error) {
	path, err := requirePath(args)
	if err != nil {
		return tool.Result{Error: err}, nil
	}

	line, col, err := requireCoordinates(args)
	if err != nil {
		return tool.Result{Error: err}, nil
	}

	fullPath := resolveProjectPath(path)
	if _, statErr := os.Stat(fullPath); statErr != nil {
		fileErr := fmt.Errorf("file not found: %s", path)
		return tool.Result{Error: fileErr}, nil
	}

	return tool.Result{Output: fmt.Sprintf("No references found at %s:%d:%d", path, line, col)}, nil
}

// requirePath validates and returns the path argument.
//
// Expected:
//   - args contains a path string.
//
// Returns:
//   - The path value or an error.
//
// Side effects:
//   - None.
func requirePath(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", errors.New("path is required")
	}

	return path, nil
}

// requireCoordinates validates and returns the line and column arguments.
//
// Expected:
//   - args contains line and column values.
//
// Returns:
//   - The line and column values or an error.
//
// Side effects:
//   - None.
func requireCoordinates(args map[string]any) (int, int, error) {
	line, ok := toInt(args["line"])
	if !ok {
		return 0, 0, errors.New("line and column are required")
	}

	col, ok := toInt(args["column"])
	if !ok {
		return 0, 0, errors.New("line and column are required")
	}

	return line, col, nil
}

// toInt converts supported numeric values into an int.
//
// Expected:
//   - v is a supported numeric value.
//
// Returns:
//   - The converted integer and true, or zero and false.
//
// Side effects:
//   - None.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// resolveProjectPath resolves a path relative to the module root when available.
//
// Expected:
//   - path is a relative or absolute filesystem path.
//
// Returns:
//   - A project-relative path when possible.
//
// Side effects:
//   - None.
func resolveProjectPath(path string) string {
	root := findModuleRoot()
	if root != "" {
		return filepath.Join(root, path)
	}

	return path
}

// findModuleRoot walks upward to locate the module root.
//
// Expected:
//   - The current process has a working directory.
//
// Returns:
//   - The module root path or an empty string.
//
// Side effects:
//   - Reads directory metadata.
func findModuleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}

		dir = parent
	}
}
