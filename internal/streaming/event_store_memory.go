package streaming

import "sync"

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
