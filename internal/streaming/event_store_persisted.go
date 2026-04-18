package streaming

import "log/slog"

// AppendFunc is the disk-write side of the persistedSwarmStore decorator.
// Implementations typically wrap AppendSwarmEvent with a bound session path
// so the decorator can push a single event to its session's JSONL file
// without knowing the filesystem layout.
type AppendFunc func(ev SwarmEvent) error

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
