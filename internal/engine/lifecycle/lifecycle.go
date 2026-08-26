package lifecycle

import "errors"

// ErrTurnRejected is returned by PreStream hooks to signal that the turn
// should be rejected without calling the provider.
var ErrTurnRejected = errors.New("turn rejected by pre_stream gate")

// StageHook is middleware for a stage-specific context type C.
//
// A hook receives the stage context and the next handler in the chain. It
// may inspect or modify the context before calling next, or short-circuit
// by returning an error without calling next. Hooks return the (potentially
// modified) context so that value-type context mutations propagate through
// the chain.
type StageHook[C any] func(ctx C, next func(C) (C, error)) (C, error)

// StageHandler is the core logic of a lifecycle stage.
//
// Handlers return the (potentially modified) context alongside any error.
// This allows value-type stage contexts (e.g. ToolExecCtx) to propagate
// mutations — such as setting Result — back to the caller.
type StageHandler[C any] func(ctx C) (C, error)

// StageHookChain composes hooks in registration order.
//
// The zero value (nil) is a usable empty chain that immediately delegates
// to the handler.
type StageHookChain[C any] []StageHook[C]

// Then wraps the given handler with all hooks in the chain.
//
// Hooks are applied inside-out: the first hook in the chain wraps the
// outermost layer, calling the second hook, which calls the third, and so
// on until the handler itself. This matches the standard middleware
// pattern where registration order = execution order for before-hooks and
// reverse order for after-hooks.
//
// A nil or empty chain returns the handler unchanged.
//
// Expected: parameters for Then.
// Returns: result of Then.
// Side effects: None.
func (c StageHookChain[C]) Then(handler StageHandler[C]) StageHandler[C] {
	if len(c) == 0 {
		return handler
	}
	wrapped := handler
	for i := len(c) - 1; i >= 0; i-- {
		idx := i
		nextFn := wrapped
		wrapped = func(ctx C) (C, error) {
			return c[idx](ctx, nextFn)
		}
	}
	return wrapped
}

// LifecycleStage defines a single lifecycle stage with hooks and a core handler.
//
// Type parameter C is the stage-specific context type that carries the data
// the stage operates on. Each stage defines its own context struct.
type LifecycleStage[C any] struct {
	// Name is a human-readable identifier for the stage, used in logging
	// and event emission.
	Name string

	// Hooks is the chain of middleware that wraps the core handler.
	// For slot phases, this chain is typically empty (pass-through).
	Hooks StageHookChain[C]

	// Handler is the core logic of the stage. It is wrapped by Hooks
	// via StageHookChain.Then() before execution.
	Handler StageHandler[C]
}

// Execute runs the stage by wrapping the handler with hooks and calling it.
//
// The returned context C carries any mutations the handler (or hooks)
// applied. For value-type stage contexts (e.g. ToolExecCtx with a Result
// field) callers MUST capture the return value to observe those mutations:
//
//	ctx, err := stage.Execute(ctx)
//	if ctx.Result != nil { ... }
//
// Expected: parameters for Execute.
// Returns: result of Execute.
// Side effects: None.
func (s LifecycleStage[C]) Execute(ctx C) (C, error) {
	return s.Hooks.Then(s.Handler)(ctx)
}

// passThroughHandler is the default handler for slot phase stages.
// It performs no work; the slot is activated entirely through its hooks.
//
// Expected: ctx is the generic phase payload.
//
// Returns: the unmodified context and nil error.
//
// Side effects: None.
func passThroughHandler[C any](ctx C) (C, error) {
	return ctx, nil
}
