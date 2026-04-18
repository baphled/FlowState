package sessionbrowser

import (
	"github.com/baphled/flowstate/internal/recall"
)

// SessionSelectedMsg is sent when a session is selected from the browser.
type SessionSelectedMsg struct {
	SessionID string
	IsNew     bool
}

// SessionLoadedMsg is sent when a session has been loaded from disk.
type SessionLoadedMsg struct {
	SessionID string
	Store     *recall.FileContextStore
	Err       error
}

// SessionDeletedMsg is sent after the browser attempts to delete a session
// via the configured Deleter. A non-nil Err indicates the deletion failed;
// the session remains in the in-memory list so the user can retry.
type SessionDeletedMsg struct {
	SessionID string
	Err       error
}
