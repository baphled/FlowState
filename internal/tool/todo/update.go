package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// UpdateTool implements the todo_update tool: a single-task patch operation
// over the stored todo list for a session. It is the companion to todowrite
// (the whole-list replace) and exists because models naturally batch many
// status flips into one todowrite call when there is no patch API. Live
// evidence: session 59b4e1a2-daf9-44f2-b179-fa0757c34f02 emitted 4 todowrite
// calls vs ~94 bash calls, so the per-status-transition signal that the
// instructions ask for never reaches the UI.
//
// The patch identifies the target by 0-based index in the stored list — the
// model already sees the indexed list returned from todowrite, so index is
// the lowest-ambiguity identifier available without introducing IDs.
// Patchable fields are status, content, and priority; all are optional, and
// at least one MUST be supplied.
type UpdateTool struct {
	store Store
}

// NewUpdate creates a new todo_update Tool backed by the given store.
//
// Expected:
//   - s is a non-nil Store implementation shared with the todowrite tool so
//     both tools mutate the same per-session list.
//
// Returns:
//   - A configured UpdateTool instance.
//
// Side effects:
//   - None.
func NewUpdate(s Store) *UpdateTool {
	return &UpdateTool{store: s}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "todo_update".
//
// Side effects:
//   - None.
func (t *UpdateTool) Name() string {
	return "todo_update"
}

// Description returns a human-readable description of the todo_update tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *UpdateTool) Description() string {
	return "Patch a single todo entry (status, content, or priority) by 0-based index. Use this for every per-task status transition; reserve `todowrite` for initial list creation only."
}

// Schema returns the input schema for the todo_update tool.
//
// Returns:
//   - A tool.Schema declaring the required index property and the optional
//     status, content, and priority patch fields.
//
// Side effects:
//   - None.
func (t *UpdateTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"index": {
				Type:        "integer",
				Description: "0-based index of the todo to patch within the stored list",
			},
			"status": {
				Type:        "string",
				Description: "New status for the todo: pending, in_progress, completed, or cancelled",
			},
			"content": {
				Type:        "string",
				Description: "Replacement content/description for the todo",
			},
			"priority": {
				Type:        "string",
				Description: "New priority for the todo: high, medium, or low",
			},
		},
		Required: []string{"index"},
	}
}

// Execute patches a single todo in the stored list and returns the full
// updated list as JSON so the UI surface (and the model) sees the same shape
// as a todowrite response.
//
// Expected:
//   - ctx contains a session.IDKey value identifying the current session.
//   - input.Arguments["index"] is a JSON number (decoded as float64) in
//     range [0, len(list)-1].
//   - At least one of status, content, or priority is supplied; supplying
//     none is a no-op and returns an error so the model gets a clear signal
//     it called the tool wrong.
//
// Returns:
//   - A tool.Result whose Output is the JSON-encoded patched list.
//   - An error when session ID is missing, the index is invalid, no patch
//     fields are supplied, or the store rejects the write.
//
// Side effects:
//   - Mutates the stored todo list for the session.
func (t *UpdateTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	sessionID, ok := ctx.Value(session.IDKey{}).(string)
	if !ok || sessionID == "" {
		return tool.Result{}, errors.New("session ID missing from context")
	}

	idx, err := parseIndex(input.Arguments)
	if err != nil {
		return tool.Result{}, err
	}

	patch, hasPatch := parsePatch(input.Arguments)
	if !hasPatch {
		return tool.Result{}, errors.New("todo_update requires at least one of status, content, or priority")
	}

	current := t.store.Get(sessionID)
	if idx < 0 || idx >= len(current) {
		return tool.Result{}, fmt.Errorf("index %d out of range: stored list has %d entries", idx, len(current))
	}

	updated := make([]Item, len(current))
	copy(updated, current)
	applyPatch(&updated[idx], patch)

	if err := t.store.Set(sessionID, updated); err != nil {
		return tool.Result{}, fmt.Errorf("storing patched todos: %w", err)
	}

	out, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return tool.Result{}, fmt.Errorf("serialising todos: %w", err)
	}
	return tool.Result{Output: string(out)}, nil
}

// itemPatch carries optional patch fields for a single todo. Empty strings
// mean "leave the existing value unchanged".
type itemPatch struct {
	status   string
	content  string
	priority string
}

// parseIndex extracts the index argument from a tool input map. JSON numbers
// decode as float64 in Go, so accept that as the canonical form; reject any
// other type with an explicit error.
func parseIndex(args map[string]interface{}) (int, error) {
	raw, present := args["index"]
	if !present {
		return 0, errors.New("index argument is required")
	}
	switch v := raw.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("index must be a number, got %T", raw)
	}
}

// parsePatch reads the optional status, content, and priority fields from a
// tool input map. Returns ok=true when at least one non-empty patch field is
// present so Execute can reject empty-patch calls.
func parsePatch(args map[string]interface{}) (itemPatch, bool) {
	p := itemPatch{
		status:   stringField(args, "status"),
		content:  stringField(args, "content"),
		priority: stringField(args, "priority"),
	}
	if p.status == "" && p.content == "" && p.priority == "" {
		return p, false
	}
	return p, true
}

// applyPatch mutates target in place with any non-empty patch fields. Empty
// patch fields preserve the existing values — the patch is additive.
func applyPatch(target *Item, p itemPatch) {
	if p.status != "" {
		target.Status = p.status
	}
	if p.content != "" {
		target.Content = p.content
	}
	if p.priority != "" {
		target.Priority = p.priority
	}
}
