package app

import "github.com/baphled/flowstate/internal/provider"

// historyRebuild is the working state of rebuildHistory: the messages so far,
// the index of the assistant message the next correlated tool.call joins (-1
// when none), and the ids that have a persisted tool.result.
type historyRebuild struct {
	messages  []provider.Message
	turn      int
	answered  map[string]bool
	firstTask string // the task of the first turn.start, which Turn echoes as a user.message
	hasTask   bool
	bare      bool // the open assistant message was made for a call with no assistant.message
}
