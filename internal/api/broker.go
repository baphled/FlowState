package api

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// brokerSubscriberGracePeriod bounds how long a Publish call will wait on a
// single subscriber's full channel before recording a drop. The original
// implementation had a `default:` clause (zero grace) — a slow subscriber
// silently lost chunks. A small grace period absorbs micro-bursts in normal
// streaming load without letting a permanently-stuck subscriber starve its
// siblings (the brief's Drop #4 constraint).
const brokerSubscriberGracePeriod = 50 * time.Millisecond

// SessionBroker distributes live session events to registered subscribers.
//
// It maintains a map of session ID to subscriber channels. When events are
// published for a session, they are forwarded to all active subscribers.
//
// active tracks sessions that currently have an in-progress Publish call.
// Consumers call IsPublishing to detect whether a Publish is running before
// entering the blocking select loop, so they can fast-path a [DONE] when a
// late subscriber arrives after the stream has already completed.
//
// dropped counts chunks that were discarded because a subscriber's channel
// remained full past brokerSubscriberGracePeriod. Pre-Drop-#4 the broker
// had no way to surface this — the silent loss was structurally invisible
// to both frontend and server-side metrics. The atomic counter is exposed
// via DroppedCount() for tests, log lines, and Prometheus integration.
type SessionBroker struct {
	mu          sync.Mutex
	subscribers map[string][]chan provider.StreamChunk
	active      map[string]bool
	dropped     atomic.Uint64
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
		active:      make(map[string]bool),
	}
}

// IsPublishing reports whether a Publish call is currently in progress for
// the given session. Callers use this to distinguish two cases after
// subscribing:
//
//   - true  → chunks are flowing; the select loop will receive them.
//   - false → either Publish hasn't started yet (pending message, caller
//     should wait) or it finished before Subscribe was called (caller
//     should check session state and fast-path [DONE] if complete).
func (b *SessionBroker) IsPublishing(sessionID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.active[sessionID]
}

// DroppedCount returns the cumulative number of chunks the broker has
// discarded because at least one subscriber's channel stayed full past
// the per-subscriber grace period. The counter is process-lifetime
// monotonic — it is never reset.
//
// The intended consumers are tests pinning observability behaviour and a
// Prometheus collector exporting the value as a counter metric. Drop #4
// of the streaming signal-drop fix introduced this accessor; pre-fix the
// broker silently dropped chunks with no observable signal at all.
func (b *SessionBroker) DroppedCount() uint64 {
	return b.dropped.Load()
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
	b.mu.Lock()
	b.active[sessionID] = true
	b.mu.Unlock()

	for chunk := range chunks {
		b.mu.Lock()
		subs := make([]chan provider.StreamChunk, len(b.subscribers[sessionID]))
		copy(subs, b.subscribers[sessionID])
		b.mu.Unlock()

		for _, sub := range subs {
			b.deliverWithBackpressure(sessionID, sub, chunk)
		}
	}

	b.mu.Lock()
	delete(b.active, sessionID)
	subs := b.subscribers[sessionID]
	delete(b.subscribers, sessionID)
	b.mu.Unlock()

	for _, sub := range subs {
		close(sub)
	}
}

// deliverWithBackpressure attempts to send a chunk to a single subscriber
// channel under bounded backpressure. The original Publish loop used a
// non-blocking `select { case sub <- chunk: default: }` send — a slow
// subscriber's chunk was silently lost with zero observability. Drop #4
// replaces that with:
//
//   - A non-blocking fast path (the common case: subscriber is keeping
//     up with the producer).
//   - A short grace period blocking send for transient bursts (a
//     subscriber that's momentarily full but draining).
//   - A counted+logged drop if the channel is still full at the deadline.
//
// Crucially the grace period is per-subscriber, not per-broker — a
// permanently stuck subscriber waits at most brokerSubscriberGracePeriod
// before its chunk drops, so siblings of a stuck subscriber never starve.
//
// Expected:
//   - sessionID identifies the session for log attribution.
//   - sub is a non-nil subscriber channel.
//   - chunk is the StreamChunk to deliver.
//
// Side effects:
//   - May send chunk on sub.
//   - On deadline: increments b.dropped atomically and emits a slog
//     warning carrying sessionID and an EventType marker so the drop is
//     attributable in production logs.
func (b *SessionBroker) deliverWithBackpressure(sessionID string, sub chan provider.StreamChunk, chunk provider.StreamChunk) {
	// Fast path — the common case under normal load. Equivalent to the
	// original `default:` non-blocking send when capacity is available.
	select {
	case sub <- chunk:
		return
	default:
	}

	// Slow path — channel is momentarily full. Wait up to the grace
	// period for the subscriber to drain. Using time.NewTimer over
	// time.After lets us reclaim the timer to keep allocation pressure
	// down on a hot fan-out path.
	timer := time.NewTimer(brokerSubscriberGracePeriod)
	defer timer.Stop()
	select {
	case sub <- chunk:
		return
	case <-timer.C:
		b.dropped.Add(1)
		// Per-drop logging is intentional: the previous silent drop was
		// the structural-invisibility failure mode. A handful of dropped
		// chunks per session is tolerable noise; the alternative —
		// log nothing, count nothing — is the bug we're fixing. The
		// EventType marker `streaming.broker.drop` lets log scrapers
		// attribute and aggregate without parsing free-text.
		slog.Warn("session broker dropped chunk under sustained backpressure",
			"session_id", sessionID,
			"event_type", "streaming.broker.drop",
			"grace_period_ms", brokerSubscriberGracePeriod.Milliseconds(),
			"total_drops", b.dropped.Load(),
		)
	}
}
