package learning

// RecallMatch represents a single result returned by a recall search.
//
// This type is defined in the learning package (consumer-side interface) to avoid
// a direct import of internal/recall or any Qdrant package from internal/learning.
type RecallMatch struct {
	// ID is the unique identifier of the recalled entry.
	ID string
	// Content is the text content of the recalled entry.
	Content string
	// Score is the similarity score between the query and this match (0.0–1.0).
	Score float64
	// AgentID is the agent that produced the recalled entry, if known.
	AgentID string
}

// RecallClient provides semantic search over stored agent outputs.
//
// This is a consumer-side interface: it is defined here so that internal/learning
// never needs to import internal/recall directly. The bridge is provided by
// internal/app/learning_adapter.go.
type RecallClient interface {
	// Search performs a semantic search and returns the top matching entries.
	Search(query string, limit int) ([]RecallMatch, error)
}
