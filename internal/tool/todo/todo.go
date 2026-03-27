package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// Tool implements the todowrite tool, managing per-session todo lists.
type Tool struct {
	store Store
}

// New creates a new todowrite Tool backed by the given store.
//
// Expected:
//   - s is a non-nil Store implementation.
//
// Returns:
//   - A configured Tool instance.
//
// Side effects:
//   - None.
func New(s Store) *Tool {
	return &Tool{store: s}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "todowrite".
//
// Side effects:
//   - None.
func (t *Tool) Name() string {
	return "todowrite"
}

// Description returns a human-readable description of the todowrite tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return "Create and manage a structured task list for tracking progress on multi-step work"
}

// Schema returns the input schema for the todowrite tool.
//
// Returns:
//   - A tool.Schema describing the required todos array property.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"todos": {
				Type:        "array",
				Description: "The updated todo list",
			},
		},
		Required: []string{"todos"},
	}
}

// Execute stores the provided todo list for the current session and returns it as JSON.
//
// Expected:
//   - ctx contains a session.IDKey value identifying the current session.
//   - input contains a "todos" argument with an array of todo objects.
//
// Returns:
//   - A tool.Result containing the serialised todo list on success.
//   - An error when the session ID is missing or the todos argument is invalid.
//
// Side effects:
//   - Replaces the stored todo list for the session in the backing Store.
func (t *Tool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	sessionID, ok := ctx.Value(session.IDKey{}).(string)
	if !ok || sessionID == "" {
		return tool.Result{}, errors.New("session ID missing from context")
	}

	rawTodos, ok := input.Arguments["todos"].([]interface{})
	if !ok {
		return tool.Result{}, errors.New("todos argument is required and must be an array")
	}

	todos, err := parseTodos(rawTodos)
	if err != nil {
		return tool.Result{}, fmt.Errorf("parsing todos: %w", err)
	}

	if err := t.store.Set(sessionID, todos); err != nil {
		return tool.Result{}, fmt.Errorf("storing todos: %w", err)
	}

	out, err := json.MarshalIndent(todos, "", "  ")
	if err != nil {
		return tool.Result{}, fmt.Errorf("serialising todos: %w", err)
	}

	return tool.Result{Output: string(out)}, nil
}

// parseTodos converts a raw JSON-decoded slice into a slice of Item values.
//
// Expected:
//   - raw contains interface{} elements that are each map[string]interface{}.
//
// Returns:
//   - A slice of Item values extracted from each element.
//   - An error when an element cannot be cast to map[string]interface{}.
//
// Side effects:
//   - None.
func parseTodos(raw []interface{}) ([]Item, error) {
	todos := make([]Item, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			return nil, errors.New("each todo must be an object")
		}
		todos = append(todos, Item{
			Content:  stringField(m, "content"),
			Status:   stringField(m, "status"),
			Priority: stringField(m, "priority"),
		})
	}
	return todos, nil
}

// stringField extracts a string value from a map by key, returning an empty string when absent.
//
// Expected:
//   - m is a non-nil map with string keys.
//   - key identifies the field to extract.
//
// Returns:
//   - The string value associated with key, or an empty string when absent or of a different type.
//
// Side effects:
//   - None.
func stringField(m map[string]interface{}, key string) string {
	v, ok := m[key].(string)
	if !ok {
		return ""
	}
	return v
}
