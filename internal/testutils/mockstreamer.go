package testutils

import (
	"context"
	"sync"

	"github.com/baphled/flowstate/internal/provider"
)

// MockStreamer is a concurrency-safe streaming.Streamer double that replays
// a fixed set of chunks (or a fixed error) on every Stream call and records
// the agent ID and message of the most recent call.
type MockStreamer struct {
	// Chunks is replayed, in order, on each Stream call.
	Chunks []provider.StreamChunk
	// Err, when non-nil, is returned by Stream instead of a channel.
	Err error

	mu              sync.Mutex
	capturedAgentID string
	capturedMessage string
}

// LastAgentID reports the agent ID passed to the most recent Stream call.
//
// Returns:
//   - The most recently observed agent ID.
//
// Side effects:
//   - None.
func (m *MockStreamer) LastAgentID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.capturedAgentID
}

// LastMessage reports the message passed to the most recent Stream call.
//
// Returns:
//   - The most recently observed message.
//
// Side effects:
//   - None.
func (m *MockStreamer) LastMessage() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.capturedMessage
}

// Stream records the agent ID and message, then either returns the
// configured error or a closed-after-replay channel of Chunks.
//
// Expected:
//   - ctx: Caller context (ignored beyond signature conformance).
//   - agentID: The agent identifier to record.
//   - message: The message to record.
//
// Returns:
//   - A channel emitting Chunks in order then closing, or nil when Err is set.
//   - The configured Err, or nil.
//
// Side effects:
//   - Records agentID/message under lock.
func (m *MockStreamer) Stream(_ context.Context, agentID string, message string) (<-chan provider.StreamChunk, error) {
	m.mu.Lock()
	m.capturedAgentID = agentID
	m.capturedMessage = message
	m.mu.Unlock()
	if m.Err != nil {
		return nil, m.Err
	}
	ch := make(chan provider.StreamChunk, len(m.Chunks))
	for i := range m.Chunks {
		ch <- m.Chunks[i]
	}
	close(ch)
	return ch, nil
}
