package lifecycle

import (
	"github.com/baphled/flowstate/internal/provider"
)

// StreamCtx carries the data for the Stream stage.
// This stage calls the provider and streams the response back.
type StreamCtx struct {
	Provider    provider.Provider
	AgentID     string
	Messages    []provider.Message
	ToolSchemas []provider.Tool
	ResultCh    chan<- provider.StreamChunk
}

// DefaultStream returns the default Stream lifecycle stage.
//
// The Stream stage pre-registers cross-cutting hooks such as logging,
// timing, and failover. Custom pipelines can reorder or replace these.
//
// Returns: result of DefaultStream.
// Side effects: None.
func DefaultStream() LifecycleStage[StreamCtx] {
	return LifecycleStage[StreamCtx]{
		Name:    "stream",
		Handler: passThroughHandler[StreamCtx],
	}
}
