package recall

import "context"

// LearningSource provides knowledge access and observation recording
//
// Query searches for knowledge nodes using the memory client
// Observe records new observations via the memory client
// Synthesize provides knowledge synthesis and insights
type LearningSource interface {
	Query(ctx context.Context, query string) ([]any, error)
	Observe(ctx context.Context, observations []any) error
	Synthesize(ctx context.Context, nodes []any) (string, error)
}
