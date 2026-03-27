package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/tool"
)

// BackgroundCancelTool enables cancellation of background tasks by task ID or all at once.
type BackgroundCancelTool struct {
	manager *BackgroundTaskManager
}

// NewBackgroundCancelTool creates a new background cancel tool.
//
// Expected:
//   - manager is a non-nil BackgroundTaskManager instance.
//
// Returns:
//   - A ready-to-use BackgroundCancelTool instance.
//
// Side effects:
//   - None.
func NewBackgroundCancelTool(manager *BackgroundTaskManager) *BackgroundCancelTool {
	return &BackgroundCancelTool{manager: manager}
}

// Name returns the tool name.
//
// Returns:
//   - The string "background_cancel".
//
// Side effects:
//   - None.
func (b *BackgroundCancelTool) Name() string {
	return "background_cancel"
}

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (b *BackgroundCancelTool) Description() string {
	return "Cancel a background task by ID or cancel all running tasks"
}

// Schema returns the JSON schema for the background cancel tool input.
//
// Returns:
//   - A tool.Schema describing optional parameters.
//
// Side effects:
//   - None.
func (b *BackgroundCancelTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"task_id": {
				Type:        "string",
				Description: "The unique identifier of the background task to cancel",
			},
			"all": {
				Type:        "boolean",
				Description: "If true, cancel all running and pending tasks; default is false",
			},
		},
		Required: []string{},
	}
}

// Execute cancels a background task or all tasks.
//
// Expected:
//   - ctx is a valid context (unused but required by tool.Tool interface).
//   - input contains either "task_id" string or "all" boolean argument.
//   - At least one of "task_id" or "all" must be provided.
//
// Returns:
//   - A tool.Result containing a list of cancelled task IDs as JSON.
//   - An error if neither task_id nor all are provided, or if task cancellation fails.
//
// Side effects:
//   - Calls the context cancel function for the specified task(s).
func (b *BackgroundCancelTool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	taskID, hasTaskID := input.Arguments["task_id"].(string)
	all := false
	if raw, ok := input.Arguments["all"].(bool); ok {
		all = raw
	}

	if !hasTaskID && !all {
		return tool.Result{}, errors.New("must provide either task_id or all=true")
	}

	var cancelledIDs []string
	var err error

	if all {
		cancelledIDs = b.manager.CancelAll()
	} else {
		err = b.manager.Cancel(taskID)
		if err != nil {
			return tool.Result{}, fmt.Errorf("cancelling task: %w", err)
		}
		cancelledIDs = []string{taskID}
	}

	output := map[string]interface{}{
		"cancelled": cancelledIDs,
	}

	jsonBytes, err := json.Marshal(output)
	if err != nil {
		return tool.Result{}, fmt.Errorf("marshalling result: %w", err)
	}

	return tool.Result{Output: string(jsonBytes)}, nil
}
