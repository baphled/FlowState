// Package sessionviewer provides a full-screen read-only intent for viewing a child session.
//
// This package wraps the SessionViewerModal from the chat views package, delegating
// all scroll logic and rendering to that component. It implements the tuiintents.Intent
// interface and handles keyboard navigation (scroll and Esc to return to parent).
package sessionviewer
