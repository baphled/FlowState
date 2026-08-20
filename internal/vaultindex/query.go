package vaultindex

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/baphled/flowstate/internal/recall/qdrant"
)

// DefaultTopK is the default number of chunks returned by query_vault.
const DefaultTopK = 5

// VaultCollectionPrefix is the canonical stem for vault Qdrant
// collections. The default shared collection equals the bare prefix and
// each per-vault collection appends "-<slug>" to it, matching the
// collections the `flowstate vault index --collection` CLI creates. It is
// aliased by toolset.DefaultVaultCollection so every site that names a
// collection resolves the same stem.
const VaultCollectionPrefix = "flowstate-vault"

// vaultSlugSeparators matches every run of characters that is not a
// lowercase ASCII alphanumeric, so vault names collapse onto a
// dash-separated slug.
var vaultSlugSeparators = regexp.MustCompile(`[^a-z0-9]+`)

// knownVaultSlugs is the canonical set of vault-name slugs that resolve to
// a per-vault collection. Vault arguments whose slug falls outside this
// set fall back to the default collection so agent-supplied garbage never
// targets a collection that cannot exist.
var knownVaultSlugs = map[string]struct{}{
	"baphled":           {},
	"personal":          {},
	"book-of-yonix":     {},
	"boodah-cooks":      {},
	"n-vyro-io":         {},
	"boodah-consulting": {},
	"colledge":          {},
	"fullspektrum":      {},
	"flowstate":         {},
}

// QueryArgs is the input schema for the query_vault MCP tool.
//
// Vault optionally scopes the query to a per-vault collection: when the
// slug derived from the vault name matches a known vault, the search
// targets flowstate-vault-<slug>; otherwise the handler's default
// collection is used.
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
// running a Qdrant vector search against the configured collection, or the
// per-vault collection derived from the request's vault argument when one
// is supplied.
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
//   - collection is the Qdrant collection name to query when no vault
//     scope is supplied.
//
// Returns:
//   - A configured *QueryHandler.
//
// Side effects:
//   - None.
func NewQueryHandler(embedder Embedder, searcher Searcher, collection string) *QueryHandler {
	return &QueryHandler{embedder: embedder, searcher: searcher, collection: collection}
}

// ResolveQueryCollection returns the Qdrant collection a query_vault
// request should search. A vault whose slug appears in the canonical
// vault table resolves to flowstate-vault-<slug>; empty, whitespace-only,
// and unknown vault names fall back to defaultCollection so callers never
// error or target a collection that cannot exist.
//
// Expected:
//   - vault is the raw vault argument from the tool call; any casing is
//     accepted.
//   - defaultCollection is the configured default collection
//     (cfg.VaultCollection or VaultCollectionPrefix).
//
// Returns:
//   - The per-vault collection name for a known vault.
//   - defaultCollection otherwise.
//
// Side effects:
//   - None.
func ResolveQueryCollection(vault, defaultCollection string) string {
	slug := slugifyVault(vault)
	if slug == "" {
		return defaultCollection
	}
	if _, ok := knownVaultSlugs[slug]; !ok {
		return defaultCollection
	}
	return VaultCollectionPrefix + "-" + slug
}

// slugifyVault derives the collection slug for a vault name: lowercase,
// every run of non-alphanumeric characters collapsed to a single dash,
// with leading and trailing dashes trimmed.
//
// Expected: name is the raw vault argument.
// Returns: result of slugifyVault.
// Side effects: None.
func slugifyVault(name string) string {
	slugged := vaultSlugSeparators.ReplaceAllString(strings.ToLower(name), "-")
	return strings.Trim(slugged, "-")
}

// Handle resolves a single query_vault request.
//
// Expected:
//   - args.Question is non-empty; empty questions return an empty result
//     without invoking the embedder.
//   - args.TopK defaults to DefaultTopK when zero or negative.
//   - args.Vault, when it slugifies to a known vault, redirects the
//     search to that vault's per-vault collection.
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
	collection := ResolveQueryCollection(args.Vault, q.collection)
	vec, err := q.embedder.Embed(ctx, args.Question)
	if err != nil {
		return QueryResponse{}, fmt.Errorf("embedding query: %w", err)
	}
	points, err := q.searcher.Search(ctx, collection, vec, topK)
	if err != nil {
		return QueryResponse{}, fmt.Errorf("searching collection %s: %w", collection, err)
	}
	chunks := make([]Chunk, 0, len(points))
	for _, p := range points {
		chunks = append(chunks, chunkFromPayload(p.Payload))
	}
	return QueryResponse{Chunks: chunks}, nil
}

// chunkFromPayload extracts a Chunk from a Qdrant payload map.
//
// Missing or wrongly-typed fields fall back to zero values rather than
// failing the entire query — the server should still return whatever rows
// it can rather than blocking the caller's recall pipeline on a single
// malformed point.
//
// Expected: parameters for chunkFromPayload.
// Returns: result of chunkFromPayload.
// Side effects: None.
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
//
// Expected: parameters for intFromPayload.
// Returns: result of intFromPayload.
// Side effects: None.
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
