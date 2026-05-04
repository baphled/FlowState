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
