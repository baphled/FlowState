package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// InsertTool implements the todo_insert tool: inserts a single item at a
// specified 0-based index in the stored todo list, shifting all subsequent
// items down. It is the companion to todo_append (end-of-list add) and
// todo_update (in-place patch) that handles the case where a new subtask
// belongs between two existing items rather than at the end.
type InsertTool struct {
	store Store
}

// NewInsert creates a new todo_insert Tool backed by the given store.
//
// Expected:
//   - s is a non-nil Store implementation shared with the other todo tools.
//
// Returns:
//   - A configured InsertTool instance.
//
// Side effects:
//   - None.
func NewInsert(s Store) *InsertTool {
	return &InsertTool{store: s}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "todo_insert".
//
// Side effects:
//   - None.
func (t *InsertTool) Name() string {
	return "todo_insert"
}

// Description returns a human-readable description of the todo_insert tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *InsertTool) Description() string {
	return "Insert a new todo item at a specific 0-based index, shifting subsequent items down. Use this when a new subtask belongs between existing items rather than at the end."
}

// Schema returns the input schema for the todo_insert tool.
//
// Returns:
//   - A tool.Schema declaring the required index and content properties
//     and optional status and priority fields.
//
// Side effects:
//   - None.
func (t *InsertTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"index": {
				Type:        "integer",
				Description: "0-based index where the new item should be inserted",
			},
			"content": {
				Type:        "string",
				Description: "Brief description of the task to insert",
			},
			"status": {
				Type:        "string",
				Description: "Initial status for the new item: pending, in_progress, completed, or cancelled. Defaults to pending.",
			},
			"priority": {
				Type:        "string",
				Description: "Priority level for the new item: high, medium, or low. Defaults to medium.",
			},
		},
		Required: []string{"index", "content"},
	}
}

// Execute inserts a single todo item at the specified index in the stored
// list and returns the full updated list as JSON.
//
// Expected:
//   - ctx contains a session.IDKey value identifying the current session.
//   - input.Arguments["index"] is a JSON number in range [0, len(list)].
//   - input.Arguments["content"] is a non-empty string.
//
// Returns:
//   - A tool.Result whose Output is the JSON-encoded updated list.
//   - An error when session ID is missing, index is invalid, content is
//     empty, or the store rejects the write.
//
// Side effects:
//   - Inserts one item into the stored todo list at the specified index.
func (t *InsertTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	sessionID, ok := ctx.Value(session.IDKey{}).(string)
	if !ok || sessionID == "" {
		return tool.Result{}, errors.New("session ID missing from context")
	}

	content := stringField(input.Arguments, "content")
	if content == "" {
		return tool.Result{}, errors.New("content is required and must be non-empty")
	}

	idx, err := parseIndex(input.Arguments)
	if err != nil {
		return tool.Result{}, err
	}

	status := stringField(input.Arguments, "status")
	if status == "" {
		status = "pending"
	}
	priority := stringField(input.Arguments, "priority")
	if priority == "" {
		priority = "medium"
	}

	newItem := Item{
		Content:  content,
		Status:   status,
		Priority: priority,
	}

	result, err := t.store.Apply(sessionID, func(current []Item) ([]Item, error) {
		if idx < 0 || idx > len(current) {
			return nil, fmt.Errorf("index %d out of range: stored list has %d entries (valid range 0-%d)", idx, len(current), len(current))
		}
		updated := make([]Item, 0, len(current)+1)
		updated = append(updated, current[:idx]...)
		updated = append(updated, newItem)
		updated = append(updated, current[idx:]...)
		return updated, nil
	})
	if err != nil {
		return tool.Result{}, err
	}

	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return tool.Result{}, fmt.Errorf("serialising todos: %w", err)
	}
	return tool.Result{Output: string(out)}, nil
}
