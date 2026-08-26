package external

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/provider"
)

// HookProvider is implemented by plugins that expose hooks for lifecycle events.
type HookProvider interface {
	// Hooks returns the hook map keyed by HookType.
	Hooks() map[plugin.HookType]interface{}
}

// ToolExecArgs wraps the parameters for tool execution hooks.
type ToolExecArgs struct {
	Name string
	Args map[string]any
}

// Dispatcher routes hook invocations to all registered plugins supporting the given hook type.
type Dispatcher struct {
	registry *plugin.Registry
}

// NewDispatcher creates a Dispatcher backed by the given plugin registry.
//
// Expected: registry must be a valid plugin registry.
//
// Returns: a new Dispatcher instance.
//
// Side effects: None.
func NewDispatcher(registry *plugin.Registry) *Dispatcher {
	return &Dispatcher{registry: registry}
}

// Dispatch invokes the specified hook on all registered plugins that support it.
//
// Plugins are called in registration order. An error from one plugin is logged
// and collected but does not prevent dispatch to remaining plugins. Returns a
// combined error of all failures, or nil when every invocation succeeds.
//
// Expected:
//   - ctx: context for hook execution
//   - hookType: the type of hook to dispatch
//   - payload: hook-specific payload
//
// Returns: combined error of all failures, or nil when every invocation succeeds.
//
// Side effects: May call external plugins via RPC.
func (d *Dispatcher) Dispatch(ctx context.Context, hookType plugin.HookType, payload interface{}) error {
	var errs []error
	for _, p := range d.registry.List() {
		hp, ok := p.(HookProvider)
		if !ok {
			continue
		}
		hookFn, exists := hp.Hooks()[hookType]
		if !exists {
			continue
		}
		if err := callHook(ctx, hookType, hookFn, payload); err != nil {
			slog.Warn("plugin hook error", "plugin", p.Name(), "hook", string(hookType), "error", err)
			errs = append(errs, fmt.Errorf("plugin %s: %w", p.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// callHook dispatches a hook invocation to the appropriate handler based on type.
//
// Expected:
//   - ctx: context for hook execution
//   - hookType: the type of hook
//   - hookFn: the hook function to invoke
//   - payload: hook-specific payload
//
// Returns: error from hook execution, or nil on success.
//
// Side effects: May call external plugins via RPC.
func callHook(ctx context.Context, hookType plugin.HookType, hookFn interface{}, payload interface{}) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("hook function panic: %v", r)
		}
	}()

	switch fn := hookFn.(type) {
	case *JSONRPCClient:
		_, err := fn.Call(ctx, "hooks/dispatch", payload)
		return err
	case plugin.ChatParamsHook:
		req, ok := payload.(*provider.ChatRequest)
		if !ok {
			return fmt.Errorf("invalid payload for %s hook: expected *provider.ChatRequest, got %T", hookType, payload)
		}
		return fn(ctx, req)
	case plugin.EventHook:
		evt, ok := payload.(plugin.Event)
		if !ok {
			return fmt.Errorf("invalid payload for %s hook: expected plugin.Event, got %T", hookType, payload)
		}
		return fn(ctx, evt)
	case plugin.ToolExecHook:
		args, ok := payload.(*ToolExecArgs)
		if !ok {
			return fmt.Errorf("invalid payload for %s hook: expected *ToolExecArgs, got %T", hookType, payload)
		}
		return fn(ctx, args.Name, args.Args)
	default:
		return fmt.Errorf("unsupported hook function type: %T", hookFn)
	}
}
