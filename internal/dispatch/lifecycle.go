package dispatch

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrShutdown reports that a dispatch was attempted after
// Dispatcher.Shutdown. Surfaced so HTTP handlers can map the condition
// to a 503 rather than accepting work a quitting process will never
// finish.
var ErrShutdown = errors.New("dispatch: dispatcher shut down")

// ctxSource is a cancellable base context source owned by the
// dispatcher.
type ctxSource struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// newCtxSource builds a cancellable context source from an optional
// parent, falling back to context.Background when parent is nil.
//
// Expected:
//   - parent may be nil.
//
// Returns:
//   - A *ctxSource with a live context and cancel function.
//
// Side effects:
//   - None.
func newCtxSource(parent context.Context) *ctxSource { //nolint:contextcheck // dispatcher base context is deliberately owned here, not inherited from a request context; Shutdown cancels it to kill all streams
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &ctxSource{ctx: ctx, cancel: cancel}
}

// DispatcherLifecycle owns the dispatcher's shutdown surface for the
// quit-means-quit contract: the dispatcher derives its WithoutCancel
// stream contexts from a dispatcher-owned base context so that
// cancelling the base (via Shutdown) terminates every in-flight
// stream — including child/subagent streams — without re-coupling
// streams to each HTTP caller's r.Context().
type DispatcherLifecycle struct {
	base     *ctxSource
	stopOnce sync.Once
	stopped  bool
	stopMu   sync.Mutex
}

// newDispatcherLifecycle creates a lifecycle whose base context
// derives from parent (or context.Background when nil).
//
// Expected:
//   - parent may be nil; Background is used in that case.
//
// Returns:
//   - A ready *DispatcherLifecycle.
//
// Side effects:
//   - None.
func newDispatcherLifecycle(parent context.Context) *DispatcherLifecycle {
	return &DispatcherLifecycle{base: newCtxSource(parent)}
}

// mergedCtx carries VALUES (and deadline) from the caller-derived
// context while taking CANCELLATION exclusively from the dispatcher's
// base context. This is how "decoupled from the HTTP caller, but not
// from quit" is expressed in one type.
type mergedCtx struct {
	caller context.Context // already WithoutCancel-wrapped
	base   context.Context
}

// Deadline reports the deadline from the caller-derived context.
//
// Expected:
//   - None.
//
// Returns:
//   - The caller context's deadline, if any.
//
// Side effects:
//   - None.
func (m *mergedCtx) Deadline() (time.Time, bool) { return m.caller.Deadline() }

// Done reports cancellation from the dispatcher's base context only.
//
// Returns:
//   - The base context's Done channel.
//
// Side effects:
//   - None.
func (m *mergedCtx) Done() <-chan struct{} { return m.base.Done() }

// Err reports the base context's error.
//
// Returns:
//   - The base context's Err.
//
// Side effects:
//   - None.
func (m *mergedCtx) Err() error { return m.base.Err() }

// Value looks up values from the caller-derived context.
//
// Expected:
//   - key is the context key to look up.
//
// Returns:
//   - The value for key, if any.
//
// Side effects:
//   - None.
func (m *mergedCtx) Value(key any) any { return m.caller.Value(key) }

// deriveStreamCtx produces the streamer context for a dispatch:
//   - the caller's ctx cancellation is stripped (WithoutCancel), so an
//     HTTP handler returning early no longer kills the stream;
//   - the dispatcher's base cancellation still propagates, so
//     Shutdown terminates every live stream (quit-means-quit).
//
// Expected:
//   - caller is a non-nil context (typically the request context).
//
// Returns:
//   - A context whose cancellation follows the dispatcher base only.
//
// Side effects:
//   - None.
func (l *DispatcherLifecycle) deriveStreamCtx(caller context.Context) context.Context {
	stripped := context.WithoutCancel(caller)
	return &mergedCtx{caller: stripped, base: l.base.ctx}
}

// Stop cancels the dispatcher-owned base context and marks the
// dispatcher stopped. Idempotent; safe for concurrent use.
//
// Side effects:
//   - Cancels the base context; all derived stream contexts fire.
func (l *DispatcherLifecycle) Stop() {
	l.stopOnce.Do(func() {
		l.stopMu.Lock()
		l.stopped = true
		l.stopMu.Unlock()
		l.base.cancel()
	})
}

// isStopped reports whether Stop has fired.
//
// Returns:
//   - True once Stop has been called at least once.
//
// Side effects:
//   - None.
func (l *DispatcherLifecycle) isStopped() bool {
	l.stopMu.Lock()
	defer l.stopMu.Unlock()
	return l.stopped
}
