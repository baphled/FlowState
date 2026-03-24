package sessionbrowser

import (
	contextpkg "github.com/baphled/flowstate/internal/context"
)

// SessionSelectedMsg is sent when a session is selected from the browser.
type SessionSelectedMsg struct {
	SessionID string
	IsNew     bool
}

// SessionLoadedMsg is sent when a session has been loaded from disk.
type SessionLoadedMsg struct {
	SessionID string
	Store     *contextpkg.FileContextStore
	Err       error
}
