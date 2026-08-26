package question

import (
	"context"
	"errors"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// Tool implements a clarifying question prompt.
type Tool struct{}

// New creates a new question tool instance.
//
// Returns:
//   - A Tool configured to prompt clarifying questions.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func New() *Tool { return &Tool{} }

// Name returns the tool identifier.
//
// Returns:
//   - The string "question".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Name() string { return "question" }

// Description returns a human-readable description of the question tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Description() string { return "Ask the user a clarifying question" }

// Schema returns the input schema for the question tool.
//
// Returns:
//   - A schema describing question, options, and allow_multiple.
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
			"question": {Type: "string", Description: "The question to ask the user"},
			"options": {
				Type:        "array",
				Description: "Optional answer options",
				Items:       map[string]interface{}{"type": "string"},
			},
			"allow_multiple": {Type: "boolean", Description: "Whether multiple options may be selected"},
		},
		Required: []string{"question"},
	}
}

// Execute returns the question payload for UI rendering.
//
// Expected:
//   - input contains a non-empty question argument.
//
// Returns:
//   - A tool.Result containing question metadata or an error.
//
// Side effects:
//   - None.
func (t *Tool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	question, ok := input.Arguments["question"].(string)
	if !ok || strings.TrimSpace(question) == "" {
		return tool.Result{}, errors.New("question argument is required")
	}

	result := tool.Result{
		Title:  "Question",
		Output: question,
		Metadata: map[string]interface{}{
			"question": question,
		},
	}

	if rawOptions, ok := input.Arguments["options"].([]any); ok && len(rawOptions) > 0 {
		options := make([]string, 0, len(rawOptions))
		for _, raw := range rawOptions {
			option, ok := raw.(string)
			if !ok {
				return tool.Result{}, errors.New("options must contain strings")
			}
			options = append(options, option)
		}
		result.Metadata["options"] = options
	}

	if allowMultiple, ok := input.Arguments["allow_multiple"].(bool); ok {
		result.Metadata["allow_multiple"] = allowMultiple
	}

	return result, nil
}
