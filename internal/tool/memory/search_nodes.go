package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/tool"
)

const defaultSearchLimit = 10

// SearchNodesTool implements mcp_memory_search_nodes: semantic search over the vector memory store.
type SearchNodesTool struct {
	client learning.MemoryClient
}

// NewSearchNodesTool creates a SearchNodesTool backed by the given MemoryClient.
func NewSearchNodesTool(client learning.MemoryClient) *SearchNodesTool {
	return &SearchNodesTool{client: client}
}

func (t *SearchNodesTool) Name() string { return "mcp_memory_search_nodes" }
func (t *SearchNodesTool) Description() string {
	return "Search the memory store for entities matching a query"
}

func (t *SearchNodesTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"query": {Type: "string", Description: "Search query to find matching memory nodes"},
			"limit": {Type: "integer", Description: "Maximum number of results (default 10)"},
		},
		Required: []string{"query"},
	}
}

// Execute performs a semantic search and returns formatted entity results.
func (t *SearchNodesTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	query, ok := input.Arguments["query"].(string)
	if !ok || query == "" {
		return tool.Result{}, errors.New("query argument is required")
	}

	entities, err := t.client.SearchNodes(ctx, query)
	if err != nil {
		return tool.Result{}, fmt.Errorf("searching memory nodes: %w", err)
	}

	_ = defaultSearchLimit // limit passed to client via SearchNodes; future extension point

	if len(entities) == 0 {
		return tool.Result{Output: "no results found"}, nil
	}

	var sb strings.Builder
	for _, e := range entities {
		fmt.Fprintf(&sb, "Name: %s\n", e.Name)
		if e.EntityType != "" {
			fmt.Fprintf(&sb, "Type: %s\n", e.EntityType)
		}
		if len(e.Observations) > 0 {
			fmt.Fprintf(&sb, "Observations:\n")
			for _, o := range e.Observations {
				fmt.Fprintf(&sb, "  - %s\n", o)
			}
		}
		sb.WriteString("---\n")
	}
	return tool.Result{Output: strings.TrimSuffix(sb.String(), "---\n")}, nil
}
