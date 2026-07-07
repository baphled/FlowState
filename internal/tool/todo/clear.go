package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// ClearTool implements the todo_clear tool, which replaces the stored todo
// list for a session with an empty slice. Once a session already has a list,
// todowrite is blocked, so todo_clear is the only way to retire a finished
// list and let a fresh todowrite create a new one. Together with the
// forward-only state machine enforced by todo_update, this prevents an agent
// from silently reverting completed work: a finished list is cleared wholesale
// rather than edited back.
type ClearTool struct {
	store Store
}

// NewClear creates a new todo_clear Tool backed by the given store.
//
// Expected:
//   - s is a non-nil Store implementation shared with the other todo tools
//     so clearing mutates the same per-session list the other tools read.
//
// Returns:
//   - A configured ClearTool instance.
//
// Side effects:
//   - None.
func NewClear(s Store) *ClearTool {
	return &ClearTool{store: s}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "todo_clear".
//
// Side effects:
//   - None.
func (t *ClearTool) Name() string {
	return "todo_clear"
}

// Description returns a human-readable description of the todo_clear tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *ClearTool) Description() string {
	return "Clear the session's todo list so a fresh `todowrite` can create a new one once the current list is finished."
}

// Schema returns the input schema for the todo_clear tool.
//
// Returns:
//   - A tool.Schema with no properties or required fields; the tool takes
//     no arguments and operates purely off the session in the context.
//
// Side effects:
//   - None.
func (t *ClearTool) Schema() tool.Schema {
	return tool.Schema{
		Type:       "object",
		Properties: map[string]tool.Property{},
	}
}

// IsStateModifying returns true because todo_clear replaces the stored
// todo list for the session with an empty slice.
func (t *ClearTool) IsStateModifying() bool { return true }

// Execute wipes the stored todo list for the session and returns an empty
// JSON array so the model observes the cleared state directly.
//
// Expected:
//   - ctx contains a session.IDKey value identifying the current session.
//
// Returns:
//   - A tool.Result whose Output is the JSON-encoded empty list ("[]").
//   - An error when the session ID is missing or the store rejects the write.
//
// Side effects:
//   - Replaces the stored todo list for the session with an empty slice,
//     unblocking subsequent todowrite calls.
func (t *ClearTool) Execute(ctx context.Context, _ tool.Input) (tool.Result, error) {
	sessionID, ok := ctx.Value(session.IDKey{}).(string)
	if !ok || sessionID == "" {
		return tool.Result{}, errors.New("session ID missing from context")
	}

	if err := t.store.Set(sessionID, []Item{}); err != nil {
		return tool.Result{}, fmt.Errorf("clearing todos: %w", err)
	}

	out, err := json.MarshalIndent([]Item{}, "", "  ")
	if err != nil {
		return tool.Result{}, fmt.Errorf("serialising todos: %w", err)
	}
	return tool.Result{Output: string(out)}, nil
}
