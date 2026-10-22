package lifecycle

// TurnLifecycle holds the six outer stages of the agent turn lifecycle in
// execution order. The struct field order IS the execution order:
//
//	ContextAssembly → PreStream → Stream → PreToolExec → ToolExec → PostProcess
//
// This struct is the single source of truth for stage ordering. There is no
// separate ordering configuration that can drift from this definition.
//
// Slot phases (PreStream, PreToolExec) are first-class v1 citizens. They
// default to pass-through (empty hook chains) and are activated when a user
// registers hooks. They cannot be removed from the struct — a user who does
// not need them simply leaves their hook chains empty.
type TurnLifecycle struct {
	ContextAssembly LifecycleStage[ContextAssemblyCtx]
	PreStream       LifecycleStage[PreStreamCtx]
	Stream          LifecycleStage[StreamCtx]
	PreToolExec     LifecycleStage[PreToolExecCtx]
	ToolExec        LifecycleStage[ToolExecCtx]
	PostProcess     LifecycleStage[PostProcessCtx]
}

// AddContextAssemblyHook appends a hook to the ContextAssembly stage chain.
//
// Expected: parameters for AddContextAssemblyHook.
// Returns: result of AddContextAssemblyHook.
// Side effects: None.
func (tl *TurnLifecycle) AddContextAssemblyHook(h StageHook[ContextAssemblyCtx]) {
	tl.ContextAssembly.Hooks = append(tl.ContextAssembly.Hooks, h)
}

// AddPreStreamHook appends a hook to the PreStream stage chain.
//
// Expected: parameters for AddPreStreamHook.
// Returns: result of AddPreStreamHook.
// Side effects: None.
func (tl *TurnLifecycle) AddPreStreamHook(h StageHook[PreStreamCtx]) {
	tl.PreStream.Hooks = append(tl.PreStream.Hooks, h)
}

// AddStreamHook appends a hook to the Stream stage chain.
//
// Expected: parameters for AddStreamHook.
// Returns: result of AddStreamHook.
// Side effects: None.
func (tl *TurnLifecycle) AddStreamHook(h StageHook[StreamCtx]) {
	tl.Stream.Hooks = append(tl.Stream.Hooks, h)
}

// AddPreToolExecHook appends a hook to the PreToolExec stage chain.
//
// Expected: parameters for AddPreToolExecHook.
// Returns: result of AddPreToolExecHook.
// Side effects: None.
func (tl *TurnLifecycle) AddPreToolExecHook(h StageHook[PreToolExecCtx]) {
	tl.PreToolExec.Hooks = append(tl.PreToolExec.Hooks, h)
}

// AddToolExecHook appends a hook to the ToolExec stage chain.
//
// Expected: parameters for AddToolExecHook.
// Returns: result of AddToolExecHook.
// Side effects: None.
func (tl *TurnLifecycle) AddToolExecHook(h StageHook[ToolExecCtx]) {
	tl.ToolExec.Hooks = append(tl.ToolExec.Hooks, h)
}

// AddPostProcessHook appends a hook to the PostProcess stage chain.
//
// Expected: parameters for AddPostProcessHook.
// Returns: result of AddPostProcessHook.
// Side effects: None.
func (tl *TurnLifecycle) AddPostProcessHook(h StageHook[PostProcessCtx]) {
	tl.PostProcess.Hooks = append(tl.PostProcess.Hooks, h)
}
