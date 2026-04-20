package tui

import (
	"github.com/baphled/flowstate/internal/plugin/eventbus"
)

// PublishResumedEvent exposes the internal publishResumedEvent function for tests.
func PublishResumedEvent(bus *eventbus.EventBus, sessionID string) {
	publishResumedEvent(bus, sessionID)
}

// PersistRootSessionMetadata exposes the internal
// persistRootSessionMetadata helper so the sidecar-on-disk contract
// can be pinned without booting a full Bubble Tea program. See the
// helper's own doc for the full contract.
func PersistRootSessionMetadata(sessionsDir, sessionID, agentID string) {
	persistRootSessionMetadata(sessionsDir, sessionID, agentID)
}
