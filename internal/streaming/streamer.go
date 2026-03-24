package streaming

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
)

// Streamer is the producer interface for streaming AI responses.
type Streamer interface {
	// Stream returns a channel of response chunks for the given agent and message.
	Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error)
}
