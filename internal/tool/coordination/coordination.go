package coordination

import (
	"context"
	"errors"
	"fmt"
	"strings"

	store "github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/tool"
)

const (
	toolName        = "coordination_store"
	toolDescription = "Read and write shared key-value context during agent delegation chains"

	operationGet    = "get"
	operationSet    = "set"
	operationList   = "list"
	operationDelete = "delete"
)

// Tool provides access to the coordination key-value store for cross-agent
// context sharing during delegation chains.
type Tool struct {
	store store.Store
}

// New creates a new coordination store tool backed by the given store.
//
// Expected:
//   - s is a non-nil Store implementation.
//
// Returns:
//   - A configured Tool instance.
//
// Side effects:
//   - None.
func New(s store.Store) *Tool {
	return &Tool{store: s}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "coordination_store".
//
// Side effects:
//   - None.
func (t *Tool) Name() string {
	return toolName
}

// Description returns a human-readable description of the coordination tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return toolDescription
}

// Schema returns the JSON schema for the coordination tool arguments.
//
// Returns:
//   - A tool.Schema describing the required and optional properties.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"operation": {
				Type:        "string",
				Description: "The operation to perform: get, set, list, or delete",
				Enum:        []string{operationGet, operationSet, operationList, operationDelete},
			},
			"key": {
				Type:        "string",
				Description: "The key to operate on (required for get, set, delete)",
			},
			"value": {
				Type:        "string",
				Description: "The value to store (required for set)",
			},
			"prefix": {
				Type:        "string",
				Description: "The key prefix to list (used by list operation)",
			},
		},
		Required: []string{"operation"},
	}
}

// Execute runs the specified coordination store operation.
//
// Expected:
//   - ctx is a valid context.
//   - input contains an "operation" string argument (get, set, list, delete).
//   - For get/set/delete: a "key" string argument is required.
//   - For set: a "value" string argument is required.
//   - For list: a "prefix" string argument is optional.
//
// Returns:
//   - A tool.Result containing the operation output.
//   - An error if the operation is unknown or arguments are missing.
//
// Side effects:
//   - May read from or write to the backing coordination store.
func (t *Tool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	operation, ok := input.Arguments["operation"].(string)
	if !ok || operation == "" {
		return tool.Result{}, errors.New("operation argument is required")
	}

	switch operation {
	case operationGet:
		return t.executeGet(input)
	case operationSet:
		return t.executeSet(input)
	case operationList:
		return t.executeList(input)
	case operationDelete:
		return t.executeDelete(input)
	default:
		return tool.Result{}, fmt.Errorf("unknown operation: %s", operation)
	}
}

// executeGet returns the stored value for the requested key.
//
// Expected:
//   - input contains a non-empty "key" string argument.
//
// Returns:
//   - A tool.Result containing the stored value.
//   - An error if the key is missing or the store lookup fails.
//
// Side effects:
//   - Reads from the backing coordination store.
func (t *Tool) executeGet(input tool.Input) (tool.Result, error) {
	key, ok := input.Arguments["key"].(string)
	if !ok || key == "" {
		return tool.Result{}, errors.New("key argument is required for get")
	}

	val, err := t.store.Get(key)
	if err != nil {
		return tool.Result{}, fmt.Errorf("getting key %q: %w", key, err)
	}

	return tool.Result{Output: string(val)}, nil
}

// executeSet stores the requested value for the requested key.
//
// Expected:
//   - input contains non-empty "key" and "value" string arguments.
//
// Returns:
//   - A tool.Result confirming the stored key.
//   - An error if required arguments are missing or the store write fails.
//
// Side effects:
//   - Writes to the backing coordination store.
func (t *Tool) executeSet(input tool.Input) (tool.Result, error) {
	key, ok := input.Arguments["key"].(string)
	if !ok || key == "" {
		return tool.Result{}, errors.New("key argument is required for set")
	}

	value, ok := input.Arguments["value"].(string)
	if !ok {
		return tool.Result{}, errors.New("value argument is required for set")
	}

	if err := t.store.Set(key, []byte(value)); err != nil {
		return tool.Result{}, fmt.Errorf("setting key %q: %w", key, err)
	}

	return tool.Result{Output: fmt.Sprintf("stored key %q", key)}, nil
}

// executeList returns keys that match the requested prefix.
//
// Expected:
//   - input may include an optional "prefix" string argument.
//
// Returns:
//   - A tool.Result containing newline-separated keys.
//   - An error if the store lookup fails.
//
// Side effects:
//   - Reads from the backing coordination store.
func (t *Tool) executeList(input tool.Input) (tool.Result, error) {
	prefix, ok := input.Arguments["prefix"].(string)
	if !ok {
		prefix = ""
	}

	keys, err := t.store.List(prefix)
	if err != nil {
		return tool.Result{}, fmt.Errorf("listing keys with prefix %q: %w", prefix, err)
	}

	return tool.Result{Output: strings.Join(keys, "\n")}, nil
}

// executeDelete removes the requested key from the store.
//
// Expected:
//   - input contains a non-empty "key" string argument.
//
// Returns:
//   - A tool.Result confirming the deleted key.
//   - An error if the key is missing or the store delete fails.
//
// Side effects:
//   - Writes to the backing coordination store.
func (t *Tool) executeDelete(input tool.Input) (tool.Result, error) {
	key, ok := input.Arguments["key"].(string)
	if !ok || key == "" {
		return tool.Result{}, errors.New("key argument is required for delete")
	}

	if err := t.store.Delete(key); err != nil {
		return tool.Result{}, fmt.Errorf("deleting key %q: %w", key, err)
	}

	return tool.Result{Output: fmt.Sprintf("deleted key %q", key)}, nil
}
