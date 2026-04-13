// Package vault provides a vault-rag-backed recall source for FlowState.
//
// This package implements a recall.Source that queries the vault-rag MCP server
// via the mcp.Client interface, enabling FlowState to retrieve observations
// from an Obsidian vault as a knowledge source alongside Qdrant and the MCP memory graph.
package vault

import (
	"context"
	"strconv"
	"time"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/mcp"
	"github.com/baphled/flowstate/internal/recall"
)

// vaultChunk represents a single chunk returned by the vault-rag MCP server.
type vaultChunk struct {
	Content    string `json:"content"`
	SourceFile string `json:"source_file"`
	ChunkIndex int    `json:"chunk_index"`
}

// vaultResponse represents the top-level response from the vault-rag MCP server.
type vaultResponse struct {
	Chunks []vaultChunk `json:"chunks"`
}

// Source implements recall.Source for the vault-rag MCP server.
//
// Expected: Provides recall.Observation results from an Obsidian vault via MCP.
// Returns: Observations relevant to the query, or an empty slice if unavailable.
// Side effects: Performs MCP tool calls to the vault-rag server.
type Source struct {
	client     mcp.Client
	serverName string
	vault      string
}

var _ recall.Source = (*Source)(nil)

// NewVaultSource constructs a Source for the given MCP client, server name, and vault.
//
// Expected: Instantiates a recall.Source backed by vault-rag.
// Returns: Pointer to Source.
// Side effects: None.
func NewVaultSource(client mcp.Client, serverName, vault string) *Source {
	if serverName == "" {
		serverName = "vault-rag"
	}
	return &Source{
		client:     client,
		serverName: serverName,
		vault:      vault,
	}
}

// Query returns recall observations for the supplied query and limit.
//
// Expected: ctx may contain an agent ID under learning.AgentIDKey. Query and limit are forwarded to the vault-rag MCP server.
// Returns: A slice of recall.Observation, or an empty slice if no results or on error.
// Side effects: Calls the "query_vault" tool on the MCP server and parses the result.
func (v *Source) Query(ctx context.Context, query string, limit int) ([]recall.Observation, error) {
	agentID := agentIDFromContext(ctx)

	args := map[string]any{
		"question": query,
		"vault":    v.vault,
		"top_k":    limit,
	}

	result, err := v.client.CallTool(ctx, v.serverName, "query_vault", args)
	if err != nil {
		return []recall.Observation{}, err
	}
	if result == nil || result.IsError {
		return []recall.Observation{}, nil
	}

	var resp vaultResponse
	empty, err := mcp.DecodeContent(result.Content, &resp,
		"tool", "query_vault", "server", v.serverName)
	if err != nil {
		return []recall.Observation{}, err
	}
	if empty {
		return []recall.Observation{}, nil
	}

	observations := make([]recall.Observation, 0, len(resp.Chunks))
	for _, chunk := range resp.Chunks {
		observations = append(observations, recall.Observation{
			ID:        "vault:" + chunk.SourceFile + ":" + strconv.Itoa(chunk.ChunkIndex),
			Source:    "vault-rag",
			AgentID:   agentID,
			Timestamp: time.Now(),
			Content:   chunk.Content,
		})
	}
	return observations, nil
}

// agentIDFromContext extracts the agent identifier from ctx if present.
//
// Expected: ctx may contain a string value under learning.AgentIDKey.
// Returns: The agent identifier when present and a string, otherwise an empty string.
// Side effects: None.
func agentIDFromContext(ctx context.Context) string {
	if val := ctx.Value(learning.AgentIDKey); val != nil {
		if id, ok := val.(string); ok {
			return id
		}
	}
	return ""
}
