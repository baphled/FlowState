package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/tool"
)

// Tool implements the tool.Tool interface for coordination store operations.
type Tool struct {
	store coordination.Store
}

// New creates a new coordination tool instance with the given store path.
//
// Expected:
//   - storePath is the file path for the coordination store.
//
// Returns:
//   - A configured Tool instance.
//
// Side effects:
//   - Creates a new FileStore at the given path.
func New(storePath string) *Tool {
	return &Tool{
		store: coordination.NewFileStore(storePath),
	}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "coordination_store".
//
// Side effects:
//   - None.
func (t *Tool) Name() string {
	return "coordination_store"
}

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return "Get/set/list coordination store entries for cross-agent context"
}

// Schema returns the JSON schema for the tool inputs.
//
// Returns:
//   - A tool.Schema describing the operation, key, and value properties.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"operation": {
				Type:        "string",
				Description: "Operation to perform: get, set, or list",
				Enum:        []string{"get", "set", "list"},
			},
			"key": {
				Type:        "string",
				Description: "Key to get, set, or list (prefix for list)",
			},
			"value": {
				Type:        "string",
				Description: "Value to set (only for set operation)",
			},
		},
		Required: []string{"operation", "key"},
	}
}

// Execute performs the coordination store operation specified in input.
//
// Expected:
//   - input contains an "operation" string (get, set, or list).
//   - input contains a "key" string (can be empty for list operation).
//   - For set operation, input contains a "value" string.
//
// Returns:
//   - A tool.Result containing the operation result as a JSON string.
//   - An error if the operation fails or arguments are invalid.
//
// Side effects:
//   - May read from or write to the filesystem via the store.
func (t *Tool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	operation, ok := input.Arguments["operation"].(string)
	if !ok || operation == "" {
		return tool.Result{}, errors.New("operation argument is required")
	}

	key, ok := input.Arguments["key"].(string)
	// For list operation, key can be empty to list all keys
	if !ok || (key == "" && operation != "list") {
		return tool.Result{}, errors.New("key argument is required")
	}

	switch operation {
	case "get":
		return t.executeGet(key)
	case "set":
		value, ok := input.Arguments["value"].(string)
		if !ok {
			return tool.Result{}, errors.New("value argument is required for set operation")
		}
		return t.executeSet(key, value)
	case "list":
		return t.executeList(key)
	default:
		return tool.Result{}, fmt.Errorf("unknown operation: %s", operation)
	}
}

// executeGet retrieves a value by key.
//
// Expected:
//   - key is the key to retrieve.
//
// Returns:
//   - A tool.Result containing the key and value as JSON.
//   - An error if the key does not exist.
//
// Side effects:
//   - None.
func (t *Tool) executeGet(key string) (tool.Result, error) {
	value, err := t.store.Get(key)
	if err != nil {
		return tool.Result{}, err
	}

	output := map[string]string{
		"key":   key,
		"value": string(value),
	}

	data, err := json.Marshal(output)
	if err != nil {
		return tool.Result{}, fmt.Errorf("marshalling result: %w", err)
	}

	return tool.Result{Output: string(data)}, nil
}

// executeSet stores a key-value pair.
//
// Expected:
//   - key is the key to store.
//   - value is the value to store.
//
// Returns:
//   - A tool.Result containing the key and status as JSON.
//   - An error if the store operation fails.
//
// Side effects:
//   - Writes to the filesystem via the store.
func (t *Tool) executeSet(key, value string) (tool.Result, error) {
	err := t.store.Set(key, []byte(value))
	if err != nil {
		return tool.Result{}, err
	}

	output := map[string]string{
		"key":    key,
		"status": "stored",
	}

	data, err := json.Marshal(output)
	if err != nil {
		return tool.Result{}, fmt.Errorf("marshalling result: %w", err)
	}

	return tool.Result{Output: string(data)}, nil
}

// executeList lists keys matching a prefix.
//
// Expected:
//   - prefix is the key prefix to filter by. Empty string returns all keys.
//
// Returns:
//   - A tool.Result containing a JSON array of matching keys and the count.
//   - An error if the list operation fails.
//
// Side effects:
//   - None.
func (t *Tool) executeList(prefix string) (tool.Result, error) {
	keys, err := t.store.List(prefix)
	if err != nil {
		return tool.Result{}, err
	}

	output := map[string]interface{}{
		"keys":  keys,
		"count": len(keys),
	}

	data, err := json.Marshal(output)
	if err != nil {
		return tool.Result{}, fmt.Errorf("marshalling result: %w", err)
	}

	return tool.Result{Output: string(data)}, nil
}
