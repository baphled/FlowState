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
//
// Expected: parameters for Name.
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
//
// Expected: parameters for Description.
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
//
// Expected: parameters for Schema.
func (t *ClearTool) Schema() tool.Schema {
	return tool.Schema{
		Type:       "object",
		Properties: map[string]tool.Property{},
	}
}

// IsStateModifying returns true because todo_clear replaces the stored
// task list for the session with an empty slice.
//
// Expected: parameters for IsStateModifying.
// Returns: result of IsStateModifying.
// Side effects: None.
func (t *ClearTool) IsStateModifying() bool { return true }

// Execute wipes the stored todo list for the session and returns an empty
// JSON array so the model observes the cleared state directly.
//
// A completion guard runs before the write: every item in the current list
// must be in a terminal state (completed or cancelled). When any item is
// still pending or in_progress the call is rejected so an agent cannot
// discard unfinished work. An empty list passes the guard trivially.
//
// Expected:
//   - ctx contains a session.IDKey value identifying the current session.
//
// Returns:
//   - A tool.Result whose Output is the JSON-encoded empty list ("[]").
//   - An error when the session ID is missing, any item is not in a
//     terminal state, or the store rejects the write.
//
// Side effects:
//   - Replaces the stored todo list for the session with an empty slice,
//     unblocking subsequent todowrite calls.
func (t *ClearTool) Execute(ctx context.Context, _ tool.Input) (tool.Result, error) {
	sessionID, ok := ctx.Value(session.IDKey{}).(string)
	if !ok || sessionID == "" {
		return tool.Result{}, errors.New("session ID missing from context")
	}

	if err := assertAllTerminal(t.store.Get(sessionID)); err != nil {
		return tool.Result{}, err
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

// assertAllTerminal rejects a list that still contains non-terminal items.
// Every item must have status completed or cancelled; the first item that is
// still pending or in_progress produces an error naming it so the caller can
// surface which entry is blocking the clear. An empty list always passes.
//
// Expected:
//   - items is the current stored todo list for a session.
//
// Returns:
//   - nil when every item is in a terminal state or the list is empty.
//   - An error describing the first non-terminal item otherwise.
//
// Side effects:
//   - None.
func assertAllTerminal(items []Item) error {
	for i, it := range items {
		if it.Status != "completed" && it.Status != "cancelled" {
			return fmt.Errorf("cannot clear todo list: item %q (index %d) is still %q", it.Content, i, it.Status)
		}
	}
	return nil
}
