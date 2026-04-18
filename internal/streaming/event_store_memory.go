package streaming

import (
	"maps"
	"sync"
)

// DefaultSwarmStoreCapacity is the fallback capacity applied when
// NewMemorySwarmStore is invoked with a non-positive capacity.
//
// 200 events is roughly 3 minutes of typical activity (~1 event/sec during
// active delegation). This matches the "what did my agents just do?" live-
// awareness contract without claiming scroll-back history — Wave 3 owns
// persistent event storage. The capacity exceeds the visible body line
// count on tall terminals (a 60-row terminal shows ~40 body lines in the
// activity pane) while keeping memory trivial: 200 × sizeof(SwarmEvent)
// is well under 80 KB per chat session. Raising the value is a behavioural
// change — update the matching test fixtures when it moves again.
const DefaultSwarmStoreCapacity = 200

// MemorySwarmStore is a thread-safe, fixed-capacity, oldest-first-eviction
// in-memory implementation of SwarmEventStore.
//
// The store is mutex-guarded rather than channel-based because activity
// events are latency-sensitive and arrive on many producer goroutines;
// serialising through a channel would risk dropping events or blocking
// streams. All methods are safe for concurrent invocation.
type MemorySwarmStore struct {
	mu       sync.Mutex
	events   []SwarmEvent
	capacity int
}

// NewMemorySwarmStore constructs a MemorySwarmStore with the given capacity.
//
// Expected:
//   - capacity is the maximum number of events the store retains; values <= 0
//     fall back to DefaultSwarmStoreCapacity.
//
// Returns:
//   - A ready-to-use *MemorySwarmStore.
//
// Side effects:
//   - None.
func NewMemorySwarmStore(capacity int) *MemorySwarmStore {
	if capacity <= 0 {
		capacity = DefaultSwarmStoreCapacity
	}
	return &MemorySwarmStore{
		events:   make([]SwarmEvent, 0, capacity),
		capacity: capacity,
	}
}

// Append adds ev to the store, evicting the oldest entry when the store is
// at capacity.
//
// Expected:
//   - ev is a populated SwarmEvent; the store does not validate fields.
//
// Side effects:
//   - Mutates the internal slice under the store mutex.
func (s *MemorySwarmStore) Append(ev SwarmEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) >= s.capacity {
		// Evict oldest: drop the first element, shifting the slice down.
		// Copy keeps the backing array bounded so it never grows without
		// bound even under millions of appends.
		copy(s.events, s.events[1:])
		s.events = s.events[:len(s.events)-1]
	}
	s.events = append(s.events, ev)
}

// All returns a defensive copy of the stored events, oldest first.
//
// Each returned SwarmEvent's Metadata map is shallow-cloned via maps.Clone
// so callers (the Ctrl+E details modal, future filter UIs) can mutate the
// top-level map without racing against concurrent Append calls. The values
// inside the map are not deep-copied — callers that mutate nested maps or
// slices share those with the store and must synchronise externally. In
// practice producers use string and primitive values so this is a
// sufficient defence.
//
// Returns:
//   - A freshly allocated slice the caller may freely mutate.
//
// Side effects:
//   - None.
func (s *MemorySwarmStore) All() []SwarmEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SwarmEvent, len(s.events))
	copy(out, s.events)
	for i := range out {
		if out[i].Metadata != nil {
			out[i].Metadata = maps.Clone(out[i].Metadata)
		}
	}
	return out
}

// Clear removes all events from the store.
//
// Side effects:
//   - Replaces the internal slice with an empty one under the store mutex.
func (s *MemorySwarmStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = s.events[:0]
}

// RestoreEvents populates the store from a previously persisted slice of
// events without invoking any disk-write side effects.
//
// Restore is semantically distinct from Append:
//   - Append routes through the full persistence chain (the persistedSwarmStore
//     decorator writes one JSONL line to the session's WAL per event).
//   - Restore is invoked after the caller has already read those JSONL lines
//     back from disk, so re-writing them would double the file on every
//     session switch and quickly corrupt long-lived sessions.
//
// Session-switch callers are expected to invoke Clear() first so restored
// events replace (rather than augment) the in-memory view. The events are
// inserted in order, with oldest-first eviction when the slice exceeds
// capacity — this mirrors the behaviour a stream of equivalent Appends would
// produce, so the visible slice after restore is deterministic.
//
// Expected:
//   - events is any slice (including nil) of previously persisted entries.
//
// Side effects:
//   - Mutates the internal slice under the store mutex. No disk I/O.
func (s *MemorySwarmStore) RestoreEvents(events []SwarmEvent) {
	if len(events) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for idx := range events {
		if len(s.events) >= s.capacity {
			copy(s.events, s.events[1:])
			s.events = s.events[:len(s.events)-1]
		}
		s.events = append(s.events, events[idx])
	}
}

// Capacity returns the configured capacity for test assertions.
//
// Returns:
//   - The maximum number of events the store retains.
//
// Side effects:
//   - None.
func (s *MemorySwarmStore) Capacity() int {
	return s.capacity
}
