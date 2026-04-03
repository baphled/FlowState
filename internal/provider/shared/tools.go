package shared

import "github.com/baphled/flowstate/internal/provider"

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
