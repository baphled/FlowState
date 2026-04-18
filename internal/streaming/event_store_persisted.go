package streaming

import "log/slog"

// AppendFunc is the disk-write side of the persistedSwarmStore decorator.
// Implementations typically wrap AppendSwarmEvent with a bound session path
// so the decorator can push a single event to its session's JSONL file
// without knowing the filesystem layout.
type AppendFunc func(ev SwarmEvent) error

// EventRestorer is the restore-mode contract for a SwarmEventStore. Stores
// that satisfy this interface accept a bulk population of previously
// persisted events WITHOUT firing any disk-write hooks.
//
// The distinction matters on session switch (P5/B2): the chat intent loads
// session B's JSONL events from disk, Clears the in-memory store, then
// repopulates it with the loaded events. Routing those events through the
// normal Append path would re-fire the WAL AppendFunc and double the
// on-disk file on every switch. RestoreEvents is the explicit non-WAL entry
// point for this case.
//
// Implementations must:
//   - Be safe for concurrent call with other store methods.
//   - Preserve insertion order and apply capacity-aware eviction identically
//     to the equivalent stream of Appends.
//   - Tolerate nil or empty slices as a no-op (not an error).
type EventRestorer interface {
	// RestoreEvents populates the store from a previously persisted slice
	// without invoking any disk-write hooks on the store.
	RestoreEvents(events []SwarmEvent)
}

// persistedSwarmStore decorates an underlying SwarmEventStore with a
// write-through hook that forwards every Append to disk. The design keeps
// MemorySwarmStore untouched and opts in to persistence per-session at
// construction time: sessions with a SwarmEventPersister get the wrapped
// store, sessions without one keep the plain in-memory store.
//
// Thread safety: the wrapped store's mutex guards concurrent All()/Append
// interleavings. The decorator invokes the AppendFunc outside that mutex
// (after the inner Append returns) so a slow disk write cannot block other
// appenders. Persistence errors are logged via slog.Error and swallowed —
// the stream path must never stall on disk I/O (P4 contract).
type persistedSwarmStore struct {
	inner  SwarmEventStore
	append AppendFunc
}

// NewPersistedSwarmStore wraps an existing SwarmEventStore so every Append
// is also written to disk via the supplied AppendFunc. A nil AppendFunc
// degrades gracefully to pure in-memory behaviour; this is intentional so
// callers constructed before a session path is known can later upgrade
// without a type change.
//
// Expected:
//   - inner is a non-nil SwarmEventStore (typically a *MemorySwarmStore).
//   - appendFn may be nil (persistence disabled) or a func that writes one
//     event to disk and returns an error on failure.
//
// Returns:
//   - A SwarmEventStore that delegates reads to inner and writes
//     through to appendFn on every Append.
//
// Side effects:
//   - None until Append/Clear/All is invoked on the returned store.
func NewPersistedSwarmStore(inner SwarmEventStore, appendFn AppendFunc) SwarmEventStore {
	return &persistedSwarmStore{
		inner:  inner,
		append: appendFn,
	}
}

// Append records ev in the underlying store and then forwards it to the
// persistence hook. Persistence errors are logged but never returned —
// callers on the streaming hot path cannot react to disk failures without
// blocking the UI loop, so the in-memory view remains the source of truth
// for the live session. The WAL may diverge from memory on persistent disk
// errors; compaction on session close corrects this.
//
// Expected:
//   - ev is a populated SwarmEvent.
//
// Side effects:
//   - Mutates the underlying store.
//   - Writes one JSONL line to disk via the configured AppendFunc.
func (s *persistedSwarmStore) Append(ev SwarmEvent) {
	s.inner.Append(ev)
	if s.append == nil {
		return
	}
	if err := s.append(ev); err != nil {
		slog.Error("swarm event persistence: append to disk failed",
			"event_id", ev.ID,
			"event_type", ev.Type,
			"error", err,
		)
	}
}

// All delegates to the underlying store and returns its defensive copy.
//
// Returns:
//   - A slice owned by the caller (the wrapped store allocates fresh).
//
// Side effects:
//   - None.
func (s *persistedSwarmStore) All() []SwarmEvent {
	return s.inner.All()
}

// Clear delegates to the underlying store. Compaction of the on-disk file
// is the caller's responsibility: persistedSwarmStore intentionally does
// not truncate the JSONL file here because Clear is also invoked for test
// isolation and pre-restore resets where the disk file must survive.
//
// Side effects:
//   - Mutates the underlying store.
func (s *persistedSwarmStore) Clear() {
	s.inner.Clear()
}

// RestoreEvents populates the inner store from a previously persisted slice
// without invoking the disk AppendFunc. This is the non-WAL entry point used
// by session switch (P5/B2): the caller has already read the events from
// disk, so re-firing the WAL would double the on-disk file on every restore.
//
// If the inner store also satisfies EventRestorer (the default
// MemorySwarmStore does), the call is forwarded directly. Otherwise the
// decorator falls back to the underlying Append path — which for a store
// with no AppendFunc is still safe, but callers wrapping a non-restorable
// store in a persistedSwarmStore risk double-writes and should audit the
// wrapping order.
//
// Expected:
//   - events is any slice (including nil) of previously persisted entries.
//
// Side effects:
//   - Mutates the inner store. Never invokes s.append.
func (s *persistedSwarmStore) RestoreEvents(events []SwarmEvent) {
	if len(events) == 0 {
		return
	}
	if restorer, ok := s.inner.(EventRestorer); ok {
		restorer.RestoreEvents(events)
		return
	}
	// Fallback: the inner store does not expose a restore path. Route
	// through Append on the inner store directly — NOT through s.Append —
	// so the AppendFunc on the decorator does not fire. This preserves
	// the restore contract for any SwarmEventStore implementation.
	for idx := range events {
		s.inner.Append(events[idx])
	}
}
