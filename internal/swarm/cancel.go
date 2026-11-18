package swarm

import "fmt"

// NewCancelTree creates an empty cancellation set.
func NewCancelTree() *CancelTree {
	return &CancelTree{cancelled: map[string]bool{}}
}

// Cancel cancels session and every transitively linked child session
// under it in the admission log. It fails closed on an empty id and
// is otherwise idempotent: cancelling an already-cancelled session
// succeeds without repeating the cascade.
func (t *CancelTree) Cancel(sessionID string, links *AdmissionLog) error {
	if sessionID == "" {
		return fmt.Errorf("swarm: session id must not be empty")
	}
	if links == nil {
		return fmt.Errorf("swarm: admission log must not be nil")
	}
	if t.cancelled[sessionID] {
		return nil
	}
	t.cancelled[sessionID] = true
	for _, link := range links.Children(sessionID) {
		if err := t.Cancel(link.ChildSessionID, links); err != nil {
			return err
		}
	}
	return nil
}

// Cancelled reports whether session was cancelled.
func (t *CancelTree) Cancelled(sessionID string) bool {
	return t.cancelled[sessionID]
}
