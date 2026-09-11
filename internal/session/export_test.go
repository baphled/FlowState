package session

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
