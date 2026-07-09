package lifecycle

import "github.com/baphled/flowstate/internal/provider"

// PostProcessCtx carries session-level data for the PostProcess stage.
// This stage runs after the stream and tool loop complete, handling
// knowledge extraction, learning, and analytics.
type PostProcessCtx struct {
	SessionID   string
	Messages    []provider.Message
	ToolsCalled int
}

// DefaultPostProcess returns the default PostProcess lifecycle stage.
//
// The PostProcess stage pre-registers hooks for knowledge extraction,
// learning pipeline triggers, and analytics emission.
func DefaultPostProcess() LifecycleStage[PostProcessCtx] {
	return LifecycleStage[PostProcessCtx]{
		Name:    "post_process",
		Handler: passThroughHandler[PostProcessCtx],
	}
}
