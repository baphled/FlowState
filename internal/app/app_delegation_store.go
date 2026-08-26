package app

import (
	"path/filepath"

	"github.com/baphled/flowstate/internal/recall"
)

// delegateStoreFactory creates file-backed context stores for delegation sessions.
type delegateStoreFactory struct {
	sessionsDir string
}

// newDelegateStoreFactory returns a delegateStoreFactory that writes session files
// into the given directory.
//
// Expected:
//   - sessionsDir is a writable directory path for session JSON files.
//
// Returns:
//   - A configured delegateStoreFactory ready for use as a DelegateStoreFactory.
//
// Side effects:
//   - None. Directory creation is deferred to CreateSessionStore.
func newDelegateStoreFactory(sessionsDir string) *delegateStoreFactory {
	return &delegateStoreFactory{sessionsDir: sessionsDir}
}

// CreateSessionStore creates a file-backed context store for the given session ID.
//
// Expected:
//   - sessionID is a non-empty identifier for the delegation session.
//
// Returns:
//   - A FileContextStore persisting to <sessionsDir>/<sessionID>.json.
//   - An error if the directory cannot be created or the store cannot be initialised.
//
// Side effects:
//   - Creates the sessions directory if it does not exist.
func (f *delegateStoreFactory) CreateSessionStore(sessionID string) (*recall.FileContextStore, error) {
	path := filepath.Join(f.sessionsDir, sessionID+".json")
	return recall.NewFileContextStore(path, "")
}
