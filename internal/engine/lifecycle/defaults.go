package lifecycle

// DefaultTurnLifecycle returns a TurnLifecycle with all six stages
// configured to their default state. Slot phases (PreStream, PreToolExec)
// start with empty hook chains and pass-through handlers; they are
// activated when hooks are registered.
//
// Callers override individual stages by replacing the LifecycleStage value
// on the returned struct. The field order is the execution order and must
// not be changed at runtime.
//
// Returns: result of DefaultTurnLifecycle.
// Side effects: None.
func DefaultTurnLifecycle() TurnLifecycle {
	return TurnLifecycle{
		ContextAssembly: LifecycleStage[ContextAssemblyCtx]{
			Name:    "context_assembly",
			Handler: passThroughHandler[ContextAssemblyCtx],
		},
		PreStream:   DefaultPreStream(),
		Stream:      DefaultStream(),
		PreToolExec: DefaultPreToolExec(),
		ToolExec:    DefaultToolExec(),
		PostProcess: DefaultPostProcess(),
	}
}
