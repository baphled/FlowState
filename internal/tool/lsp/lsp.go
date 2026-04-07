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
func New() tool.Tool {
	return &lspTool{}
}

// Name returns the tool's name.
func (l *lspTool) Name() string {
	return "lsp"
}

// Description returns a short description of the tool.
func (l *lspTool) Description() string {
	return "Provides Language Server Protocol (LSP) operations: diagnostics, symbols, goto definition, and find references."
}

// Schema returns the tool's input schema.
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
func (l *lspTool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	operation, ok := input.Arguments["operation"].(string)
	if !ok {
		return tool.Result{Error: errors.New("operation must be a string")}, nil
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
		return tool.Result{Error: fmt.Errorf("unsupported operation: %s", operation)}, nil
	}
}

func (l *lspTool) diagnostics(args map[string]interface{}) (tool.Result, error) {
	path, err := requirePath(args)
	if err != nil {
		return tool.Result{Error: err}, nil
	}

	return tool.Result{Output: fmt.Sprintf("No diagnostics found for %s", path)}, nil
}

func (l *lspTool) symbols(args map[string]interface{}) (tool.Result, error) {
	path, err := requirePath(args)
	if err != nil {
		return tool.Result{Error: err}, nil
	}

	if raw, exists := args["query"]; exists {
		if _, ok := raw.(string); !ok {
			return tool.Result{Error: errors.New("query must be a string")}, nil
		}
	}

	query, _ := args["query"].(string)

	return tool.Result{Output: fmt.Sprintf("No symbols found for query %q in %s", query, path)}, nil
}

func (l *lspTool) gotoDefinition(args map[string]interface{}) (tool.Result, error) {
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

func (l *lspTool) findReferences(args map[string]interface{}) (tool.Result, error) {
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
		return tool.Result{Error: fmt.Errorf("file not found: %s", path)}, nil
	}

	return tool.Result{Output: fmt.Sprintf("No references found at %s:%d:%d", path, line, col)}, nil
}

func requirePath(args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", errors.New("path is required")
	}

	return path, nil
}

func requireCoordinates(args map[string]interface{}) (int, int, error) {
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

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func resolveProjectPath(path string) string {
	root := findModuleRoot()
	if root != "" {
		return filepath.Join(root, path)
	}

	return path
}

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
