package qdrant

// Point represents a Qdrant point for upsert operations.
type Point struct {
	ID      string         `json:"id"`
	Vector  []float64      `json:"vector"`
	Payload map[string]any `json:"payload,omitempty"`
}

// ScoredPoint represents a Qdrant search result with its relevance score.
type ScoredPoint struct {
	ID      string         `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload,omitempty"`
}

// CollectionConfig describes the vector collection configuration.
type CollectionConfig struct {
	VectorSize int    `json:"vector_size"`
	Distance   string `json:"distance"`
}
