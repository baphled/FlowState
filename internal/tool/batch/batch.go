// Package batch provides a tool for concurrent execution of multiple tool calls.
//
// This package implements the batch tool, which:
//   - Executes multiple tool invocations concurrently using errgroup
//   - Aggregates individual results and errors, preserving all outputs
//   - Handles context cancellation and error propagation robustly
//   - Returns a single result object containing all tool outputs and error details
//   - Is intended for use within the FlowState agent platform to enable efficient, parallel tool workflows
package batch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/tool"
)

// Tool implements the batch tool for concurrent execution of multiple tools.
type Tool struct {
	registry *tool.Registry
}

// batchResult captures the outcome of one batch tool invocation.
type batchResult struct {
	name   string
	output string
	err    error
}

// New creates and returns a new batch tool instance.
//
// Returns:
//   - A Tool configured to execute tool calls concurrently.
//
// Expected:
//   - registry must not be nil.
//
// Side effects:
//   - None.
func New(registry *tool.Registry) *Tool {
	return &Tool{registry: registry}
}

// Name returns the unique identifier for the batch tool.
//
// Returns:
//   - The string "batch".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Name() string {
	return "batch"
}

// Description returns a human-readable summary of the batch tool's purpose.
//
// Returns:
//   - A short description of the tool.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return "Execute multiple tool calls concurrently"
}

// Schema returns the input schema for the batch tool, describing the expected arguments.
//
// Returns:
//   - A schema describing the tools array.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"tools": {
				Type:        "array",
				Description: "Tool calls to execute concurrently",
				Items: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name":      map[string]interface{}{"type": "string"},
						"arguments": map[string]interface{}{"type": "object"},
					},
					"required": []any{"name"},
				},
			},
		},
		Required: []string{"tools"},
	}
}

// Execute runs the batch tool, executing multiple tool calls concurrently.
//
// Expected:
//   - input contains a tools array of tool call objects.
//
// Returns:
//   - A tool.Result containing aggregated outputs or an error.
//
// Side effects:
//   - Launches goroutines to execute registered tools.
func (t *Tool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	if t.registry == nil {
		return tool.Result{}, errors.New("tool registry is required")
	}

	rawTools, ok := input.Arguments["tools"].([]any)
	if !ok {
		return tool.Result{}, errors.New("tools argument is required")
	}

	results := make([]batchResult, len(rawTools))
	resultCh := make(chan struct {
		index int
		item  batchResult
	}, len(rawTools))
	for idx, raw := range rawTools {
		go func(index int, raw any) {
			resultCh <- struct {
				index int
				item  batchResult
			}{index: index, item: t.executeOne(ctx, raw)}
		}(idx, raw)
	}
	for range rawTools {
		result := <-resultCh
		results[result.index] = result.item
	}

	outputs := make([]map[string]any, 0, len(results))
	var failureCount int
	var failureSummary string
	for _, result := range results {
		entry := map[string]any{"name": result.name, "output": result.output}
		if result.err != nil {
			failureCount++
			entry["error"] = result.err.Error()
			if failureSummary == "" {
				failureSummary = result.err.Error()
			} else {
				failureSummary += "; " + result.err.Error()
			}
		}
		outputs = append(outputs, entry)
	}

	payload, err := json.Marshal(outputs)
	if err != nil {
		return tool.Result{}, fmt.Errorf("encoding batch results: %w", err)
	}

	result := tool.Result{Output: string(payload)}
	if failureCount > 0 {
		result.Error = fmt.Errorf("%d tool call(s) failed: %s", failureCount, failureSummary)
	}
	return result, nil
}

// executeOne executes a single batch tool call.
//
// Expected:
//   - raw is one tool call object from the batch input.
//
// Returns:
//   - The name, output, and error for one tool call.
//
// Side effects:
//   - Invokes another registered tool.
func (t *Tool) executeOne(ctx context.Context, raw any) batchResult {
	call, ok := raw.(map[string]any)
	if !ok {
		return batchResult{err: errors.New("tool call must be an object")}
	}

	name, ok := call["name"].(string)
	if !ok {
		return batchResult{err: errors.New("tool call name is required")}
	}
	if name == "" {
		return batchResult{err: errors.New("tool call name is required")}
	}

	args, ok := call["arguments"].(map[string]any)
	if !ok && call["arguments"] != nil {
		return batchResult{err: errors.New("tool call arguments must be an object")}
	}
	if args == nil {
		args = map[string]any{}
	}

	toolInstance, err := t.registry.Get(name)
	if err != nil {
		return batchResult{name: name, err: fmt.Errorf("getting tool %q: %w", name, err)}
	}

	result, err := toolInstance.Execute(ctx, tool.Input{Name: name, Arguments: args})
	if err != nil {
		return batchResult{name: name, err: fmt.Errorf("executing tool %q: %w", name, err)}
	}
	if result.Error != nil {
		return batchResult{name: name, output: result.Output, err: result.Error}
	}

	return batchResult{name: name, output: result.Output}
}
