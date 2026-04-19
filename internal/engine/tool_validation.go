package engine

import (
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// ValidateToolArgs checks that args conform to the tool's schema.
// Unknown keys are stripped (logged). Missing required keys return an error.
//
// Expected:
//   - schema is the tool's declared input schema.
//   - args is the map from the LLM's tool call.
//
// Returns:
//   - The sanitised args map (unknown keys removed).
//   - An error if a required key is missing.
//
// Side effects:
//   - Modifies args in place by deleting unknown keys.
func ValidateToolArgs(schema tool.Schema, args map[string]interface{}) (map[string]interface{}, error) {
	if len(schema.Properties) == 0 {
		return args, nil
	}

	// Strip unknown keys
	for key := range args {
		if _, known := schema.Properties[key]; !known {
			delete(args, key)
		}
	}

	// Check required keys
	var missing []string
	for _, req := range schema.Required {
		if _, ok := args[req]; !ok {
			missing = append(missing, req)
		}
	}
	if len(missing) > 0 {
		return args, fmt.Errorf("missing required arguments: %s", strings.Join(missing, ", "))
	}

	return args, nil
}
