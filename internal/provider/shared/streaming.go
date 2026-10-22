package shared

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
)

// SendChunk sends chunk to ch, respecting ctx cancellation.
//
// Expected:
//   - ctx is a valid context.
//   - ch is an open, non-nil channel.
//   - chunk is the StreamChunk to send.
//
// Returns:
//   - true if the chunk was sent successfully.
//   - false if the context was cancelled (an error chunk is sent instead).
//
// Side effects:
//   - May send an error chunk if the context is cancelled.
func SendChunk(
	ctx context.Context,
	ch chan<- provider.StreamChunk,
	chunk provider.StreamChunk,
) bool {
	select {
	case <-ctx.Done():
		select {
		case ch <- provider.StreamChunk{Error: ctx.Err(), Done: true}:
		default:
		}
		return false
	default:
	}

	select {
	case ch <- chunk:
		return true
	case <-ctx.Done():
		select {
		case ch <- provider.StreamChunk{Error: ctx.Err(), Done: true}:
		default:
		}
		return false
	}
}
