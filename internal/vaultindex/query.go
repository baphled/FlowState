package vaultindex

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/baphled/flowstate/internal/recall/qdrant"
)

// DefaultTopK is the default number of chunks returned by query_vault.
const DefaultTopK = 5

// QueryArgs is the input schema for the query_vault MCP tool.
//
// Vault is accepted for forward compatibility with multi-vault setups; the
// current server is single-collection so the field is recorded but
// unused.
type QueryArgs struct {
	Question string `json:"question"`
	TopK     int    `json:"top_k,omitempty"`
	Vault    string `json:"vault,omitempty"`
}

// Chunk is one entry in the query_vault response.
//
// The shape matches the contract decoded by internal/recall/vault.Source.
type Chunk struct {
	Content    string `json:"content"`
	SourceFile string `json:"source_file"`
	ChunkIndex int    `json:"chunk_index"`
}

// QueryResponse is the JSON payload returned by query_vault.
type QueryResponse struct {
	Chunks []Chunk `json:"chunks"`
}

// Searcher is the subset of qdrant.VectorStore needed to answer queries.
type Searcher interface {
	// Search runs a vector search against the named collection and returns
	// the top-N scored points.
	Search(ctx context.Context, collection string, vector []float64, limit int) ([]qdrant.ScoredPoint, error)
}

// QueryHandler answers query_vault MCP calls by embedding the question and
// running a Qdrant vector search against the configured collection.
type QueryHandler struct {
	embedder   Embedder
	searcher   Searcher
	collection string
}

// NewQueryHandler constructs a QueryHandler.
//
// Expected:
//   - embedder is non-nil; it embeds the question text.
//   - searcher is non-nil; it runs the vector search.
//   - collection is the Qdrant collection name to query.
//
// Returns:
//   - A configured *QueryHandler.
//
// Side effects:
//   - None.
func NewQueryHandler(embedder Embedder, searcher Searcher, collection string) *QueryHandler {
	return &QueryHandler{embedder: embedder, searcher: searcher, collection: collection}
}

// Handle resolves a single query_vault request.
//
// Expected:
//   - args.Question is non-empty; empty questions return an empty result
//     without invoking the embedder.
//   - args.TopK defaults to DefaultTopK when zero or negative.
//
// Returns:
//   - A QueryResponse populated from the search hits.
//   - A wrapped error when embedding or searching fails.
//
// Side effects:
//   - Calls the embedder and the vector store.
func (q *QueryHandler) Handle(ctx context.Context, args QueryArgs) (QueryResponse, error) {
	if args.Question == "" {
		return QueryResponse{Chunks: []Chunk{}}, nil
	}
	topK := args.TopK
	if topK <= 0 {
		topK = DefaultTopK
	}
	vec, err := q.embedder.Embed(ctx, args.Question)
	if err != nil {
		return QueryResponse{}, fmt.Errorf("embedding query: %w", err)
	}
	points, err := q.searcher.Search(ctx, q.collection, vec, topK)
	if err != nil {
		return QueryResponse{}, fmt.Errorf("searching collection %s: %w", q.collection, err)
	}
	chunks := make([]Chunk, 0, len(points))
	for _, p := range points {
		chunks = append(chunks, chunkFromPayload(p.Payload))
	}
	return QueryResponse{Chunks: chunks}, nil
}

// RegisterQueryTool registers query_vault on the supplied MCP server.
//
// Expected:
//   - server is an initialised MCP server.
//   - handler is non-nil.
//
// Side effects:
//   - Registers a tool handler on the server.
func RegisterQueryTool(server *mcp.Server, handler *QueryHandler) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_vault",
		Description: "Search the indexed Obsidian vault and return relevant chunks.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args QueryArgs) (*mcp.CallToolResult, any, error) {
		resp, err := handler.Handle(ctx, args)
		if err != nil {
			return nil, nil, err
		}
		encoded, err := json.Marshal(resp)
		if err != nil {
			return nil, nil, fmt.Errorf("marshalling query_vault response: %w", err)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		}, nil, nil
	})
}

// chunkFromPayload extracts a Chunk from a Qdrant payload map.
//
// Missing or wrongly-typed fields fall back to zero values rather than
// failing the entire query — the server should still return whatever rows
// it can rather than blocking the caller's recall pipeline on a single
// malformed point.
func chunkFromPayload(payload map[string]any) Chunk {
	chunk := Chunk{}
	if v, ok := payload["content"].(string); ok {
		chunk.Content = v
	}
	if v, ok := payload["source_file"].(string); ok {
		chunk.SourceFile = v
	}
	chunk.ChunkIndex = intFromPayload(payload["chunk_index"])
	return chunk
}

// intFromPayload coerces a Qdrant payload value to int, accepting both
// JSON numbers (decoded as float64) and json.Number representations.
func intFromPayload(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return int(i)
		}
	}
	return 0
}
