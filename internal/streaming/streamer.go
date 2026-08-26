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

// HistorySeeder is an optional extension of Streamer. When a Streamer also
// implements HistorySeeder, the session manager calls SeedHistory before the
// first Stream call for a session so that the engine's in-memory context store
// is pre-populated with prior conversation turns after a server restart.
type HistorySeeder interface {
	// SeedHistory prepopulates the engine store with historical messages for
	// sessionID. Called with messages[:len-1] — the current user turn is
	// excluded because the engine appends it itself during Stream.
	SeedHistory(sessionID string, messages []provider.Message)
}
