package streaming

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
)

// SessionContextStreamer decorates a Streamer with session ID context injection.
//
// It reads the active session ID via a getter function on every Stream call and,
// when the ID is non-empty, stores it in the context under the supplied key before
// delegating to the inner streamer. Downstream tool handlers can then retrieve
// the session ID from the context without coupling to the session package.
type SessionContextStreamer struct {
	inner       Streamer
	sessionIDFn func() string
	contextKey  any
}

// NewSessionContextStreamer creates a SessionContextStreamer that injects the session ID
// into the streaming context before delegating to the inner streamer.
//
// Expected:
//   - inner is a non-nil Streamer for delegation.
//   - sessionIDFn returns the current session ID; it is called on every Stream invocation.
//   - contextKey is the context key under which the session ID is stored (typically session.IDKey{}).
//
// Returns:
//   - A configured SessionContextStreamer ready to decorate streaming calls.
//
// Side effects:
//   - None.
func NewSessionContextStreamer(inner Streamer, sessionIDFn func() string, contextKey any) *SessionContextStreamer {
	return &SessionContextStreamer{
		inner:       inner,
		sessionIDFn: sessionIDFn,
		contextKey:  contextKey,
	}
}

var _ Streamer = (*SessionContextStreamer)(nil)

// Stream reads the current session ID from the getter and, if non-empty, injects it into
// the context before delegating to the inner streamer.
//
// Expected:
//   - ctx is a valid context for the streaming operation.
//   - agentID identifies the agent to stream from.
//   - message is the user's input text.
//
// Returns:
//   - A channel of StreamChunk values containing the response from the inner streamer.
//   - An error if the inner streamer fails.
//
// Side effects:
//   - Adds the session ID to the context under the configured key when the getter returns
//     a non-empty string.
func (s *SessionContextStreamer) Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error) {
	if id := s.sessionIDFn(); id != "" {
		ctx = context.WithValue(ctx, s.contextKey, id)
	}
	return s.inner.Stream(ctx, agentID, message)
}
