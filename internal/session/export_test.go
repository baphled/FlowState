package session

import (
	"errors"
	"time"
)

// ErrPersistSweeperStarted reports that SetPersistDebounceForTest was
// called after the debounce sweeper goroutine had already started, so
// the new interval could not take effect.
var ErrPersistSweeperStarted = errors.New("persist sweeper already started")

// ExtractPrimaryArgForTest exposes the shared tool display logic for external test assertions.
func ExtractPrimaryArgForTest(name string, args map[string]any) string {
	return toolArgValue(name, args)
}

// SetPersistFnForTest replaces the persistence implementation used by persistLocked
// so that tests can inject a slow or blocking persister to exercise lock-hold behaviour.
// Pass nil to restore the default (PersistSession).
func (m *Manager) SetPersistFnForTest(fn func(dir string, sess *Session) error) {
	m.persistFn = fn
}

// AppendSessionMessageForTest exposes the unexported appendSessionMessage hot path
// so that in-repo benchmarks (see manager_bench_test.go) can drive it directly.
func (m *Manager) AppendSessionMessageForTest(sessionID string, msg Message) {
	m.appendSessionMessage(sessionID, msg)
}

// MetaFileSuffixForTest exposes the persistence sidecar suffix so benchmarks
// can assert the baseline sidecar exists before measuring.
const MetaFileSuffixForTest = metaFileSuffix

// SetPersistDebounceForTest overrides the debounce-flusher tick interval so
// tests can exercise the time-driven sweeper without sleeping against the
// production PersistDebounceInterval. Must be called before the first
// mutation that marks a session dirty (the sweeper starts lazily on first
// dirty-mark and captures the interval at start).
func (m *Manager) SetPersistDebounceForTest(d time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.persistSweepStarted {
		return ErrPersistSweeperStarted
	}
	m.persistDebounce = d
	return nil
}
