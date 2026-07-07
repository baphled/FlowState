package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// AppendTool implements the todo_append tool: adds a single item to the end
// of the stored todo list for a session. It is the companion to todo_update
// (single-item patch) and todowrite (whole-list creation) that closes the
// gap where agents discover additional subtasks mid-work but cannot add
// them because todowrite is blocked once a list exists.
type AppendTool struct {
	store Store
}

// NewAppend creates a new todo_append Tool backed by the given store.
//
// Expected:
//   - s is a non-nil Store implementation shared with the other todo tools
//     so all tools mutate the same per-session list.
//
// Returns:
//   - A configured AppendTool instance.
//
// Side effects:
//   - None.
func NewAppend(s Store) *AppendTool {
	return &AppendTool{store: s}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "todo_append".
//
// Side effects:
//   - None.
func (t *AppendTool) Name() string {
	return "todo_append"
}

// Description returns a human-readable description of the todo_append tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *AppendTool) Description() string {
	return "Append a single todo item to the end of the current list. Use this when you discover an additional subtask mid-work and need to grow the list without replacing it."
}

// Schema returns the input schema for the todo_append tool.
//
// Returns:
//   - A tool.Schema declaring the required content property and optional
//     status and priority fields.
//
// Side effects:
//   - None.
func (t *AppendTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"content": {
				Type:        "string",
				Description: "Brief description of the task to append",
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
		Required: []string{"content"},
	}
}

// IsStateModifying returns true because todo_append appends an item to
// the stored todo list for the session.
func (t *AppendTool) IsStateModifying() bool { return true }

// Execute appends a single todo item to the stored list and returns the
// full updated list as JSON so the model sees the same shape as a
// todowrite or todo_update response.
//
// Expected:
//   - ctx contains a session.IDKey value identifying the current session.
//   - input.Arguments["content"] is a non-empty string.
//
// Returns:
//   - A tool.Result whose Output is the JSON-encoded updated list.
//   - An error when session ID is missing, content is empty, or the store
//     rejects the write.
//
// Side effects:
//   - Appends one item to the stored todo list for the session.
func (t *AppendTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	sessionID, ok := ctx.Value(session.IDKey{}).(string)
	if !ok || sessionID == "" {
		return tool.Result{}, errors.New("session ID missing from context")
	}

	content := stringField(input.Arguments, "content")
	if content == "" {
		return tool.Result{}, errors.New("content is required and must be non-empty")
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
		return append(current, newItem), nil
	})
	if err != nil {
		return tool.Result{}, fmt.Errorf("appending todo: %w", err)
	}

	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return tool.Result{}, fmt.Errorf("serialising todos: %w", err)
	}
	return tool.Result{Output: string(out)}, nil
}
