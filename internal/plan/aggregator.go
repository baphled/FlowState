package plan

import (
	"context"
	"errors"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

const maxPlanSize = 1 * 1024 * 1024 // 1MB

// Aggregator collects streaming response chunks into complete plan text.
//
// Aggregator is responsible for aggregating streamed plan output from a channel of provider.StreamChunk
// into a single string. It enforces a maximum plan size, propagates stream errors, and respects context cancellation.
type Aggregator struct{}

// Aggregate collects all stream chunks into a single complete string.
//
// Expected:
//   - ctx is a valid context (cancellation is respected)
//   - chunks is a readable channel of StreamChunk values
//
// Returns:
//   - The complete aggregated text from all chunks.
//   - An error if the stream is empty, exceeds 1MB, is cancelled, or contains an error chunk.
//
// Side effects:
//   - None.
func (a *Aggregator) Aggregate(ctx context.Context, chunks <-chan provider.StreamChunk) (string, error) {
	var builder strings.Builder
	received := false

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				if !received {
					return "", errors.New("empty stream: no content received")
				}
				return builder.String(), nil
			}
			if chunk.Error != nil {
				return "", chunk.Error
			}
			if chunk.Content != "" {
				received = true
				builder.WriteString(chunk.Content)
				if builder.Len() > maxPlanSize {
					return "", errors.New("plan exceeds maximum size of 1MB")
				}
			}
		}
	}
}
