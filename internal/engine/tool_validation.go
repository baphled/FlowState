package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// ValidateToolArgs checks that args conform to the tool's schema. Unknown keys
// produce an error naming them so the model can self-correct on the next
// turn. Missing required keys also produce an error. The unknown-key check
// is reported first because it is the more common failure mode and yields
// the most actionable feedback to the model — a missing key is often a
// downstream consequence of an unknown key carrying the same payload under
// the wrong name.
//
// Expected:
//   - schema is the tool's declared input schema. A schema with no Properties
//     opts out of validation entirely (the tool accepts anything).
//   - args is the map from the LLM's tool call.
//
// Returns:
//   - The args map unchanged on success (no key removal).
//   - nil on error.
//   - An error naming the unknown keys when any are present.
//   - An error naming the missing required keys when no unknown keys are
//     present and required keys are absent.
//
// Side effects:
//   - None. The args map is never mutated; callers see exactly the keys the
//     model produced. Earlier behaviour silently stripped unknown keys and
//     reported success — that masked hallucinated arguments and let the
//     model double down on them on subsequent turns.
func ValidateToolArgs(schema tool.Schema, args map[string]interface{}) (map[string]interface{}, error) {
	if len(schema.Properties) == 0 {
		return args, nil
	}

	var unknown []string
	for key := range args {
		if _, known := schema.Properties[key]; !known {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown arguments: %s", strings.Join(unknown, ", "))
	}

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
