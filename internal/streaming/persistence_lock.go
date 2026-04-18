package streaming

import "sync"

// sessionMutex is a path-keyed mutex registry. Both AppendSwarmEvent and
// CompactSwarmEvents acquire the mutex for a given session's events file
// before touching the filesystem so that a compact-time atomic rename cannot
// land between an appender's OpenFile and its Write.
//
// The map entries are never deleted — the working set is bounded by the
// number of distinct session event files ever touched in a single process,
// which in practice is small (tens) and would require a long-lived daemon to
// matter. If that assumption changes, add reference counting to the entries
// and remove them when their count drops to zero.
type sessionMutex struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// sessionLocks is the package-level singleton used by AppendSwarmEvent and
// CompactSwarmEvents. Tests can observe it via LockPathForTest.
var sessionLocks = &sessionMutex{locks: make(map[string]*sync.Mutex)}

// Lock acquires the per-path mutex and returns a closure that releases it.
// Callers are expected to defer the returned closure.
//
// Expected:
//   - path is a non-empty identifier. Callers use the absolute events file
//     path so two writers to the same session serialise even when they
//     computed the path independently.
//
// Returns:
//   - A closure that releases the lock. Safe to call exactly once.
//
// Side effects:
//   - Blocks the caller until the lock is available. Allocates a sync.Mutex
//     on first access for a path.
func (s *sessionMutex) Lock(path string) func() {
	s.mu.Lock()
	m, ok := s.locks[path]
	if !ok {
		m = &sync.Mutex{}
		s.locks[path] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// LockPathForTest exposes the per-path lock to tests so they can assert on
// serialisation properties without reaching into the package. Production
// code must not call this helper — use it only to mirror what
// AppendSwarmEvent and CompactSwarmEvents do internally.
//
// Expected:
//   - path is the same path the production code uses for the session.
//
// Returns:
//   - A closure that releases the lock.
//
// Side effects:
//   - Acquires the per-path mutex.
func LockPathForTest(path string) func() {
	return sessionLocks.Lock(path)
}
