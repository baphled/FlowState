package recall

import (
	"context"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/learning"
)

// MCPMemorySource adapts a LearningSource to the Source interface for the recall broker.
//
// Expected: ls must be a non-nil LearningSource backed by the MCP memory graph.
// Returns: A Source that queries the MCP memory graph and maps entities to Observations.
// Side effects: None.
type MCPMemorySource struct {
	ls LearningSource
}

// Compile-time assertion that MCPMemorySource implements Source.
var _ Source = (*MCPMemorySource)(nil)

// NewMCPMemorySource constructs an MCPMemorySource wrapping the supplied LearningSource.
//
// Expected: ls is a non-nil LearningSource.
// Returns: A pointer to MCPMemorySource.
// Side effects: None.
func NewMCPMemorySource(ls LearningSource) *MCPMemorySource {
	return &MCPMemorySource{ls: ls}
}

// Query retrieves observations from the MCP memory graph for the given query.
//
// Expected: ctx is non-nil and query is the search term; limit caps the result count.
// Returns: Observations mapped from MCP memory entities, or an empty slice on nil results.
// Side effects: Calls the underlying LearningSource which contacts the MCP server.
func (m *MCPMemorySource) Query(ctx context.Context, query string, limit int) ([]Observation, error) {
	raw, err := m.ls.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	agentID := agentIDFromCtx(ctx)
	observations := make([]Observation, 0, len(raw))
	for _, item := range raw {
		entity, ok := item.(learning.Entity)
		if !ok {
			continue
		}
		observations = append(observations, Observation{
			ID:        entity.Name,
			Source:    "mcp-memory",
			AgentID:   agentID,
			Timestamp: time.Now(),
			Content:   strings.Join(entity.Observations, "\n"),
		})
	}
	if limit > 0 && len(observations) > limit {
		observations = observations[:limit]
	}
	return observations, nil
}

// agentIDFromCtx extracts the agent identifier from the context if present.
//
// Expected: ctx may contain a string value under learning.AgentIDKey.
// Returns: The agent identifier when present and a string, otherwise an empty string.
// Side effects: None.
func agentIDFromCtx(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v := ctx.Value(learning.AgentIDKey)
	if id, ok := v.(string); ok {
		return id
	}
	return ""
}
