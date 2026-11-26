package app

// The turn.after point of ADR-012 packet 4a2. It fires in every exec loop (a root run, a delegated child and
// a swarm coordinator) after each provider turn and its tool results, the final no-call turn included, and
// before the turn-limit check. A hook may stop the run; nothing else it returns changes the loop.

import (
	"context"
	"fmt"
)

// fireTurnAfter runs the turn.after hooks of the loop's set in file order and returns ErrHookStop, naming the
// hook, for the first hook that asked the run to stop; later hooks do not run. A deny or a failure is
// recorded and the chain goes on (turn.after has nothing to deny). A run whose context has ended fires
// nothing: its cancellation is classified by the caller. calls is the number of tool calls of the turn.
func fireTurnAfter(ctx context.Context, cfg *ExecLoopConfig, res *ExecLoopResult, calls int) error {
	var hooks []Hook
	for _, h := range hookSetHooks(cfg.Hooks) {
		if h.Point == HookTurnAfter {
			hooks = append(hooks, h)
		}
	}
	if len(hooks) == 0 || ctx.Err() != nil {
		return nil
	}
	w, err := cfg.Store.NewEventWriter(ctx, cfg.Session.ID)
	if err != nil {
		return fmt.Errorf("turn.after writer: %w", err)
	}
	defer func() { _ = w.Close() }()
	g := &hookGate{hooks: hooks, events: w, input: loopHookInput(*cfg, "")}
	stdin, ierr := g.pointDocument(HookTurnAfter, map[string]any{
		"turn": map[string]any{"index": res.Turns, "tool_calls": calls, "final": calls == 0}})
	for _, h := range hooks {
		step, err := g.invoke(ctx, h, stdin, ierr, nil)
		if err != nil {
			return err
		}
		if step.stop {
			return fmt.Errorf("%w: the turn.after hook %s stopped the run", ErrHookStop, h.ID)
		}
	}
	return nil
}
