package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/tool"
)

// OpenNodesTool implements mcp_memory_open_nodes: retrieve specific entities and their relations by name.
type OpenNodesTool struct {
	client learning.MemoryClient
}

// NewOpenNodesTool creates an OpenNodesTool backed by the given MemoryClient.
func NewOpenNodesTool(client learning.MemoryClient) *OpenNodesTool {
	return &OpenNodesTool{client: client}
}

func (t *OpenNodesTool) Name() string        { return "mcp_memory_open_nodes" }
func (t *OpenNodesTool) Description() string { return "Retrieve specific memory nodes by name" }

func (t *OpenNodesTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"names": {Type: "array", Description: "List of entity names to retrieve"},
		},
		Required: []string{"names"},
	}
}

// Execute retrieves the requested nodes and returns a formatted knowledge graph.
func (t *OpenNodesTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	raw, ok := input.Arguments["names"]
	if !ok {
		return tool.Result{}, errors.New("names argument is required")
	}
	names, err := toStringSlice(raw)
	if err != nil || len(names) == 0 {
		return tool.Result{}, errors.New("names argument must be a non-empty array of strings")
	}

	graph, err := t.client.OpenNodes(ctx, names)
	if err != nil {
		return tool.Result{}, fmt.Errorf("opening memory nodes: %w", err)
	}

	if len(graph.Entities) == 0 && len(graph.Relations) == 0 {
		return tool.Result{Output: "no nodes found"}, nil
	}

	var sb strings.Builder
	if len(graph.Entities) > 0 {
		sb.WriteString("Entities:\n")
		for _, e := range graph.Entities {
			fmt.Fprintf(&sb, "  %s (%s)\n", e.Name, e.EntityType)
			for _, o := range e.Observations {
				fmt.Fprintf(&sb, "    - %s\n", o)
			}
		}
	}
	if len(graph.Relations) > 0 {
		sb.WriteString("Relations:\n")
		for _, r := range graph.Relations {
			fmt.Fprintf(&sb, "  %s -[%s]-> %s\n", r.From, r.RelationType, r.To)
		}
	}
	return tool.Result{Output: sb.String()}, nil
}

// toStringSlice coerces an interface{} to []string, accepting both
// []interface{} (from JSON decode) and []string.
func toStringSlice(v interface{}) ([]string, error) {
	switch typed := v.(type) {
	case []string:
		return typed, nil
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("names element is not a string: %T", item)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("names must be an array, got %T", v)
	}
}
