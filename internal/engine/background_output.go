package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/baphled/flowstate/internal/tool"
)

// BackgroundOutputTool enables retrieval of background task results by task ID.
type BackgroundOutputTool struct {
	manager *BackgroundTaskManager
}

// NewBackgroundOutputTool creates a new background output tool.
//
// Expected:
//   - manager is a non-nil BackgroundTaskManager instance.
//
// Returns:
//   - A ready-to-use BackgroundOutputTool instance.
//
// Side effects:
//   - None.
func NewBackgroundOutputTool(manager *BackgroundTaskManager) *BackgroundOutputTool {
	return &BackgroundOutputTool{manager: manager}
}

// Name returns the tool name.
//
// Returns:
//   - The string "background_output".
//
// Side effects:
//   - None.
func (b *BackgroundOutputTool) Name() string {
	return "background_output"
}

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (b *BackgroundOutputTool) Description() string {
	return "Retrieve background task results by task ID"
}

// Schema returns the JSON schema for the background output tool input.
//
// Returns:
//   - A tool.Schema describing required and optional parameters.
//
// Side effects:
//   - None.
func (b *BackgroundOutputTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"task_id": {
				Type:        "string",
				Description: "The unique identifier of the background task",
			},
			"block": {
				Type:        "boolean",
				Description: "If true, poll until task completes; default is false",
			},
			"timeout": {
				Type:        "integer",
				Description: "Maximum time in milliseconds to wait when block=true",
			},
			"full_session": {
				Type:        "boolean",
				Description: "If true, include full message history in result",
			},
		},
		Required: []string{"task_id"},
	}
}

// Execute retrieves task results by task ID.
//
// Expected:
//   - ctx is a valid context for the retrieval operation.
//   - input contains "task_id" string argument.
//   - Optional "block" boolean to poll until completion.
//   - Optional "timeout" integer for blocking timeout in milliseconds.
//   - Optional "full_session" boolean to include full message history.
//
// Returns:
//   - A tool.Result containing the task status and result as JSON.
//   - An error if the task is not found or other failures occur.
//
// Side effects:
//   - None.
func (b *BackgroundOutputTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	taskID, ok := input.Arguments["task_id"].(string)
	if !ok || taskID == "" {
		return tool.Result{}, errors.New("task_id is required and must be a string")
	}

	task, found := b.manager.Get(taskID)
	if !found {
		return tool.Result{}, fmt.Errorf("task not found: %s", taskID)
	}

	block := false
	if raw, ok := input.Arguments["block"].(bool); ok {
		block = raw
	}
	var timeoutMs int
	if raw, ok := input.Arguments["timeout"]; ok {
		switch v := raw.(type) {
		case float64:
			timeoutMs = int(v)
		case int:
			timeoutMs = v
		}
	}
	fullSession := false
	if raw, ok := input.Arguments["full_session"].(bool); ok {
		fullSession = raw
	}

	if block {
		if err := b.blockUntilComplete(ctx, taskID, timeoutMs); err != nil {
			return tool.Result{}, err
		}
		task, _ = b.manager.Get(taskID)
	}

	result := b.buildResultOutput(task, fullSession)
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return tool.Result{}, fmt.Errorf("marshalling result: %w", err)
	}

	return tool.Result{Output: string(jsonBytes)}, nil
}

// pollUntilComplete polls a task until it reaches a terminal state or timeout.
//
// Expected:
//   - taskID is a valid task identifier.
//   - timeoutMs is the maximum wait time in milliseconds.
//
// Returns:
//   - The terminal status string on success, or empty string on timeout.
//
// Side effects:
//   - Blocks the caller and sleeps between polls.
func (b *BackgroundOutputTool) pollUntilComplete(_ context.Context, taskID string, timeoutMs int) string {
	const defaultTimeoutMs = 30000
	const pollIntervalMs = 50

	if timeoutMs == 0 {
		timeoutMs = defaultTimeoutMs
	}

	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)

	for {
		task, found := b.manager.Get(taskID)
		if !found {
			return ""
		}

		status := task.Status.Load()
		if isTerminalStatus(status) {
			return status
		}

		if time.Now().After(deadline) {
			return ""
		}

		time.Sleep(time.Duration(pollIntervalMs) * time.Millisecond)
	}
}

// blockUntilComplete polls a task until completion or timeout.
//
// Expected:
//   - taskID is a valid task identifier.
//   - timeoutMs is the maximum wait time in milliseconds.
//
// Returns:
//   - An error if the polling times out.
//
// Side effects:
//   - Blocks the caller until the task reaches a terminal state or timeout.
func (b *BackgroundOutputTool) blockUntilComplete(ctx context.Context, taskID string, timeoutMs int) error {
	finalStatus := b.pollUntilComplete(ctx, taskID, timeoutMs)
	if finalStatus == "" {
		return errors.New("task polling timeout exceeded")
	}
	return nil
}

// buildResultOutput constructs the result output map for a task.
//
// Expected:
//   - task is a non-nil BackgroundTask instance.
//   - fullSession indicates whether to include the full_session flag.
//
// Returns:
//   - A map containing task ID, status, result, error, and optional full_session flag.
//
// Side effects:
//   - None.
func (b *BackgroundOutputTool) buildResultOutput(task *BackgroundTask, fullSession bool) map[string]interface{} {
	output := map[string]interface{}{
		"task_id": task.ID,
		"status":  task.Status.Load(),
	}

	if task.Result != "" {
		output["result"] = task.Result
	}

	if task.Error != nil {
		output["error"] = task.Error.Error()
	}

	if fullSession {
		output["full_session"] = true
	}

	return output
}

// isTerminalStatus reports whether a status is in a terminal state.
//
// Expected:
//   - status is a non-empty status string.
//
// Returns:
//   - true if status is completed, failed, or cancelled.
//
// Side effects:
//   - None.
func isTerminalStatus(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}
