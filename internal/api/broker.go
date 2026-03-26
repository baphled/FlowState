package api

import (
	"sync"

	"github.com/baphled/flowstate/internal/provider"
)

// SessionBroker distributes live session events to registered subscribers.
//
// It maintains a map of session ID to subscriber channels. When events are
// published for a session, they are forwarded to all active subscribers.
type SessionBroker struct {
	mu          sync.Mutex
	subscribers map[string][]chan provider.StreamChunk
}

// NewSessionBroker creates a new SessionBroker with an empty subscriber map.
//
// Returns:
//   - A ready-to-use SessionBroker.
//
// Side effects:
//   - None.
func NewSessionBroker() *SessionBroker {
	return &SessionBroker{
		subscribers: make(map[string][]chan provider.StreamChunk),
	}
}

// Subscribe registers a new subscriber for a session and returns a receive channel and an unsubscribe function.
//
// Expected:
//   - sessionID is a non-empty string identifying an existing session.
//
// Returns:
//   - A buffered channel that receives StreamChunk values as they are published.
//   - A function that removes this subscriber when called.
//
// Side effects:
//   - Adds the subscriber channel to the session's subscriber list.
func (b *SessionBroker) Subscribe(sessionID string) (<-chan provider.StreamChunk, func()) {
	ch := make(chan provider.StreamChunk, 64)
	b.mu.Lock()
	b.subscribers[sessionID] = append(b.subscribers[sessionID], ch)
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		subs := b.subscribers[sessionID]
		for i, sub := range subs {
			if sub == ch {
				b.subscribers[sessionID] = append(subs[:i], subs[i+1:]...)
				close(ch)
				break
			}
		}
	}

	return ch, unsubscribe
}

// Publish forwards all chunks from the given channel to all subscribers of a session.
//
// Expected:
//   - sessionID is a non-empty string.
//   - chunks is a readable channel of StreamChunk values that will be drained.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Reads all chunks from the source channel.
//   - Sends each chunk to every active subscriber channel for the session.
//   - Unsubscribes all subscribers and closes their channels when the source closes.
func (b *SessionBroker) Publish(sessionID string, chunks <-chan provider.StreamChunk) {
	for chunk := range chunks {
		b.mu.Lock()
		subs := make([]chan provider.StreamChunk, len(b.subscribers[sessionID]))
		copy(subs, b.subscribers[sessionID])
		b.mu.Unlock()

		for _, sub := range subs {
			select {
			case sub <- chunk:
			default:
			}
		}
	}

	b.mu.Lock()
	subs := b.subscribers[sessionID]
	delete(b.subscribers, sessionID)
	b.mu.Unlock()

	for _, sub := range subs {
		close(sub)
	}
}
