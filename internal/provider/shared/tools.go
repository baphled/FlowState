package shared

import (
	"encoding/json"
	"log/slog"

	"github.com/baphled/flowstate/internal/provider"
)

// BaseToolSchema holds the provider-agnostic fields extracted from a provider.Tool.
// It serves as an intermediate representation used by each provider to build
// its own wire-format tool definition without duplicating field extraction logic.
type BaseToolSchema struct {
	Name        string
	Description string
	Properties  map[string]interface{}
	Required    []string
}

// BuildBaseToolSchema extracts the common fields from a provider.Tool into a
// BaseToolSchema. Provider-specific annotations (such as cache-control or
// type wrappers) remain the responsibility of each provider.
//
// Expected:
//   - t is a provider.Tool with Name, Description, and Schema populated.
//
// Returns:
//   - A BaseToolSchema containing Name, Description, Properties, and Required.
//
// Side effects:
//   - None.
func BuildBaseToolSchema(t provider.Tool) BaseToolSchema {
	return BaseToolSchema{
		Name:        t.Name,
		Description: t.Description,
		Properties:  t.Schema.Properties,
		Required:    t.Schema.Required,
	}
}

// ParseToolArguments unmarshals a JSON string into a map of tool arguments.
//
// Expected:
//   - raw is a JSON object string, or empty.
//
// Returns:
//   - A map of argument key-value pairs on success.
//   - nil if raw is empty or cannot be parsed.
//
// Side effects:
//   - None.
func ParseToolArguments(raw string) map[string]interface{} {
	slog.Debug("question-path: ParseToolArguments entry", "raw_len", len(raw))
	if raw == "" {
		slog.Warn("question-path: ParseToolArguments empty raw argument, returning nil")
		return nil
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &args); err == nil {
		slog.Debug("question-path: ParseToolArguments decoded object", "keys", len(args))
		return args
	}
	var inner string
	if err := json.Unmarshal([]byte(raw), &inner); err == nil {
		var args2 map[string]interface{}
		if err2 := json.Unmarshal([]byte(inner), &args2); err2 == nil {
			slog.Warn("question-path: ParseToolArguments re-decoded double-encoded string args", "keys", len(args2))
			return args2
		}
	}
	slog.Warn("question-path: ParseToolArguments unparseable argument payload, returning nil", "raw_len", len(raw))
	return nil
}
