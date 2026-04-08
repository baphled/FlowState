package recall

import "context"

// LearningSource provides knowledge access and observation recording.
//
// It defines the methods used to query knowledge, store observations, and synthesise results.
type LearningSource interface {
	// Query searches for knowledge nodes using the memory client.
	Query(ctx context.Context, query string) ([]any, error)
	// Observe records new observations via the memory client.
	Observe(ctx context.Context, observations []any) error
	// Synthesize provides knowledge synthesis and insights.
	Synthesize(ctx context.Context, nodes []any) (string, error)
}
