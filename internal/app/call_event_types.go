package app

import (
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// callEventSink is the app-owned per-call EventSink adapter. It binds
// one immutable provider ToolCall identity (ID, name, actor and
// compacted argument tuple) to the tool.call/tool.result events the
// tool.Loop emits, annotating them with the provider call ID so the
// bounded-history projection can correlate them later. The caller's
// argument payload is never mutated; the stored args are a normalised
// copy compared byte-for-byte against the bound call.
//
// The adapter is single-use for one call lifecycle (start then at most
// one result) and is not safe for concurrent use; the underlying
// writer serialises persistence per session.
type callEventSink struct {
	writer  tool.EventSink
	call    provider.ToolCall
	args    string
	sticky  error
	started bool
	done    bool
}
