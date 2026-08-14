package lifecycle

import (
	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
)

// PreStreamCtx carries the boundary between ContextAssembly and Stream.
// A PreStream hook can inspect and modify the assembled messages before
// they are sent to the provider. Returning ErrTurnRejected signals that
// the gate has rejected the turn without calling the provider.
type PreStreamCtx struct {
	Messages    []provider.Message
	TokenBudget int
	Manifest    *agent.Manifest
}

// DefaultPreStream returns the default PreStream lifecycle stage.
//
// The slot phase has no pre-registered hooks and a pass-through handler.
// Hooks activate the slot by being added to the chain.
//
// Returns: result of DefaultPreStream.
// Side effects: None.
func DefaultPreStream() LifecycleStage[PreStreamCtx] {
	return LifecycleStage[PreStreamCtx]{
		Name:    "pre_stream",
		Handler: passThroughHandler[PreStreamCtx],
	}
}
