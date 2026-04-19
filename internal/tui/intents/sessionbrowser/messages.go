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

// SessionForkedMsg is sent after the browser attempts to fork a session
// via the configured Forker. Success carries NewSessionID so the parent
// intent (typically chat) can switch to the newly-forked session without
// a second load hop. Failure surfaces via Err; NewSessionID will be empty
// in that case and the parent should render a toast rather than switching.
//
// Fields:
//   - OriginID: the session the user forked from.
//   - NewSessionID: the fresh session ID produced by Forker.Fork.
//   - PivotMessageID: the message ID at which the fork was anchored.
//     Empty when the first-cut fork-at-last semantics were used.
//   - Err: non-nil when the store reported a failure.
type SessionForkedMsg struct {
	OriginID       string
	NewSessionID   string
	PivotMessageID string
	Err            error
}
