package tui

import (
	"github.com/baphled/flowstate/internal/plugin/eventbus"
)

// PublishResumedEvent exposes the internal publishResumedEvent function for tests.
func PublishResumedEvent(bus *eventbus.EventBus, sessionID string) {
	publishResumedEvent(bus, sessionID)
}
