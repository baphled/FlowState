package chat

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
)

// Streamer produces streaming response chunks.
//
// Satisfied by *engine.Engine and *streaming.HarnessStreamer.
type Streamer interface {
	// Stream returns a channel of response chunks for the given agent and message.
	Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error)
}
