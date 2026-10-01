// Package engine — the tool loop.
//
// This file holds streamWithToolLoop (repeat-fingerprint, iteration,
// duration, same-tool-pattern and rejection guards; todo and background
// task continuation machinery), tool-call deduplication and batch
// execution, stream chunk processing with the idle watchdog, retry
// plumbing and the delivery-failure fallback.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/bash"
	"github.com/baphled/flowstate/internal/tool/todo"
)

// batchDeadlineKilled reports whether any result in the just-executed
// batch was killed by the bash tool's own per-command deadline (as
// opposed to failing for a substantive reason). The tool-loop guards
// exempt such batches from the identical-call and same-tool-pattern
// counters: a deadline-killed command is legitimately retried by the
// model (often identically), and counting those retries as "stuck"
// trips ToolLoopExceeded on healthy sessions running long builds/tests.
// This mirrors the TimeoutOverrider exemption on the duration backstop.
//
// Returns: true if any result carries bash.ErrDeadlineExceeded.
// Expected: the executed batch's results slice.
// Side effects: None.
func batchDeadlineKilled(results []tool.Result) bool {
	for _, r := range results {
		if r.Error != nil && errors.Is(r.Error, bash.ErrDeadlineExceeded) {
			return true
		}
	}
	return false
}

// maxOverflowRetries bounds context-window overflow recovery to two
// retries so a turn makes at most three provider calls (the initial
// request plus two post-compaction retries) before surfacing the
// terminal ErrCompactionInsufficient error.
const maxOverflowRetries = 2

// toolLoopWallClockMultiplier scales the duration cap into an absolute
// wall-clock bound on a single tool-loop budget window. The elapsed
// backstop subtracts tool execution time (so slow legitimate tools do
// not consume the provider round-trip budget), which also excludes
// delegated child runs from ever tripping it; the wall-clock backstop
// multiplies the cap by this factor and counts everything, bounding
// delegated turns at twice the configured duration regardless of the
// exclusions.
const toolLoopWallClockMultiplier = 2

// contextReductionDelta is the minimum meaningful token-estimate drop a
// compacted window must achieve over the pre-compaction slice before the
// retry path accepts it. A compaction that reduces less than this is
// treated as no reduction and short-circuits to the terminal error.
const contextReductionDelta = 64

// contextEstimateDropped reports whether the rebuilt window's estimated
// token load fell by at least contextReductionDelta tokens versus the
// original slice, so a compaction that produces no meaningful reduction
// short-circuits instead of re-entering the overflow retry loop.
//
// Expected:
//   - before and after are message slices from the same turn.
//
// Returns:
//   - true when the estimate dropped by at least the meaningful delta.
//
// Side effects: None.
func (e *Engine) contextEstimateDropped(before, after []provider.Message) bool {
	if e == nil || e.tokenCounter == nil {
		return len(after) < len(before)
	}
	count := func(msgs []provider.Message) int {
		tokens := 0
		for i := range msgs {
			tokens += int(e.tokenCounter.Count(msgs[i].Content))
		}
		return tokens
	}
	return count(before)-count(after) >= contextReductionDelta
}

// contextEstimateOverBudget reports whether the supplied message slice
// exceeds the usable context budget for the active provider/model,
// including tool schemas and the response reserve. Recovery must use
// the same limit as dispatch before clearing the overflow state.
//
// Expected:
//   - ctx carries the provider/model resolution keys.
//   - messages is the live tool-loop slice.
//
// Returns:
//   - true when the estimated request tokens exceed the usable budget.
//
// Side effects: None.
func (e *Engine) contextEstimateOverBudget(ctx context.Context, messages []provider.Message) bool {
	if e == nil || e.tokenCounter == nil || len(messages) == 0 {
		return false
	}
	prov := e.lastProviderCtx(ctx)
	model := e.lastModelCtx(ctx)
	limit := e.ResolveContextLength(prov, model)
	if limit <= 0 {
		return false
	}
	req := &provider.ChatRequest{
		Provider: prov,
		Model:    model,
		Messages: messages,
		Tools:    e.buildToolSchemasCtx(ctx),
	}
	return e.checkContextWindowOverflow(req) != nil
}

// watchdogChunkMarkInterval bounds how often flowing stream chunks may
// stamp the turn watchdog's progress mark. Real streams emit content
// deltas every few to few-hundred milliseconds; stamping the atomic on
// every chunk would be pure waste, while stamping at most once per
// interval keeps the marked-progress granularity far below any
// realistic window. The effective throttle shrinks to window/4 for
// small windows so test-scale streams still mark inside the window.
const watchdogChunkMarkInterval = time.Second

// turnWatchdogKeyType identifies the context key carrying the active
// turn watchdog so provider-stream open sites can mark progress without
// widening every retry helper's signature.
type turnWatchdogKeyType struct{}

var turnWatchdogKey turnWatchdogKeyType

// pendingOverflowRecoveryKey is the context key flagging a pending
// context-window-overflow recovery retry.
type pendingOverflowRecoveryKey struct{}

// withPendingOverflowRecovery returns a child context flagged as
// performing a context-window-overflow recovery retry.
//
// Expected:
//   - ctx is a valid context to extend.
//
// Returns:
//   - A child context carrying the pending-overflow-recovery flag.
//
// Side effects:
//   - Stores the flag in the returned context for later retrieval.
func withPendingOverflowRecovery(ctx context.Context) context.Context {
	return context.WithValue(ctx, pendingOverflowRecoveryKey{}, true)
}

// pendingOverflowRecoveryFromContext reports whether the context was
// flagged by withPendingOverflowRecovery.
//
// Expected:
//   - ctx may carry the pending-overflow-recovery flag.
//
// Returns:
//   - True when the flag is present, false otherwise.
//
// Side effects:
//   - None.
func pendingOverflowRecoveryFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(pendingOverflowRecoveryKey{}).(bool)
	return v
}

// pendingNaiveOverflowKey is the context key for the naive-truncation
// overflow recovery taint.
type pendingNaiveOverflowKey struct{}

// withPendingNaiveOverflow returns a child context flagged as tainted by
// a naive-truncation overflow recovery retry. Per the BDD contract, this
// taint must survive a clean retry turn; continuation resumes only after
// proven compaction.
//
// Expected:
//   - ctx is a valid context to extend.
//
// Returns:
//   - A child context carrying the naive-overflow taint flag.
//
// Side effects:
//   - Stores the flag in the returned context for later retrieval.
func withPendingNaiveOverflow(ctx context.Context) context.Context {
	return context.WithValue(ctx, pendingNaiveOverflowKey{}, true)
}

// pendingNaiveOverflowFromContext reports whether the context was
// flagged by withPendingNaiveOverflow.
//
// Expected:
//   - ctx may carry the naive-overflow taint flag.
//
// Returns:
//   - True when the flag is present, false otherwise.
//
// Side effects:
//   - None.
func pendingNaiveOverflowFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(pendingNaiveOverflowKey{}).(bool)
	return v
}

// withTurnWatchdog returns a child context carrying the given turn
// watchdog.
//
// Expected:
//   - ctx is a valid context to extend.
//   - w is the turn-local watchdog for the stream being run.
//
// Returns:
//   - A child context containing the watchdog.
//
// Side effects:
//   - Stores the watchdog in the returned context for later retrieval.
func withTurnWatchdog(ctx context.Context, w *turnWatchdog) context.Context {
	return context.WithValue(ctx, turnWatchdogKey, w)
}

// turnWatchdogFromContext extracts the turn watchdog from the context,
// returning nil when absent. All methods are nil-receiver safe.
//
// Expected:
//   - ctx may carry a turn watchdog stored by withTurnWatchdog.
//
// Returns:
//   - The watchdog, or nil when the context carries none.
//
// Side effects:
//   - None.
func turnWatchdogFromContext(ctx context.Context) *turnWatchdog {
	w, _ := ctx.Value(turnWatchdogKey).(*turnWatchdog)
	return w
}

// turnWatchdog bounds how long a tool-loop turn may run without
// observable progress. Progress is marked at loop iteration starts,
// every provider stream open (inside retryStreamForToolResultWithTools,
// covering all retryStreamForToolResult* call sites), every tool batch
// completion, and — throttled to watchdogChunkMarkInterval — every
// chunk a provider stream delivers. The stall clock pauses while a
// tool batch is executing: an in-flight stamp freezes accumulation so
// a single tool (notably a delegated child) running longer than the
// window does not false-positive, and the batch-completion mark
// re-arms the clock. Provider stream waits and post-tool engine code
// are never paused. When the measured stall reaches the window the
// watchdog fires: it logs an "engine turn watchdog fired" warning,
// cancels the turn's derived context, and the unwind paths terminate
// the stream with the existing StopReasonToolLoopExceeded sentinel.
// The terminal claim guard guarantees exactly one sentinel Done chunk
// per turn.
//
// The watchdog exists because the iteration, duration, and total
// tool-time backstops only evaluate at iteration boundaries; a turn
// parked mid-iteration (cooldown wait, blocked output send, mid-loop
// compaction, a provider that never opens the next stream) makes no
// boundary progress and previously wedged indefinitely.
type turnWatchdog struct {
	window                  time.Duration
	lastProgress            atomic.Int64
	toolExecStartedAt       atomic.Int64
	iterations              atomic.Int64
	fired                   atomic.Bool
	toolTimeBudgetExhausted atomic.Bool
	terminalSent            atomic.Bool
}

// newTurnWatchdog returns a watchdog armed for the given window and
// seeded with the current time. A non-positive window yields a watchdog
// whose watch loop exits immediately (defence for future disable
// semantics; the v1 resolver supplies a positive window in practice).
//
// Expected:
//   - window is the no-progress budget the watchdog enforces.
//
// Returns:
//   - A seeded *turnWatchdog.
//
// Side effects:
//   - None beyond allocating the watchdog.
func newTurnWatchdog(window time.Duration) *turnWatchdog {
	w := &turnWatchdog{window: window}
	if window > 0 {
		w.lastProgress.Store(time.Now().UnixNano())
	}
	return w
}

// markProgress records that the turn made observable progress, re-arming
// the no-progress window. Nil-receiver and disarmed no-op.
//
// Expected:
//   - the receiver may be nil or carry a non-positive window.
//
// Side effects:
//   - Overwrites the last-progress timestamp.
func (w *turnWatchdog) markProgress() {
	if w == nil || w.window <= 0 {
		return
	}
	w.lastProgress.Store(time.Now().UnixNano())
}

// markChunkProgress stamps the throttled chunk-arrival progress mark:
// at most once per watchdogChunkMarkInterval (shrunk to window/4 for
// small windows), via a compare-and-swap that only ever advances the
// timestamp. A healthy stream with inter-chunk gaps g therefore sees
// marks at most (throttle + g) apart, comfortably inside the window,
// while the atomic write stays off the per-chunk hot path.
//
// Expected:
//   - called from the chunk-consumption path with the receiver armed.
//
// Side effects:
//   - Advances the last-progress timestamp when the throttle elapsed.
func (w *turnWatchdog) markChunkProgress() {
	if w == nil || w.window <= 0 {
		return
	}
	throttle := watchdogChunkMarkInterval
	if floor := w.window / 4; floor < throttle {
		throttle = floor
	}
	now := time.Now().UnixNano()
	for {
		last := w.lastProgress.Load()
		if now-last < int64(throttle) {
			return
		}
		if w.lastProgress.CompareAndSwap(last, now) {
			return
		}
	}
}

// markToolExecStart stamps the moment the current tool batch began
// executing so stallSince can freeze accumulation for the in-flight
// span, mirroring the duration backstop's toolExecDuration exclusion.
//
// Expected:
//   - called immediately before the batch's tool executions begin.
//
// Side effects:
//   - Overwrites the in-flight tool-execution timestamp.
func (w *turnWatchdog) markToolExecStart() {
	if w == nil || w.window <= 0 {
		return
	}
	w.toolExecStartedAt.Store(time.Now().UnixNano())
}

// markToolBatchComplete clears the in-flight stamp and records the
// batch completion as progress, re-arming the no-progress clock for
// the post-tool engine code.
//
// Expected:
//   - called immediately after the batch's tool executions return.
//
// Side effects:
//   - Clears the in-flight tool-execution timestamp and overwrites the
//     last-progress timestamp.
func (w *turnWatchdog) markToolBatchComplete() {
	if w == nil {
		return
	}
	w.toolExecStartedAt.Store(0)
	w.markProgress()
}

// stallSince returns the no-progress time measured at now, excluding
// any span where a tool batch is executing: while the in-flight stamp
// is set the measured stall freezes at the value it had when the batch
// started.
//
// Expected:
//   - now is the evaluation instant supplied by the watch loop.
//
// Returns:
//   - The effective stall duration, never negative.
//
// Side effects:
//   - None.
func (w *turnWatchdog) stallSince(now time.Time) time.Duration {
	last := time.Unix(0, w.lastProgress.Load())
	stall := now.Sub(last)
	startedNanos := w.toolExecStartedAt.Load()
	if startedNanos == 0 {
		return stall
	}
	started := time.Unix(0, startedNanos)
	if started.After(last) {
		return started.Sub(last)
	}
	return 0
}

// recordIterations mirrors the loop's iteration counter for the fired
// warning's observability attr.
//
// Expected:
//   - n is the loop's current continuation count.
//
// Side effects:
//   - Overwrites the mirrored iteration counter.
func (w *turnWatchdog) recordIterations(n int) {
	if w == nil {
		return
	}
	w.iterations.Store(int64(n))
}

// markFired flags the watchdog as fired without going through the
// watch loop. The duration/tool-time backstops are budget mechanisms
// orthogonal to the no-progress window; when they trip, recovery-exit
// chunk construction (terminalRecoveryChunk) must still stamp
// tool_loop_exceeded, so the backstop marks the turn as fired.
//
// Side effects:
//   - Sets the fired flag (idempotent).
func (w *turnWatchdog) markFired() {
	if w == nil {
		return
	}
	w.fired.Store(true)
}

// hasFired reports whether the watchdog has fired for this turn.
//
// Expected:
//   - the receiver may be nil.
//
// Returns:
//   - true once the no-progress window expired and the watchdog fired.
//
// Side effects:
//   - None.
func (w *turnWatchdog) hasFired() bool {
	return w != nil && w.fired.Load()
}

// markToolTimeBudgetExhausted flags that the total tool-execution-time
// budget has been exhausted. Unlike the duration backstop this does
// not mark the turn fired, but recovery-exit chunk construction
// (terminalRecoveryChunk) still stamps tool_loop_exceeded so the
// terminal chunk carries a stop reason.
//
// Side effects:
//   - Sets the tool-time budget exhausted flag (idempotent).
func (w *turnWatchdog) markToolTimeBudgetExhausted() {
	if w == nil {
		return
	}
	w.toolTimeBudgetExhausted.Store(true)
}

// hasToolTimeBudgetExhausted reports whether the total tool-execution
// time budget was exhausted for this turn.
//
// Expected:
//   - the receiver may be nil.
//
// Returns:
//   - true once the tool-time budget backstop has tripped.
//
// Side effects:
//   - None.
func (w *turnWatchdog) hasToolTimeBudgetExhausted() bool {
	return w != nil && w.toolTimeBudgetExhausted.Load()
}

// claimTerminal reports whether the caller is responsible for emitting
// the watchdog's terminal Done chunk; exactly one caller wins per turn.
//
// Expected:
//   - the receiver may be nil.
//
// Returns:
//   - true for the single caller that must emit the sentinel chunk.
//
// Side effects:
//   - Flips the terminal-sent flag on the winning caller.
func (w *turnWatchdog) claimTerminal() bool {
	if w == nil {
		return false
	}
	return w.terminalSent.CompareAndSwap(false, true)
}

// watch runs the watchdog loop until done closes, acknowledging
// shutdown on the ack channel. On fire it warns, marks the turn as
// fired, and cancels the turn context. The second done-check narrows
// the race between a turn completing naturally and the window expiring.
//
// Expected:
//   - sessionID identifies the turn being watched, for logging.
//   - cancel cancels the turn's derived context.
//   - done closes when the turn completes.
//   - ack receives the goroutine's shutdown acknowledgement.
//
// Side effects:
//   - Emits one WARN log and invokes cancel when the window expires.
//   - Closes ack on every exit path.
func (w *turnWatchdog) watch(sessionID string, cancel context.CancelFunc, done <-chan struct{}, ack chan<- struct{}) {
	defer close(ack)
	if w.window <= 0 {
		return
	}
	interval := w.window / 4
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	if interval > 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			stall := w.stallSince(time.Now())
			if stall < w.window {
				continue
			}
			select {
			case <-done:
				return
			default:
			}
			if !w.fired.CompareAndSwap(false, true) {
				cancel()
				return
			}
			slog.Warn("engine turn watchdog fired",
				"session", sessionID,
				"last_progress_age", stall,
				"iterations", w.iterations.Load(),
			)
			cancel()
			return
		}
	}
}

// emitStreamChunk sends chunk to the turn's output channel, abandoning
// the send when the turn context is cancelled so a consumer that has
// stopped reading cannot wedge the loop goroutine on a full buffer.
//
// Expected:
//   - ctx is the turn context whose cancellation ends the stream.
//   - outChan is the turn's output channel.
//   - chunk is the non-terminal chunk to forward.
//
// Returns:
//   - true when the chunk was delivered; false when the turn context
//     was cancelled first.
//
// Side effects:
//   - Sends chunk to outChan unless the context cancellation wins.
func emitStreamChunk(ctx context.Context, outChan chan<- provider.StreamChunk, chunk provider.StreamChunk) bool {
	select {
	case outChan <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

// emitTerminalStreamChunk sends the turn's terminal Done chunk with
// terminal-chunk-first discipline: when the guarded send loses to a
// cancelled turn context the chunk is still delivered via a send that
// does not race the same cancellation, so consumers observe a terminal
// chunk before the channel closes instead of hanging on a wedged turn.
//
// Expected:
//   - ctx is the turn context whose cancellation ends the stream.
//   - outChan is the turn's output channel.
//   - chunk is the terminal (Done) chunk every consumer requires.
//
// Side effects:
//   - Sends chunk to outChan; warns and drops it only when the buffer
//     is full and no receiver remains.
func emitTerminalStreamChunk(ctx context.Context, outChan chan<- provider.StreamChunk, chunk provider.StreamChunk) {
	select {
	case outChan <- chunk:
		return
	case <-ctx.Done():
	}
	select {
	case outChan <- chunk:
	default:
		slog.Warn("engine terminal chunk dropped: output channel unavailable after turn cancellation")
	}
}

// terminalRecoveryChunk builds the terminal Done chunk for an
// error-recovery exit (synthetic tool-result error, stream-error after
// retry, context-cancel fall-through). When the turn watchdog has
// fired — including a fired-but-unclaimed watchdog racing a
// compaction/overflow recovery — the StopReason sentinel is stamped so
// consumers reading the terminal chunk still observe tool_loop_exceeded
// instead of an empty reason; model/provider mirror the normal
// terminal path (cf. toolloop.go:1972/1987/2798).
//
// Expected:
//   - ctx is the turn context carrying the turn watchdog and last
//     provider/model resolution.
//   - err is the recovery error carried on the chunk (may be nil only
//     on watchdog-only exits).
//
// Side effects:
//   - Stamps the StopReason sentinel tool_loop_exceeded on the terminal
//     chunk when the turn watchdog has fired or the tool time budget is
//     exhausted.
//
// Returns: the fully stamped terminal chunk.
func (e *Engine) terminalRecoveryChunk(w *turnWatchdog, ctx context.Context, err error) provider.StreamChunk {
	chunk := provider.StreamChunk{
		Error:      err,
		Done:       true,
		ModelID:    e.lastModelCtx(ctx),
		ProviderID: e.lastProviderCtx(ctx),
	}
	if w != nil && (w.hasFired() || w.hasToolTimeBudgetExhausted()) {
		chunk.StopReason = session.StopReasonToolLoopExceeded
	}
	return chunk
}

// streamWithToolLoop processes streaming chunks, handles tool calls, and loops until completion.
//
// Expected:
//   - ctx is a valid context for the operation.
//   - messages contains the conversation history.
//   - providerChunks is a channel of chunks from the provider.
//   - outChan is the output channel for processed chunks.
//
// Side effects:
//   - Sends chunks to outChan.
//   - Executes tool calls and appends results to messages.
//   - Stores responses in the context store.
//
// Returns: result of streamWithToolLoop.
func (e *Engine) streamWithToolLoop(
	ctx context.Context, sessionID string, messages []provider.Message,
	providerChunks <-chan provider.StreamChunk, outChan chan<- provider.StreamChunk,
	postTurnUsage postTurnUsageEmitter,
) {
	defer e.evictCompletedBackgroundTasks()
	watchdog := turnWatchdogFromContext(ctx)
	if watchdog == nil {
		watchdog = newTurnWatchdog(e.toolLoopWatchdog)
		ctx = withTurnWatchdog(ctx, watchdog)
	}
	ctx = withMidLoopCompactionGuard(ctx, &midLoopCompactionGuard{})
	ctx, loopCancel := context.WithCancel(ctx)
	defer loopCancel()
	watchdogDone := make(chan struct{})
	watchdogAck := make(chan struct{})
	go watchdog.watch(sessionID, loopCancel, watchdogDone, watchdogAck)
	defer func() {
		close(watchdogDone)
		<-watchdogAck
		if watchdog.hasFired() && watchdog.claimTerminal() {
			e.warnDeliveryToolBypassCtx(ctx, sessionID)
			emitTerminalStreamChunk(ctx, outChan, provider.StreamChunk{
				Done:       true,
				StopReason: session.StopReasonToolLoopExceeded,
				ModelID:    e.lastModelCtx(ctx),
				ProviderID: e.lastProviderCtx(ctx),
			})
		}
	}()
	var todoContinuationCount int
	var noProgressContinuations int
	var lastTodoContinuationSnapshot []todo.Item
	var backgroundContinuationCount int
	const maxTodoContinuations = 20
	const maxNoProgressContinuations = 3
	const maxBackgroundContinuations = 20

	attempt := 0
	// loopStart is the monotonic turn clock, captured once at tool-loop
	// entry and never reset — continuation injections, grace rounds, and
	// forced-summary rounds all draw from the SAME turn-level time
	// budget. Compared against maxToolLoopDuration in the cap check below
	// so a duration cap that a continuation window could previously reset
	// is now a real upper bound on the whole turn.
	loopStart := time.Now()
	// toolExecDuration accumulates time spent executing tools across the
	// WHOLE turn (monotonic, never reset). It is subtracted from the
	// wall-clock elapsed when checking maxToolLoopDuration so that slow
	// tools do not consume the duration budget meant to cap provider
	// round-trips and retry logic; nonDelegatedToolExecDuration mirrors
	// it for the total-tool-time backstop and excludes delegation-shaped
	// calls that inherit the parent deadline.
	var toolExecDuration time.Duration
	var nonDelegatedToolExecDuration time.Duration
	// Turn-local tool-loop guard state. Declared here (never on the Engine)
	// so concurrent turns can never share it. iterations counts continuations
	// (about-to-re-request passes); lastFingerprint / identicalRun track the
	// primary repeat-call detector — when the same canonicalised tool batch
	// recurs maxIdenticalToolCalls consecutive times the loop is stuck.
	iterations := 0
	softTripContinuations := 0
	lastFingerprint := ""
	identicalRun := 0
	sameToolPatternRun := 0
	lastToolNames := ""
	const maxToolUseNoCallsRetries = 3
	var toolUseNoCallsAttempts int
	sawContextOverflow := false
	pendingOverflowRecovery := pendingOverflowRecoveryFromContext(ctx)
	naiveOverflow := pendingNaiveOverflowFromContext(ctx)
	pendingSkipOverflow := false
	overflowRetries := 0
	const maxDeliveryRetries = 3
	var deliveryRetries int
	const maxProviderRetryWait = 5 * time.Minute
	cumulativeCooldownWait := time.Duration(0)
	maxCumulativeCooldownWait := e.maxToolLoopDuration
	if maxCumulativeCooldownWait <= 0 {
		maxCumulativeCooldownWait = 10 * time.Minute
	}
	todoContinuationCount = 0
	noProgressContinuations = 0
	lastTodoContinuationSnapshot = []todo.Item(nil)
	consecutiveSameToolContinuations := 0
	const maxRejectedToolCalls = 3
	const maxSoftTripContinuations = 3
	consecutiveRejectedToolCalls := 0
	delegationGraceUsed := false
	finalResponseGraceUsed := false
	forcedSummaryUsed := false
	skillsGuardRejectionLoop := false
	// updateTodoContinuationProgress applies the continuation progress rule:
	// progress means the todo snapshot changed since the last continuation
	// decision. Tool activity alone does not count — repeated identical
	// tool batches with an unchanged todo list are not progress.
	updateTodoContinuationProgress := func(current []todo.Item) {
		if slices.Equal(lastTodoContinuationSnapshot, current) {
			noProgressContinuations++
		} else {
			noProgressContinuations = 0
		}
		lastTodoContinuationSnapshot = append([]todo.Item(nil), current...)
	}
	checkIncompleteTodosBeforeComplete := func(
		attempt int,
		noProgressContinuations int,
		todoContinuationCount int,
		maxNoProgressContinuations int,
		maxTodoContinuations int,
		logReason string,
	) (bool, provider.Message) {
		if e.todoStore == nil || sawContextOverflow || naiveOverflow {
			return false, provider.Message{}
		}

		hasMore, incompletes := e.hasIncompleteTodos(sessionID)
		if !hasMore {
			slog.Debug("no incomplete todos, completing response",
				"session", sessionID,
				"reason", logReason,
			)
			return false, provider.Message{}
		}

		slog.Warn("preventing completion: session has incomplete todos",
			"session", sessionID,
			"reason", logReason,
			"incomplete_count", len(incompletes),
			"attempt", attempt,
		)

		if noProgressContinuations >= maxNoProgressContinuations {
			slog.Warn("todo continuation stopped: no progress with healthy providers",
				"session", sessionID,
				"no_progress_continuations", noProgressContinuations,
				"reason", logReason,
			)
			return false, provider.Message{}
		}

		if todoContinuationCount >= maxTodoContinuations {
			slog.Warn("todo continuation budget exhausted",
				"session", sessionID,
				"max_continuations", maxTodoContinuations,
				"reason", logReason,
			)
			return false, provider.Message{}
		}

		slog.Info("injecting todo continuation before completion",
			"session", sessionID,
			"incomplete_count", len(incompletes),
			"reason", logReason,
		)

		continuationMsg := buildTodoContinuationMessage(incompletes)
		e.resetContinuationState(sessionID)

		if allTodosTerminal(incompletes) {
			e.mu.Lock()
			e.todoContinuationFired[sessionID] = false
			e.mu.Unlock()
		}

		return true, continuationMsg
	}
	waitForProviderRetry := func(retryAt time.Time) (bool, bool) {
		wait := time.Until(retryAt)
		if wait > maxProviderRetryWait {
			slog.Warn("todo continuation stopped: all providers rate-limited, retry too far out",
				"session", sessionID,
				"retry_at", retryAt,
				"wait", wait,
			)
			emitStreamChunk(ctx, outChan, provider.StreamChunk{
				Content:   fmt.Sprintf("All providers unavailable until %s. Please retry later.", retryAt.Format(time.RFC1123)),
				EventType: "provider_retry_too_far",
			})
			return false, true
		}
		if cumulativeCooldownWait+wait > maxCumulativeCooldownWait {
			slog.Warn("todo continuation stopped: cumulative cooldown wait budget exhausted",
				"session", sessionID,
				"provider", e.lastProviderCtx(ctx),
				"model", e.lastModelCtx(ctx),
				"wait", wait,
				"cumulative", cumulativeCooldownWait,
				"budget", maxCumulativeCooldownWait,
			)
			emitStreamChunk(ctx, outChan, provider.StreamChunk{
				Content:   "All providers rate-limited and cumulative cooldown wait budget exhausted. Please retry later.",
				EventType: "provider_retry_budget_exhausted",
			})
			return false, true
		}
		slog.Info("cooldown wait before provider retry",
			"session", sessionID,
			"provider", e.lastProviderCtx(ctx),
			"model", e.lastModelCtx(ctx),
			"wait", wait,
			"cumulative", cumulativeCooldownWait+wait,
		)
		emitStreamChunk(ctx, outChan, provider.StreamChunk{
			Content:   fmt.Sprintf("All providers unavailable. Retrying in %s (at %s).", wait.Round(time.Second), retryAt.Format("15:04:05")),
			EventType: "provider_retry_scheduled",
		})
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
			cumulativeCooldownWait += wait
			return true, false
		case <-ctx.Done():
			return false, true
		}
	}
	type deliveryRetryAction int
	const (
		deliveryRetryNone deliveryRetryAction = iota
		deliveryRetryContinue
		deliveryRetryStop
	)
	// maybeCompactForRetry compacts the session if the context is large before retrying.
	// This prevents timeout-based failures when retrying with large contexts.
	maybeCompactForRetry := func(reason string) bool {
		pendingSkipOverflow = false
		if e == nil || e.store == nil || e.tokenCounter == nil {
			return false
		}
		retryReq := provider.ChatRequest{
			Provider: e.lastProviderCtx(ctx),
			Model:    e.lastModelCtx(ctx),
			Messages: messages,
			Tools:    e.buildToolSchemasCtx(ctx),
		}
		if provOverride := session.ProviderOverrideFromContext(ctx); provOverride != "" {
			retryReq.Provider = provOverride
		}
		if modelOverride := session.ModelOverrideFromContext(ctx); modelOverride != "" {
			retryReq.Model = modelOverride
		}
		if pErr := e.checkContextWindowOverflow(&retryReq); pErr == nil {
			return false
		}
		manifestCopy := e.Manifest()
		tokenBudget := e.ResolveContextLength(retryReq.Provider, retryReq.Model)
		if tokenBudget <= 0 {
			return false
		}
		slog.Info(reason+": context large, force-compacting before retry",
			"session", sessionID, "message_count", len(messages))
		if summary := e.maybeAutoCompactExplicit(ctx, sessionID, &manifestCopy, tokenBudget, "manual", messages); summary != "" {
			slog.Info(reason+": compaction succeeded, rebuilding context window",
				"session", sessionID, "summary_length", len(summary))
			if rebuilt := e.rebuildContextWindowTokenBounded(ctx, sessionID, messages, summary); rebuilt != nil {
				messages = rebuilt
			}
			pendingSkipOverflow = true
			return true
		}
		return false
	}
	// compactSoftContinuation gates soft-cap continuation on context
	// headroom. Unlike maybeCompactForRetry it does NOT require an
	// existing overflow error: when the context is over the
	// auto-compaction threshold it force-compacts (trigger
	// "soft_continuation") and rebuilds the window; the continuation
	// is only allowed when the result provably fits.
	compactSoftContinuation := func() bool {
		if e == nil || e.store == nil || e.tokenCounter == nil {
			return true
		}
		provName := e.lastProviderCtx(ctx)
		modelName := e.lastModelCtx(ctx)
		if provOverride := session.ProviderOverrideFromContext(ctx); provOverride != "" {
			provName = provOverride
		}
		if modelOverride := session.ModelOverrideFromContext(ctx); modelOverride != "" {
			modelName = modelOverride
		}
		tokenBudget := e.ResolveContextLength(provName, modelName)
		if tokenBudget <= 0 {
			// Legacy engines without compaction wiring keep the old
			// permissive behaviour.
			return true
		}
		manifestCopy := e.Manifest()
		fullWindowTokens := e.estimateRequestTokens(&provider.ChatRequest{
			Provider: provName,
			Model:    modelName,
			Messages: messages,
			Tools:    e.buildToolSchemasCtx(ctx),
		})
		if threshold, ok := e.autoCompactionThreshold(&manifestCopy, tokenBudget); !ok ||
			float64(fullWindowTokens) <= threshold*float64(tokenBudget) {
			// Under the auto-compaction threshold — there is headroom,
			// continue without compaction.
			return true
		}
		slog.Info("soft continuation: context over threshold, force-compacting",
			"session", sessionID,
			"full_window_tokens", fullWindowTokens,
			"token_budget", tokenBudget)
		if summary := e.maybeAutoCompactExplicit(ctx, sessionID, &manifestCopy, tokenBudget, "soft_continuation", messages); summary != "" {
			if rebuilt := e.rebuildContextWindowTokenBounded(ctx, sessionID, messages, summary); rebuilt != nil {
				messages = rebuilt
				return true
			}
		}
		return false
	}
	skipCtx := func(base context.Context) context.Context {
		if pendingSkipOverflow {
			pendingSkipOverflow = false
			return session.WithSkipContextWindowOverflowCheck(base)
		}
		return base
	}

	maybeRetryDelivery := func(onStop func()) deliveryRetryAction {
		if !e.requiresDeliveryToolCtx(ctx) || e.deliveryToolCompleted(sessionID) {
			return deliveryRetryNone
		}
		if deliveryRetries >= maxDeliveryRetries {
			slog.Warn("delivery tool not called after max retries, completing with warning",
				"session", sessionID,
				"delivery_retries", deliveryRetries,
			)
			onStop()
			return deliveryRetryStop
		}
		deliveryRetries++
		slog.Warn("delivery tool not called, retrying with corrective message",
			"session", sessionID,
			"attempt", deliveryRetries,
			"max_attempts", maxDeliveryRetries,
		)
		messages = append(messages, provider.Message{
			Role:    "user",
			Content: "Your previous response narrated an intent to call a tool but did not actually call it. You MUST call one of the delivery tools now to persist your results. Do not respond with prose — call the tool.",
		})
		var streamErr error
		deliveryCtx := ctx
		if deliveryTools := e.deliveryToolsForCtx(ctx); len(deliveryTools) > 0 {
			deliveryCtx = session.WithToolsAllowlistOverride(ctx, deliveryTools)
		}
		maybeCompactForRetry("delivery retry")
		deliveryCtx = skipCtx(deliveryCtx)
		providerChunks, streamErr = e.retryStreamForToolResult(deliveryCtx, sessionID, messages, attempt)
		if streamErr != nil {
			slog.Error("delivery tool retry stream failed", "session", sessionID, "error", streamErr)
			e.persistDeliveryFailureFallback(deliveryCtx, sessionID, messages, streamErr)

			if retryAt, ok := e.SoonestProviderRetry(); ok {
				slog.Info("delivery retry: providers rate-limited, waiting for cooldown",
					"session", sessionID, "retry_at", retryAt, "wait", time.Until(retryAt))

				e.bus.Publish(events.EventProviderRequestRetry,
					events.NewProviderRequestRetryEvent(
						events.ProviderRequestRetryEventData{
							SessionID:    sessionID,
							AgentID:      e.activeAgentID(ctx),
							ProviderName: e.lastProviderCtx(ctx),
							ModelName:    e.lastModelCtx(ctx),
							Reason:       "delivery_cooldown_wait",
							Attempt:      deliveryRetries,
						}))

				if retry, stop := waitForProviderRetry(retryAt); retry {
					deliveryRetries++
					slog.Warn("delivery tool not called, retrying after provider cooldown",
						"session", sessionID, "attempt", deliveryRetries)
					maybeCompactForRetry("delivery retry")
					providerChunks, streamErr = e.retryStreamForToolResult(skipCtx(deliveryCtx), sessionID, messages, attempt+1)
					if streamErr != nil {
						slog.Error("delivery tool retry stream failed after provider cooldown",
							"session", sessionID, "error", streamErr)
						e.persistDeliveryFailureFallback(deliveryCtx, sessionID, messages, streamErr)
						onStop()
						return deliveryRetryStop
					}
					attempt++
					e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
					return deliveryRetryContinue
				} else if stop {
					slog.Error("delivery retry cancelled during cooldown wait",
						"session", sessionID)
					onStop()
					return deliveryRetryStop
				}
			}

			onStop()
			return deliveryRetryStop
		}
		attempt++
		e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
		return deliveryRetryContinue
	}
	var responseContent string
	var thinkingContent string
	continueAfterTodoCheck := func(reason string) bool {
		shouldContinue, contMsg := checkIncompleteTodosBeforeComplete(
			attempt, noProgressContinuations, todoContinuationCount, maxNoProgressContinuations, maxTodoContinuations, reason,
		)
		if !shouldContinue {
			return false
		}
		todoContinuationCount++

		messages = append(messages, contMsg)
		maybeCompactForRetry("todo continuation retry")
		var streamErr error
		providerChunks, streamErr = e.retryStreamForToolResult(skipCtx(ctx), sessionID, messages, attempt)
		if streamErr != nil {
			slog.Error("todo continuation stream failed after completion guard",
				"session", sessionID,
				"error", streamErr,
				"reason", reason,
			)
			return false
		}
		attempt++
		iterations = 0
		identicalRun = 0
		lastFingerprint = ""
		sameToolPatternRun = 0
		lastToolNames = ""
		e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
		return true
	}
	completeAfterTodoCheck := func(reason string) bool {
		if continueAfterTodoCheck(reason) {
			return true
		}
		e.completeResponse(ctx, sessionID, responseContent, thinkingContent)
		return false
	}
	forcedSummaryTerminal := func(reason string) bool {
		if !forcedSummaryUsed {
			forcedSummaryUsed = true
			slog.Info("forced summary round: terminal stop, stripping tools",
				"session", sessionID,
				"reason", reason,
			)
			messages = append(messages, buildFinalResponseMessage())
			var streamErr error
			providerChunks, streamErr = e.retryStreamForToolResultNoSchemas(ctx, sessionID, messages, attempt)
			if streamErr == nil {
				attempt++
				e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
				return true
			}
			slog.Error("forced summary round stream failed",
				"session", sessionID,
				"error", streamErr,
			)
		}
		e.persistCappedTurnTerminal(ctx, sessionID, reason)
		e.warnDeliveryToolBypassCtx(ctx, sessionID)
		emitTerminalStreamChunk(ctx, outChan, provider.StreamChunk{
			Done:       true,
			StopReason: session.StopReasonToolLoopExceeded,
			ModelID:    e.lastModelCtx(ctx),
			ProviderID: e.lastProviderCtx(ctx),
		})
		return false
	}
	doneAfterTodoCheck := func(reason string) bool {
		if continueAfterTodoCheck(reason) {
			return true
		}
		return forcedSummaryTerminal(reason)
	}
	for {
		watchdog.markProgress()
		result := e.processStreamChunks(ctx, sessionID, providerChunks, outChan, postTurnUsage)
		responseContent = result.responseContent
		thinkingContent = result.thinkingContent
		if result.done {
			if pendingOverflowRecovery && !result.contextOverflow && !naiveOverflow {
				sawContextOverflow = false
				pendingOverflowRecovery = false
			}
			// tool_use_no_calls: provider announced stop_reason="tool_use"
			// but emitted zero tool_call blocks. This is a provider-side
			// fault (observed on Z.AI glm-5.1 and similar providers).
			// Retry the stream instead of completing the turn with a
			// broken or regurgitated response.
			if result.stopReason == "tool_use" && len(result.toolCalls) == 0 && toolUseNoCallsAttempts < maxToolUseNoCallsRetries {
				toolUseNoCallsAttempts++
				slog.Warn("tool_use_no_calls detected, retrying provider stream",
					"session", sessionID,
					"attempt", toolUseNoCallsAttempts,
					"max_attempts", maxToolUseNoCallsRetries,
				)
				retryReq := provider.ChatRequest{
					Provider: e.lastProviderCtx(ctx),
					Model:    e.lastModelCtx(ctx),
					Messages: messages,
					Tools:    e.buildToolSchemasCtx(ctx),
				}
				if provOverride := session.ProviderOverrideFromContext(ctx); provOverride != "" {
					retryReq.Provider = provOverride
				}
				if modelOverride := session.ModelOverrideFromContext(ctx); modelOverride != "" {
					retryReq.Model = modelOverride
				}
				newChunks, streamErr := e.streamFromProvider(ctx, &retryReq)
				if streamErr != nil {
					slog.Error("tool_use_no_calls retry stream failed, falling through",
						"session", sessionID,
						"error", streamErr,
						"attempt", toolUseNoCallsAttempts,
					)
					if completeAfterTodoCheck("tool execution error") {
						continue
					}
					return
				}
				providerChunks = newChunks
				e.bus.Publish(events.EventProviderRequestRetry, events.NewProviderRequestRetryEvent(events.ProviderRequestRetryEventData{
					SessionID:    sessionID,
					AgentID:      e.activeAgentID(ctx),
					ProviderName: e.lastProviderCtx(ctx),
					ModelName:    e.lastModelCtx(ctx),
					Reason:       "tool_use_no_calls",
					Attempt:      toolUseNoCallsAttempts,
				}))
				e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
				continue
			}
			if e.maxToolLoopDuration > 0 && time.Since(loopStart)-toolExecDuration >= e.maxToolLoopDuration {
				watchdog.markFired()
			}
			if result.contextOverflow {
				sawContextOverflow = true
				if pendingOverflowRecovery {
					slog.Error("post-compaction retry still over context window, surfacing terminal compaction-insufficient error",
						"session", sessionID,
						"overflow_retries", overflowRetries,
					)
					recoveryChunk := e.terminalRecoveryChunk(watchdog, ctx, fmt.Errorf("%w", ErrCompactionInsufficient))
					recoveryChunk.Content = ErrCompactionInsufficient.Error()
					if e.maxToolLoopDuration > 0 && (time.Since(loopStart)-toolExecDuration >= e.maxToolLoopDuration || nonDelegatedToolExecDuration >= e.maxToolLoopDuration) {
						recoveryChunk.StopReason = session.StopReasonToolLoopExceeded
					}
					emitTerminalStreamChunk(ctx, outChan, recoveryChunk)
					return
				}
				if overflowRetries < maxOverflowRetries {
					overflowRetries++
					slog.Warn("context window overflow detected, attempting compaction and retry",
						"session", sessionID,
						"overflow_retry", overflowRetries,
						"max_overflow_retries", maxOverflowRetries,
					)
					forceManifest := e.Manifest()
					forceBudget := e.ModelContextLimit()
					compacted := ""
					if forceBudget > 0 && !midLoopCompactionGuardFired(ctx) {
						compacted = e.maybeAutoCompactExplicit(ctx, sessionID, &forceManifest, forceBudget, "tool_result_wave", messages)
					}
					// Part 3: accept the compacted window only when it
					// meaningfully reduces the estimated token load —
					// a compaction that produces no reduction will not
					// clear the gate, so short-circuit to the terminal
					// error instead of re-entering the loop.
					summary := ""
					compactionReduced := false
					if compacted != "" {
						summary = e.lastCompactionSummaryText()
						if rebuilt := e.rebuildContextWindowTokenBounded(ctx, sessionID, messages, summary); rebuilt != nil {
							stillOver := e.contextEstimateOverBudget(ctx, rebuilt)
							if stillOver && !e.contextEstimateDropped(messages, rebuilt) {
								slog.Warn("compaction produced no meaningful token reduction, surfacing terminal compaction-insufficient error",
									"session", sessionID,
									"messages", len(messages),
								)
								recoveryChunk := e.terminalRecoveryChunk(watchdog, ctx, fmt.Errorf("%w", ErrCompactionInsufficient))
								if e.maxToolLoopDuration > 0 && (time.Since(loopStart)-toolExecDuration >= e.maxToolLoopDuration || nonDelegatedToolExecDuration >= e.maxToolLoopDuration) {
									recoveryChunk.StopReason = session.StopReasonToolLoopExceeded
								}
								emitTerminalStreamChunk(ctx, outChan, recoveryChunk)
								return
							}
							compactionReduced = len(rebuilt) > 0 && e.contextEstimateDropped(messages, rebuilt)
							messages = rebuilt
						}
					}
					compactedViaRefresh := compacted != "" || e.emitMidToolLoopRefresh(ctx, sessionID, outChan, messages)
					if compactedViaRefresh {
						// rebuildContextWindow reassembles from the
						// persisted store, which still carries every
						// message (nothing is cold when the 50-message
						// sliding window retains the whole transcript),
						// so the estimate stays ~est > usable and the
						// retry would re-hit the gate. Mirror
						// maybeCompactForRetry instead: system +
						// todo + summary + token-bounded hot tail that
						// provably fits the usable budget.
						// Force-compaction happened (or the gate tier
						// agreed), but the rebuilt window can still
						// sit above the gate's usable budget — the
						// cold prefix is replaced by the summary but
						// the hot tail plus summary may remain over
						// the refusal boundary. Mirror
						// one retry so the real provider (whose
						// limit differs from the fallback-derived
						// estimate) gets the final say. Only skip
						// when the rebuilt window is no longer
						// estimated over budget — an over-budget
						// rebuild must keep the gate armed.
						var retryCtx context.Context = ctx
						// A compaction that genuinely reduced the window
						// (contextEstimateDropped, verified above when a
						// rebuild occurred) earns exactly one skip-gate
						// retry so the real provider — whose limit may
						// differ from the engine's reserve-inflated
						// estimate — gets the final say. The output
						// reserve can collapse usable (limit - reserve)
						// below even a tiny post-compaction request, so
						// an estimate-only check here would strand a
						// legitimately compacted turn at the gate. When
						// compaction did NOT reduce the window, keep the
						// gate armed.
						if compactionReduced || !e.contextEstimateOverBudget(ctx, messages) {
							retryCtx = session.WithSkipContextWindowOverflowCheck(ctx)
						}
						var retryErr error
						providerChunks, retryErr = e.retryStreamForToolResult(retryCtx, sessionID, messages, attempt)
						if retryErr == nil {
							// Compaction succeeded and the retry
							// stream opened cleanly — the overflow
							// is recovered, not terminal. Clear the
							// taint so a clean post-compaction turn
							// with pending todos can still drive the
							// todo-continuation loop (Ginkgo:
							// "compacts before re-sending a near-limit
							// continuation turn"). The taint stays
							// set when compaction could NOT fire
							// (naive-truncation fallback below),
							// which is what the BDD contract
							// "Context-window overflow does not
							// trigger todo-continuation" pins: an
							// unrecoverable overflow must not spin
							// the continuation loop.
							pendingOverflowRecovery = true
							attempt++
							e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
							continue
						}
						slog.Error("context overflow retry stream failed",
							"session", sessionID,
							"error", retryErr,
						)
					} else {
						slog.Warn("context overflow: compaction did not fire, applying token-budgeted truncation",
							"session", sessionID,
							"messages_before", len(messages),
						)
						messages = e.truncateMessagesTokenBounded(ctx, messages)
						slog.Info("context overflow: truncated messages",
							"session", sessionID,
							"messages_after", len(messages),
						)
						var retryErr error
						providerChunks, retryErr = e.retryStreamForToolResult(ctx, sessionID, messages, attempt)
						if retryErr == nil {
							// Naive truncation does not guarantee the
							// overflow is recovered: keep the taint set
							// so todo-continuation stays suppressed and
							// a second overflow terminates the loop.
							naiveOverflow = true
							pendingOverflowRecovery = true
							attempt++
							e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
							continue
						}
						slog.Error("context overflow retry stream failed after truncation",
							"session", sessionID,
							"error", retryErr,
						)
					}
				} else {
					slog.Warn("context overflow retries exhausted",
						"session", sessionID,
						"overflow_retries", overflowRetries,
					)
				}
				e.completeResponse(ctx, sessionID, responseContent, thinkingContent)
				return
			}
			if result.responseContent == "" && len(result.toolCalls) == 0 {
				provider := e.lastProviderCtx(ctx)
				model := e.lastModelCtx(ctx)
				slog.Warn("model returned empty response, completing turn and marking provider unhealthy",
					"session", sessionID,
					"stop_reason", result.stopReason,
					"provider", provider,
					"model", model,
				)
				if provider != "" && model != "" && e.failoverManager != nil {
					e.failoverManager.Health().MarkRateLimited(provider, model, time.Now().Add(5*time.Minute))
				}
				if completeAfterTodoCheck("empty response with no tool calls") {
					continue
				}
				return
			}
			if sawContextOverflow {
				e.completeResponse(ctx, sessionID, responseContent, thinkingContent)
				return
			}
			if hasMore, incompletes := e.hasIncompleteTodos(sessionID); hasMore && !sawContextOverflow && !naiveOverflow {
				deliveryStopped := false
				switch maybeRetryDelivery(func() {
					deliveryStopped = true
				}) {
				case deliveryRetryContinue:
					continue
				case deliveryRetryStop:
					break
				}
				if deliveryStopped {
					noProgressContinuations = 0
					if completeAfterTodoCheck("delivery retry exhausted after turn end") {
						continue
					}
					return
				}
				updateTodoContinuationProgress(incompletes)
				if noProgressContinuations >= maxNoProgressContinuations {
					if retryAt, ok := e.SoonestProviderRetry(); ok {
						if retry, stop := waitForProviderRetry(retryAt); retry {
							var streamErr error
							providerChunks, streamErr = e.retryStreamForToolResult(ctx, sessionID, messages, attempt)
							if streamErr != nil {
								slog.Error("todo continuation retry stream failed after cooldown",
									"session", sessionID,
									"error", streamErr,
								)
								if completeAfterTodoCheck("todo continuation retry failed after cooldown") {
									continue
								}
								return
							}
							attempt++
							identicalRun = 0
							lastFingerprint = ""
							sameToolPatternRun = 0
							lastToolNames = ""
							e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
							continue
						} else if stop {
							if completeAfterTodoCheck("todo continuation cooldown stop after turn end") {
								continue
							}
							return
						}
					}
					slog.Warn("todo continuation stopped: no progress with healthy providers",
						"session", sessionID,
						"no_progress_continuations", noProgressContinuations,
					)
					if completeAfterTodoCheck("todo continuation no progress after turn end") {
						continue
					}
					return
				}
				if todoContinuationCount >= maxTodoContinuations {
					slog.Warn("todo continuation budget exhausted after turn end",
						"session", sessionID,
						"max_continuations", maxTodoContinuations,
					)
					if completeAfterTodoCheck("todo continuation budget exhausted after turn end") {
						continue
					}
					return
				}
				todoContinuationCount++
				consecutiveSameToolContinuations = 0
				slog.Info("incomplete todos after turn end, injecting continuation",
					"session", sessionID,
					"incomplete_count", len(incompletes),
				)
				contMsg := buildTodoContinuationMessage(incompletes)
				messages = append(messages, contMsg)
				maybeCompactForRetry("todo continuation retry")
				e.resetContinuationState(sessionID)
				if allTodosTerminal(incompletes) {
					e.mu.Lock()
					e.todoContinuationFired[sessionID] = false
					e.mu.Unlock()
				}
				var streamErr error
				providerChunks, streamErr = e.retryStreamForToolResult(skipCtx(ctx), sessionID, messages, attempt)
				if streamErr != nil {
					slog.Error("todo continuation stream failed",
						"session", sessionID,
						"error", streamErr,
					)
					if completeAfterTodoCheck("todo continuation stream failed") {
						continue
					}
					return
				}
				attempt++
				iterations = 0
				identicalRun = 0
				lastFingerprint = ""
				sameToolPatternRun = 0
				lastToolNames = ""
				e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
				continue
			}
			deliveryStopped := false
			switch maybeRetryDelivery(func() {
				deliveryStopped = true
			}) {
			case deliveryRetryContinue:
				continue
			case deliveryRetryStop:
				e.completeResponse(ctx, sessionID, responseContent, thinkingContent)
				return
			}
			if deliveryStopped {
				if completeAfterTodoCheck("delivery retry exhausted after stream truncation") {
					continue
				}
				return
			}
			if completeAfterTodoCheck("turn end without todo continuation") {
				continue
			}
			return
		}

		if len(result.toolCalls) == 0 {
			if hasMore, incompletes := e.hasIncompleteTodos(sessionID); hasMore && !sawContextOverflow && !naiveOverflow {
				deliveryStopped := false
				switch maybeRetryDelivery(func() {
					deliveryStopped = true
				}) {
				case deliveryRetryContinue:
					continue
				case deliveryRetryStop:
					break
				}
				if deliveryStopped {
					noProgressContinuations = 0
					if completeAfterTodoCheck("delivery retry exhausted after stream truncation") {
						continue
					}
					return
				}
				updateTodoContinuationProgress(incompletes)
				if noProgressContinuations >= maxNoProgressContinuations {
					if retryAt, ok := e.SoonestProviderRetry(); ok {
						if retry, stop := waitForProviderRetry(retryAt); retry {
							var streamErr error
							providerChunks, streamErr = e.retryStreamForToolResult(ctx, sessionID, messages, attempt)
							if streamErr != nil {
								slog.Error("todo continuation retry stream failed after cooldown",
									"session", sessionID,
									"error", streamErr,
								)
								if completeAfterTodoCheck("todo continuation retry failed after cooldown") {
									continue
								}
								return
							}
							attempt++
							identicalRun = 0
							lastFingerprint = ""
							sameToolPatternRun = 0
							lastToolNames = ""
							e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
							continue
						} else if stop {
							if completeAfterTodoCheck("todo continuation cooldown stop after stream truncation") {
								continue
							}
							return
						}
					}
					slog.Warn("todo continuation stopped: no progress with healthy providers",
						"session", sessionID,
						"no_progress_continuations", noProgressContinuations,
					)
					if completeAfterTodoCheck("todo continuation no progress after stream truncation") {
						continue
					}
					return
				}
				if todoContinuationCount >= maxTodoContinuations {
					slog.Warn("todo continuation budget exhausted after stream truncation",
						"session", sessionID,
						"max_continuations", maxTodoContinuations,
					)
					if completeAfterTodoCheck("todo continuation budget exhausted after stream truncation") {
						continue
					}
					return
				}
				todoContinuationCount++
				slog.Info("incomplete todos after stream truncation, injecting continuation",
					"session", sessionID,
					"incomplete_count", len(incompletes),
				)
				contMsg := buildTodoContinuationMessage(incompletes)
				messages = append(messages, contMsg)
				maybeCompactForRetry("todo continuation retry")
				e.resetContinuationState(sessionID)
				if allTodosTerminal(incompletes) {
					e.mu.Lock()
					e.todoContinuationFired[sessionID] = false
					e.mu.Unlock()
				}
				var streamErr error
				providerChunks, streamErr = e.retryStreamForToolResult(skipCtx(ctx), sessionID, messages, attempt)
				if streamErr != nil {
					slog.Error("todo continuation stream failed after truncation",
						"session", sessionID,
						"error", streamErr,
					)
					if completeAfterTodoCheck("todo continuation stream failed after stream truncation") {
						continue
					}
					return
				}
				attempt++
				iterations = 0
				identicalRun = 0
				lastFingerprint = ""
				sameToolPatternRun = 0
				lastToolNames = ""
				e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
				continue
			}
			if completeAfterTodoCheck("tool execution error") {
				continue
			}
			return
		}

		// Persist all tool_use blocks in a single assistant message before any
		// branch that can exit early (permission denied, ErrToolNotFound,
		// execute failure). The transcript must retain the model's intent even
		// when execution is skipped — otherwise the persisted session is
		// indistinguishable from the model having replied with no tool use at
		// all (session-1776623141279480382).
		e.storeAssistantToolUseBatch(result.toolCalls, result.responseContent)

		// Permission checks are sequential and fast — run them before launching
		// any goroutines so a denied call halts the whole batch cleanly. The
		// denied call is answered inside checkToolPermission; any earlier
		// calls that were allowed but never executed are answered here so
		// the persisted transcript carries no dangling tool_use.
		for i, tc := range result.toolCalls {
			if denied := e.checkToolPermission(tc, outChan); denied {
				for _, halted := range result.toolCalls[:i] {
					e.storeToolResult(halted, tool.Result{
						Output:  "Error: permission denial halted the batch before this tool ran",
						IsError: true,
						Error:   fmt.Errorf("batch halted by permission denial"),
					})
				}
				return
			}
		}

		toolExecStart := time.Now()
		watchdog.markToolExecStart()
		execResults, nonDelegatedExec := e.executeDeduplicatedToolCalls(ctx, sessionID, result.toolCalls, outChan)
		toolExecDuration += time.Since(toolExecStart)
		nonDelegatedToolExecDuration += nonDelegatedExec
		watchdog.markToolBatchComplete()

		// When a tool execution returns a hard error (not a tool-level Result.Error)
		// persist a synthetic tool_result so the session history has a complete
		// tool_call + tool_result pair. Without this the agent sees a dangling
		// tool_call on the next turn and forgets the failed attempt entirely.
		for _, er := range execResults {
			if er.err != nil {
				synthetic := tool.Result{Output: "Error: " + er.err.Error()}
				e.storeToolResult(er.toolCall, synthetic)
				emitStreamChunk(ctx, outChan, provider.StreamChunk{
					EventType:  "tool_result",
					ToolCallID: er.toolCall.ID,
					InternalToolCallID: e.toolCallCorrelator.InternalID(
						sessionID, er.toolCall.ID, er.toolCall.Name, er.toolCall.Arguments,
					),
					ToolResult: &provider.ToolResultInfo{
						Content: synthetic.Output,
						IsError: true,
					},
				})
				if e.requiresDeliveryToolCtx(ctx) && !e.deliveryToolCompleted(sessionID) {
					e.warnDeliveryToolBypassCtx(ctx, sessionID)
				}
				emitTerminalStreamChunk(ctx, outChan, e.terminalRecoveryChunk(watchdog, ctx, er.err))
				return
			}
		}

		// Persist results and emit tool_result chunks in deterministic order.
		for _, er := range execResults {
			e.storeToolResult(er.toolCall, er.toolResult)

			// Precedence (PR6 close-out for S3/C1, May 2026): the chunk
			// Content MUST prefer the tool's Output when it is non-empty,
			// regardless of whether IsError is also set.
			//
			// Why: rich error messages set their text on `Result.Output`
			// while keeping `Result.Error` populated with a sentinel
			// (e.g. tool.ErrToolNotFound). Examples shipped on this code
			// path:
			//   - Item 3 skill-name redirect: Output carries the
			//     `'X' is a skill, not a tool. Invoke it with
			//     skill_load(name="X").` recovery hint.
			//   - Fuzzy-suggest tool-not-found fallback: Output carries
			//     the `Available tools: [...]. Did you mean 'X'?`
			//     inventory + Levenshtein suggestion.
			//   - Custom tool failure messages: any tool that returns
			//     `Result{Output: humanText, Error: sentinel, IsError: true}`.
			// The pre-PR6 path overwrote Output with
			// `"Error: " + Error.Error()`, stripping every signal above
			// before the chunk reached the model and the UI.
			//
			// The PR5 shape (`Result{Error: err}` with empty Output —
			// real tools using the result-only-error pattern) still
			// needs to surface the error text. Fall back to the wrapped
			// `"Error: ..."` rendering only when Output is empty.
			//
			// Both spec harnesses (`executeToolCall` return value and
			// the Stream chunk observer) now agree on what consumers
			// see: the rich Output text when there is one, the
			// `"Error: ..."` shorthand when there is not.
			resultContent := er.toolResult.Output
			isError := er.toolResult.Error != nil || er.toolResult.IsError
			if resultContent == "" && er.toolResult.Error != nil {
				resultContent = "Error: " + er.toolResult.Error.Error()
			}
			// Strip the delegation `<task_result>` wrapper from the
			// chunk that feeds SSE consumers and the session accumulator.
			// The wrapper is the LLM-visible boundary marker
			// formatDelegationOutput emits — useful for the next-turn
			// LLM prompt (preserved via tool.Result.Output in
			// appendToolResultsBatchToMessages and storeToolResult)
			// but pure noise in the chat bubble. Session 2d8dc0ac
			// messages 167/178/183/188 captured the leak (May 2026
			// chat-UI leak triage).
			resultContent = UnwrapTaskResult(resultContent)
			// P14: re-resolve the internal id so the tool_result chunk carries
			// the same InternalToolCallID as the originating tool_call.
			emitStreamChunk(ctx, outChan, provider.StreamChunk{
				EventType:  "tool_result",
				ToolCallID: er.toolCall.ID,
				InternalToolCallID: e.toolCallCorrelator.InternalID(
					sessionID, er.toolCall.ID, er.toolCall.Name, er.toolCall.Arguments,
				),
				ToolResult: &provider.ToolResultInfo{
					Content: resultContent,
					IsError: isError,
				},
			})
		}

		toolResults := make([]tool.Result, len(execResults))
		batchAllRejected := len(execResults) > 0
		for i, er := range execResults {
			toolResults[i] = er.toolResult
			if !er.toolResult.IsError && er.toolResult.Error == nil {
				batchAllRejected = false
			} else if !strings.Contains(er.toolResult.Output, "not available to agent") &&
				!strings.Contains(er.toolResult.Output, "must load your always-active skills via") {
				batchAllRejected = false
			}
		}
		if batchAllRejected {
			consecutiveRejectedToolCalls++
		} else {
			consecutiveRejectedToolCalls = 0
		}
		if batchGuardOutput(execResults) != "" &&
			strings.Contains(batchGuardOutput(execResults),
				"must load your always-active skills via") &&
			consecutiveRejectedToolCalls >= maxRejectedToolCalls {
			skillsGuardRejectionLoop = true
		}
		messages = e.appendToolResultsBatchToMessages(messages, result.toolCalls, toolResults)

		// Phase-5 Slice γ — mid-tool-loop refresh. Emits a fresh
		// context_usage chunk so the chip tracks the swelling
		// tool-result wave between batches, AND fires the
		// tool-result-wave compaction trigger when the persisted
		// store crosses the gate-proximity boundary. Without this
		// hook the chip stays stale until terminal Done and
		// maybeAutoCompact never fires from the tool-loop path
		// (retryStreamForToolResult skips buildContextWindow).
		//
		// Bug #35 — when emitMidToolLoopRefresh reports compaction
		// fired, reload `messages` from the post-compaction view so
		// the next provider request sees the compressed slice rather
		// than the swollen pre-compaction prefix. The no-fire branch
		// returns false and we skip the reload — buildContextWindow
		// is not free.
		compacted := e.emitMidToolLoopRefresh(ctx, sessionID, outChan, messages)
		if compacted {
			if rebuilt := e.rebuildContextWindowAfterMidLoopCompaction(ctx, sessionID, messages); rebuilt != nil {
				messages = rebuilt
			}
		}

		attempt++

		// Tool-loop cap (continuation path only). We are about to re-request
		// the provider with the just-computed tool results. A legitimately
		// completing turn never reaches here — it returns at the result.done
		// or len(toolCalls)==0 early-exits above. So tripping a cap here can
		// only fire on the re-request path, never on a clean completion.
		//
		// Layered guards (all active):
		//   1. Repeat detection (primary): fingerprint this batch as
		//      (tool name + canonicalised args); if the SAME fingerprint
		//      recurs maxIdenticalToolCalls consecutive iterations, trip.
		//   2. Fixed-N backstop: an absolute ceiling of maxToolLoopIterations
		//      total continuations, trip regardless of fingerprint. Covers
		//      stuck loops the fingerprint can't catch (e.g. cycling args).
		//   3. Duration backstop: a cumulative wall-clock ceiling of
		//      maxToolLoopDuration since loopStart, independent of iteration
		//      count. Catches slow-but-varied loops that never repeat and
		//      never hit the iteration ceiling. Added June 2026.
		//   4. Same-tool-pattern detector: counts consecutive
		//      continuations where the SAME set of tool-call names
		//      recurs, regardless of the assistant response text
		//      content — narration alongside an identical tool batch is
		//      not progress; trips at maxSameToolPatternCalls. Catches
		//      stalls where varied tool args dodge the fingerprint and
		//      the low count dodges the iteration backstop.
		// Zero/negative on any field disables its respective check.
		iterations++
		watchdog.recordIterations(iterations)
		// Deadline-exemption (F2): when the previous batch's results
		// contain a tool-deadline kill (bash.ErrDeadlineExceeded), the
		// model re-issuing the same batch is a legitimate retry of a
		// long-running command, not a stuck loop. Reset both repeat
		// detectors so the retry run starts from a clean slate.
		deadlineKilled := batchDeadlineKilled(toolResults)
		fingerprint := fingerprintToolBatch(result.toolCalls)
		if e.maxIdenticalToolCalls > 0 {
			if deadlineKilled {
				identicalRun = 0
				lastFingerprint = ""
			} else if fingerprint != "" && fingerprint == lastFingerprint {
				identicalRun++
			} else {
				identicalRun = 1
			}
			if !deadlineKilled {
				lastFingerprint = fingerprint
			}
		}

		if e.maxSameToolPatternCalls > 0 {
			if len(result.toolCalls) > 0 && batchAllFailed(toolResults) {
				names := make([]string, len(result.toolCalls))
				for i, tc := range result.toolCalls {
					names[i] = tc.Name
				}
				sort.Strings(names)
				currentNames := strings.Join(names, ",")
				if deadlineKilled {
					sameToolPatternRun = 0
					lastToolNames = ""
				} else if currentNames == lastToolNames {
					sameToolPatternRun++
				} else {
					sameToolPatternRun = 1
				}
				if !deadlineKilled {
					lastToolNames = currentNames
				}
			} else {
				sameToolPatternRun = 0
				lastToolNames = ""
			}
		}

		wallElapsed := time.Since(loopStart)
		elapsed := wallElapsed - toolExecDuration
		repeatTripped := e.maxIdenticalToolCalls > 0 && identicalRun >= e.maxIdenticalToolCalls
		backstopTripped := e.maxToolLoopIterations > 0 && iterations >= e.maxToolLoopIterations
		durationTripped := e.maxToolLoopDuration > 0 && elapsed >= e.maxToolLoopDuration
		wallTripped := e.maxToolLoopDuration > 0 && wallElapsed >= toolLoopWallClockMultiplier*e.maxToolLoopDuration
		durationTripped = durationTripped || wallTripped
		totalToolTimeTripped := e.maxToolLoopDuration > 0 && nonDelegatedToolExecDuration >= e.maxToolLoopDuration
		sameToolTripped := e.maxSameToolPatternCalls > 0 && sameToolPatternRun >= e.maxSameToolPatternCalls
		rejectionTripped := consecutiveRejectedToolCalls >= maxRejectedToolCalls
		if skillsGuardRejectionLoop {
			e.warnDeliveryToolBypassCtx(ctx, sessionID)
			emitTerminalStreamChunk(ctx, outChan, provider.StreamChunk{
				Done:       true,
				StopReason: session.StopReasonToolLoopExceeded,
				ModelID:    e.lastModelCtx(ctx),
				ProviderID: e.lastProviderCtx(ctx),
			})
			return
		}
		softTripped := (backstopTripped || durationTripped || totalToolTimeTripped) && !repeatTripped && !sameToolTripped && !rejectionTripped && softTripContinuations < maxSoftTripContinuations
		if softTripped && !compactSoftContinuation() {
			softTripped = false
		}
		if softTripped {
			if totalToolTimeTripped {
				watchdog.markToolTimeBudgetExhausted()
			}
			softTripContinuations++
			iterations = 0
			// loopStart / toolExecDuration / nonDelegatedToolExecDuration
			// are deliberately NOT reset: resetting them would zero the
			// duration budget on every soft continuation and the loop
			// would never reach forcedSummaryTerminal after
			// maxSoftTripContinuations.
			slog.Info("tool loop soft cap tripped without loop signature, continuing",
				"session", sessionID,
				"continuations", softTripContinuations)
		}
		if !softTripped && (repeatTripped || backstopTripped || durationTripped || totalToolTimeTripped || sameToolTripped || rejectionTripped) {
			reason := "iteration_backstop"
			if repeatTripped {
				reason = "identical_call_repeat"
			} else if durationTripped {
				reason = "duration_backstop"
			} else if totalToolTimeTripped {
				reason = "total_tool_time_backstop"
			} else if sameToolTripped {
				reason = "same_tool_pattern"
			} else if rejectionTripped {
				reason = "consecutive_tool_rejection"
			}
			slog.Warn("engine tool loop capped",
				"session", sessionID,
				"trip", reason,
				"iterations", iterations,
				"identical_run", identicalRun,
				"same_tool_run", sameToolPatternRun,
				"elapsed", elapsed,
				"wall_elapsed", wallElapsed,
				"non_delegated_tool_time", nonDelegatedToolExecDuration,
				"max_iterations", e.maxToolLoopIterations,
				"max_duration", e.maxToolLoopDuration,
				"max_identical", e.maxIdenticalToolCalls,
				"max_same_tool", e.maxSameToolPatternCalls,
			)
			if durationTripped || totalToolTimeTripped {
				slog.Info("turn time budget exhausted, ending turn through forced summary",
					"session", sessionID,
					"trip", reason,
				)
				// Mark the turn fired so recovery exits stamp the
				// tool_loop_exceeded sentinel on the terminal chunk.
				watchdog.markFired()
				if forcedSummaryTerminal(reason) {
					continue
				}
				return
			}
			todosAllComplete := false
			if e.todoStore != nil {
				if hasMore, _ := e.hasIncompleteTodos(sessionID); !hasMore {
					todosAllComplete = true
				}
			}
			if !delegationGraceUsed && !durationTripped && !totalToolTimeTripped && (batchContainsDelegate(result.toolCalls) || (todosAllComplete && !e.hasActiveBackgroundTasks(sessionID))) {
				delegationGraceUsed = true
				slog.Info("delegation grace round: model delegated work with no incomplete todos",
					"session", sessionID,
					"trip", reason,
				)
				contMsg := buildGraceRoundMessage()
				messages = append(messages, contMsg)
				var streamErr error
				providerChunks, streamErr = e.retryStreamForToolResult(ctx, sessionID, messages, attempt)
				if streamErr != nil {
					slog.Error("grace round stream failed after tool loop cap",
						"session", sessionID,
						"error", streamErr,
					)
					if completeAfterTodoCheck("grace round stream failed after tool loop cap") {
						continue
					}
					return
				}
				attempt++
				iterations = 0
				identicalRun = 0
				lastFingerprint = ""
				sameToolPatternRun = 0
				lastToolNames = ""
				e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
				continue
			}
			if hasMore, incompletes := e.hasIncompleteTodos(sessionID); hasMore && !sawContextOverflow && !naiveOverflow {
				deliveryStopped := false
				switch maybeRetryDelivery(func() {
					deliveryStopped = true
				}) {
				case deliveryRetryContinue:
					continue
				case deliveryRetryStop:
					break
				}
				if deliveryStopped {
					noProgressContinuations = 0
					if doneAfterTodoCheck("tool loop cap after incomplete todos") {
						continue
					}
					return
				}
				updateTodoContinuationProgress(incompletes)
				if noProgressContinuations >= maxNoProgressContinuations {
					if retryAt, ok := e.SoonestProviderRetry(); ok {
						if retry, stop := waitForProviderRetry(retryAt); retry {
							var streamErr error
							providerChunks, streamErr = e.retryStreamForToolResult(ctx, sessionID, messages, attempt)
							if streamErr != nil {
								slog.Error("todo continuation retry stream failed after cooldown",
									"session", sessionID,
									"error", streamErr,
								)
								if doneAfterTodoCheck("todo continuation retry failed after cooldown") {
									continue
								}
								return
							}
							attempt++
							identicalRun = 0
							lastFingerprint = ""
							sameToolPatternRun = 0
							lastToolNames = ""
							e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
							continue
						} else if stop {
							if doneAfterTodoCheck("todo continuation cooldown stop after tool loop cap") {
								continue
							}
							return
						}
					}
					slog.Warn("todo continuation stopped: no progress with healthy providers",
						"session", sessionID,
						"no_progress_continuations", noProgressContinuations,
					)
					if doneAfterTodoCheck("todo continuation no progress after tool loop cap") {
						continue
					}
					return
				}
				// Guard: consecutive same-tool-pattern caps across continuation
				// boundaries means the model is stuck repeating the same tool.
				// One recovery is allowed; a second consecutive cap is a pattern —
				// stop through the forced-summary terminal without injecting
				// another continuation.
				if reason == "same_tool_pattern" {
					consecutiveSameToolContinuations++
					if consecutiveSameToolContinuations >= 2 {
						slog.Warn("consecutive same-tool-pattern caps, stopping",
							"session", sessionID,
							"consecutive", consecutiveSameToolContinuations,
						)
						if forcedSummaryTerminal("consecutive same-tool-pattern caps") {
							continue
						}
						return
					}
				} else {
					consecutiveSameToolContinuations = 0
				}
				if todoContinuationCount >= maxTodoContinuations {
					slog.Warn("todo continuation budget exhausted after tool loop cap",
						"session", sessionID,
						"trip", reason,
						"max_continuations", maxTodoContinuations,
					)
					if doneAfterTodoCheck("todo continuation budget exhausted after tool loop cap") {
						continue
					}
					return
				}
				todoContinuationCount++
				slog.Info("tool loop capped but incomplete todos remain, injecting continuation",
					"session", sessionID,
					"trip", reason,
					"incomplete_count", len(incompletes),
				)
				contMsg := buildTodoContinuationMessage(incompletes)
				messages = append(messages, contMsg)
				e.resetContinuationState(sessionID)
				var streamErr error
				providerChunks, streamErr = e.retryStreamForToolResult(ctx, sessionID, messages, attempt)
				if streamErr != nil {
					slog.Error("todo continuation stream failed after tool loop cap",
						"session", sessionID,
						"error", streamErr,
					)
					if completeAfterTodoCheck("todo continuation stream failed after tool loop cap") {
						continue
					}
					return
				}
				attempt++
				iterations = 0
				identicalRun = 0
				lastFingerprint = ""
				sameToolPatternRun = 0
				lastToolNames = ""
				e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
				continue
			} else if activeTasks := e.activeBackgroundTaskCount(sessionID); activeTasks > 0 {
				if backgroundContinuationCount >= maxBackgroundContinuations {
					slog.Warn("background continuation budget exhausted",
						"session", sessionID,
						"active_tasks", activeTasks,
						"max_continuations", maxBackgroundContinuations,
					)
					if doneAfterTodoCheck("background continuation budget exhausted") {
						continue
					}
					return
				}
				backgroundContinuationCount++
				slog.Info("background tasks still active, continuing",
					"session", sessionID,
					"active_tasks", activeTasks,
				)
				contMsg := buildBackgroundTaskContinuationMessage(activeTasks)
				messages = append(messages, contMsg)
				e.resetContinuationState(sessionID)
				var streamErr error
				providerChunks, streamErr = e.retryStreamForToolResult(ctx, sessionID, messages, attempt)
				if streamErr != nil {
					slog.Error("background task continuation stream failed",
						"session", sessionID,
						"error", streamErr,
					)
					if completeAfterTodoCheck("background task continuation stream failed") {
						continue
					}
					return
				}
				attempt++
				iterations = 0
				identicalRun = 0
				lastFingerprint = ""
				sameToolPatternRun = 0
				lastToolNames = ""
				e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
				continue
			} else if !finalResponseGraceUsed && !durationTripped && !totalToolTimeTripped && delegationGraceUsed &&
				!batchContainsDelegate(result.toolCalls) &&
				strings.TrimSpace(result.responseContent) == "" {
				finalResponseGraceUsed = true
				slog.Info("final-response grace round: post-delegation synthesis budget exhausted",
					"session", sessionID,
					"trip", reason,
				)
				contMsg := buildFinalResponseMessage()
				messages = append(messages, contMsg)
				var streamErr error
				providerChunks, streamErr = e.retryStreamForToolResult(ctx, sessionID, messages, attempt)
				if streamErr != nil {
					slog.Error("final-response grace round stream failed",
						"session", sessionID,
						"error", streamErr,
					)
					if completeAfterTodoCheck("final-response grace round stream failed") {
						continue
					}
					return
				}
				attempt++
				iterations = 0
				identicalRun = 0
				lastFingerprint = ""
				sameToolPatternRun = 0
				lastToolNames = ""
				e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
				continue
			} else if !forcedSummaryUsed {
				forcedSummaryUsed = true
				slog.Info("forced summary round: tool budget exhausted, stripping tools",
					"session", sessionID,
					"trip", reason,
				)
				contMsg := buildFinalResponseMessage()
				messages = append(messages, contMsg)
				var streamErr error
				providerChunks, streamErr = e.retryStreamForToolResultNoSchemas(ctx, sessionID, messages, attempt)
				if streamErr != nil {
					slog.Error("forced summary round stream failed",
						"session", sessionID,
						"error", streamErr,
					)
					if completeAfterTodoCheck("forced summary round stream failed") {
						continue
					}
					return
				}
				attempt++
				e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
				continue
			} else {
				e.persistCappedTurnTerminal(ctx, sessionID, reason)
				e.warnDeliveryToolBypassCtx(ctx, sessionID)
				emitTerminalStreamChunk(ctx, outChan, provider.StreamChunk{
					Done:       true,
					StopReason: session.StopReasonToolLoopExceeded,
					ModelID:    e.lastModelCtx(ctx),
					ProviderID: e.lastProviderCtx(ctx),
				})
				return
			}
		}

		var streamErr error
		providerChunks, streamErr = e.retryStreamForToolResult(ctx, sessionID, messages, attempt)
		if streamErr != nil {
			if watchdog.hasFired() && watchdog.claimTerminal() {
				e.warnDeliveryToolBypassCtx(ctx, sessionID)
				emitTerminalStreamChunk(ctx, outChan, provider.StreamChunk{
					Done:       true,
					StopReason: session.StopReasonToolLoopExceeded,
					ModelID:    e.lastModelCtx(ctx),
					ProviderID: e.lastProviderCtx(ctx),
				})
				return
			}
			if e.requiresDeliveryToolCtx(ctx) && !e.deliveryToolCompleted(sessionID) {
				e.warnDeliveryToolBypassCtx(ctx, sessionID)
			}
			emitTerminalStreamChunk(ctx, outChan, e.terminalRecoveryChunk(watchdog, ctx, streamErr))
			return
		}

		// Bug #36 — post-retry context_usage refresh. The next
		// provider stream may take seconds to produce its first
		// chunk. Without this emission the chip holds the stale
		// figure across that gap because emitMidToolLoopRefresh wrote
		// against a different message shape (no tools schema, no
		// post-reload payload). tryEmitContextUsage coalesces when
		// the resulting payload happens to be byte-identical to the
		// last emission for this session.
		e.emitPostRetryContextUsage(ctx, sessionID, messages, outChan)
	}
}

// batchAllFailed reports whether every result in the batch represents a failed
// tool execution, i.e. no result succeeded.
//
// Returns: true when all results are errors.
// Expected: the executed batch's results slice.
// Side effects: None.
func batchAllFailed(results []tool.Result) bool {
	if len(results) == 0 {
		return false
	}
	for _, result := range results {
		if result.Error == nil && !result.IsError {
			return false
		}
	}
	return true
}

// deliveryFailureEnvelope is the JSON shape for a delivery failure event payload.
type deliveryFailureEnvelope struct {
	Status                  string                `json:"status"`
	SessionID               string                `json:"session_id"`
	AgentID                 string                `json:"agent_id"`
	ChainID                 string                `json:"chain_id,omitempty"`
	RequiredDeliveryTools   []string              `json:"required_delivery_tools,omitempty"`
	ExpectedCoordinationKey string                `json:"expected_coordination_key,omitempty"`
	FailureSummary          string                `json:"failure_summary"`
	MessageCount            int                   `json:"message_count"`
	RequestBytes            int                   `json:"request_bytes"`
	LastAssistantText       string                `json:"last_assistant_text,omitempty"`
	Timestamp               string                `json:"timestamp"`
	FailureDetail           *failureSummaryDetail `json:"failure_detail,omitempty"`
}

// failureSummaryDetail captures structured diagnostic context for a delivery
// failure, complementing the free-form failure_summary field.
type failureSummaryDetail struct {
	Step      string `json:"step,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	LastError string `json:"last_error,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// truncateFailureText clips a diagnostic string to a bounded length so the
// persisted envelope stays compact.
//
// Expected: a diagnostic string to clip.
//
// Returns: the input string clipped to 256 bytes if it exceeds that length,
// otherwise the input unchanged.
//
// Side effects: None.
func truncateFailureText(s string) string {
	if len(s) > 256 {
		cut := 256
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return s[:cut]
	}
	return s
}

// persistDeliveryFailureFallback ...
//
// Expected: parameters for persistDeliveryFailureFallback.
//
// Returns: result of persistDeliveryFailureFallback.
//
// Side effects: None.
func (e *Engine) persistDeliveryFailureFallback(ctx context.Context, sessionID string, messages []provider.Message, cause error) {
	if e == nil || cause == nil || !isTerminalDeliveryProviderFailure(cause) {
		return
	}
	if !e.requiresDeliveryToolCtx(ctx) || e.deliveryToolCompleted(sessionID) {
		return
	}
	agentID, deliveryTools := e.deliveryFallbackContext(ctx)
	if !slices.Contains(deliveryTools, "coordination_store") {
		return
	}
	coordTool := e.lookupTool("coordination_store")
	if coordTool == nil {
		return
	}
	scope := swarm.MemberCoordChainID(ctx)
	if scope == "" {
		scope = sessionID
	}
	if scope == "" {
		return
	}
	reservedKey := scope + "/_engine_fallback/" + agentID + "/delivery_failure"
	now := time.Now().UTC().Format(time.RFC3339)
	envelope := deliveryFailureEnvelope{
		Status:                  "delivery_failed_engine_fallback",
		SessionID:               sessionID,
		AgentID:                 agentID,
		ChainID:                 swarm.MemberCoordChainID(ctx),
		RequiredDeliveryTools:   append([]string(nil), deliveryTools...),
		ExpectedCoordinationKey: expectedCoordinationKey(agentID, swarm.MemberCoordChainID(ctx)),
		FailureSummary:          cause.Error(),
		MessageCount:            len(messages),
		RequestBytes:            estimateMessageBytes(messages),
		LastAssistantText:       lastAssistantText(messages),
		Timestamp:               now,
		FailureDetail: &failureSummaryDetail{
			Step:      "delivery",
			Provider:  e.lastProviderCtx(ctx),
			Model:     e.lastModelCtx(ctx),
			LastError: truncateFailureText(cause.Error()),
			Timestamp: now,
		},
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		slog.Warn("delivery fallback persist skipped: marshal failed", "session", sessionID, "error", err)
		return
	}
	result, execErr := coordTool.Execute(ctx, tool.Input{
		Name: "coordination_store",
		Arguments: map[string]interface{}{
			"operation": "set",
			"key":       reservedKey,
			"value":     string(payload),
		},
	})
	if execErr != nil || result.Error != nil || result.IsError {
		slog.Warn("delivery fallback persist failed",
			"session", sessionID,
			"agent", agentID,
			"key", reservedKey,
			"error", coalesceToolError(execErr, result))
		return
	}
	slog.Warn("delivery fallback persisted",
		"session", sessionID,
		"agent", agentID,
		"key", reservedKey)
}

// deliveryFallbackContext ...
//
// Expected: parameters for deliveryFallbackContext.
//
// Returns: result of deliveryFallbackContext.
//
// Side effects: None.
func (e *Engine) deliveryFallbackContext(ctx context.Context) (string, []string) {
	if m, ok := manifestFromContext(ctx); ok {
		return m.ID, append([]string(nil), m.Capabilities.DeliveryTools...)
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.manifest.ID, append([]string(nil), e.manifest.Capabilities.DeliveryTools...)
}

// lookupTool ...
//
// Expected: parameters for lookupTool.
//
// Returns: result of lookupTool.
//
// Side effects: None.
func (e *Engine) lookupTool(name string) tool.Tool {
	for _, t := range e.tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

// isTerminalDeliveryProviderFailure ...
//
// Expected: parameters for isTerminalDeliveryProviderFailure.
//
// Returns: result of isTerminalDeliveryProviderFailure.
//
// Side effects: None.
func isTerminalDeliveryProviderFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "all providers failed") || strings.Contains(msg, "no healthy providers available")
}

// estimateMessageBytes ...
//
// Expected: parameters for estimateMessageBytes.
//
// Returns: result of estimateMessageBytes.
//
// Side effects: None.
func estimateMessageBytes(messages []provider.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Role) + len(msg.Content)
		for _, tc := range msg.ToolCalls {
			total += len(tc.ID) + len(tc.Name)
			if args, err := json.Marshal(tc.Arguments); err == nil {
				total += len(args)
			}
		}
	}
	return total
}

// lastAssistantText ...
//
// Expected: parameters for lastAssistantText.
//
// Returns: result of lastAssistantText.
//
// Side effects: None.
func lastAssistantText(messages []provider.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
	}
	return ""
}

// coalesceToolError ...
//
// Expected: parameters for coalesceToolError.
//
// Returns: result of coalesceToolError.
//
// Side effects: None.
func coalesceToolError(execErr error, result tool.Result) error {
	if execErr != nil {
		return execErr
	}
	if result.Error != nil {
		return result.Error
	}
	if result.IsError {
		return errors.New("tool returned IsError=true")
	}
	return errors.New("coordination fallback persist failed")
}

// fingerprintToolBatch produces a deterministic signature for a batch of
// tool calls so the tool-loop repeat detector can recognise the SAME request
// recurring across continuations. The signature is the ordered list of
// (tool name + canonicalised arguments) for every call in the batch.
//
// Arguments are canonicalised by compact-JSON of a key-sorted copy of the
// arg map (json.Marshal already emits map keys in sorted order), so two
// semantically identical calls whose maps were assembled in different orders
// fingerprint identically. The batch order is preserved deliberately: a
// provider that re-emits the same multi-call batch is just as stuck as one
// re-emitting a single call. An empty batch returns "" — the caller treats a
// blank fingerprint as "cannot fingerprint", never matching the previous run
// (the absolute backstop still bounds those turns).
//
// Expected: parameters for fingerprintToolBatch.
// Returns: result of fingerprintToolBatch.
// Side effects: None.
func fingerprintToolBatch(toolCalls []*provider.ToolCall) string {
	if len(toolCalls) == 0 {
		return ""
	}
	var b strings.Builder
	for i, tc := range toolCalls {
		if tc == nil {
			continue
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(tc.Name)
		b.WriteByte('|')
		if args, err := json.Marshal(tc.Arguments); err == nil {
			b.Write(args)
		} else {
			// Defensive: an un-marshalable arg map is rare, but fall back to
			// the Go-syntax rendering so distinct args still fingerprint
			// distinctly rather than collapsing to a shared "".
			fmt.Fprintf(&b, "%#v", tc.Arguments)
		}
	}
	return b.String()
}

// batchContainsDelegate returns true if any of the provided tool calls is a
// "delegate" call. Used by the grace round to detect whether the model
// delegated work in its last turn and should be given another round to
// receive the results.
//
// Expected: parameters for batchContainsDelegate.
// Returns: result of batchContainsDelegate.
// Side effects: None.
func batchContainsDelegate(toolCalls []*provider.ToolCall) bool {
	for _, tc := range toolCalls {
		if tc != nil && tc.Name == "delegate" {
			return true
		}
	}
	return false
}

// buildGraceRoundMessage renders a user-role message telling the model that
// the tool loop budget is exhausted but a delegation grace round has been
// granted so it can check back on delegated work.
//
// Returns: result of buildGraceRoundMessage.
// Side effects: None.
func buildGraceRoundMessage() provider.Message {
	return provider.Message{
		Role:    "user",
		Content: "Your tool loop budget is exhausted but you delegated work. A grace round has been granted so you can receive the results. Check back on your delegated tasks now.",
	}
}

// buildFinalResponseMessage constructs a message that prompts the model
// to produce a final summary when the tool loop budget is exhausted and
// the model has been making tool calls without producing user-visible
// response text. This gives the model one last turn to synthesise what
// it accomplished before the session terminates.
//
// Returns: result of buildFinalResponseMessage.
// Side effects: None.
func buildFinalResponseMessage() provider.Message {
	return provider.Message{
		Role:    "user",
		Content: "Your tool loop budget is exhausted. Please provide a final summary of what you've accomplished — the results of each tool call are still visible in the conversation above.",
	}
}

// buildCappedTurnTerminalContent renders the engine-synthesised terminal
// assistant message persisted when a capped turn ends without a
// model-produced summary. It names the stop reason and the incomplete todo
// state so the transcript records why the turn ended and what remains
// outstanding.
//
// Expected: reason is the tool-loop cap trip reason; incomplete is the
// snapshot of todo items still outstanding at the terminal decision.
// Returns: the terminal message content.
// Side effects: None.
func buildCappedTurnTerminalContent(reason string, incomplete []todo.Item) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Turn stopped by the engine (%s).", reason))
	if len(incomplete) == 0 {
		sb.WriteString(" No incomplete todos remain.")
		return sb.String()
	}
	sb.WriteString(fmt.Sprintf(" %d incomplete todo(s) remain:", len(incomplete)))
	for _, it := range incomplete {
		sb.WriteString(fmt.Sprintf("\n- [%s] %s", it.Status, it.Content))
	}
	return sb.String()
}

// persistCappedTurnTerminal stores the engine-synthesised terminal assistant
// message for a capped turn so the persisted transcript never ends on a
// dangling tool result. The message carries the tool_loop_exceeded stop
// reason stamp alongside a short structured summary of the terminal state.
//
// Expected: ctx is the turn context; sessionID identifies the active
// session; reason is the tool-loop cap trip or todo-machinery stop reason.
// Returns: None.
// Side effects: appends the terminal assistant message to the context
// store and chain store when configured, embeds it when an embedding
// provider is wired, and logs the persistence.
func (e *Engine) persistCappedTurnTerminal(ctx context.Context, sessionID, reason string) {
	var incomplete []todo.Item
	if e.todoStore != nil {
		if has, items := e.hasIncompleteTodos(sessionID); has {
			incomplete = items
		}
	}
	content := buildCappedTurnTerminalContent(reason, incomplete)
	if e.store == nil {
		return
	}
	msg := provider.Message{
		Role:       "assistant",
		Content:    content,
		StopReason: session.StopReasonToolLoopExceeded,
		ModelID:    e.LastModel(),
	}
	msgID := e.store.AppendReturningID(msg)
	e.dualWriteToChainStore(ctx, msg)
	e.embedMessage(ctx, content, msgID)
	slog.Info("persisted terminal assistant message for capped turn",
		"session", sessionID,
		"reason", reason,
	)
}

// deduplicateToolCalls collapses identical tool calls (same name + canonical
// arguments) within a single batch so that the engine executes only the first
// occurrence of each unique call and replicates the result to all duplicates.
//
// Background: Z.AI glm-5.2 has been observed (session c5c93b09, June 2026)
// generating 9 identical todo_update calls (all with input {}) in a single
// turn. Without deduplication the engine fan-outs all 9 goroutines, executes
// the same tool 9 times, and wastes ~15 KB of token space on identical results
// plus ~200 ms per execution. This is distinct from fingerprintToolBatch,
// which detects the same batch recurring across DIFFERENT tool-loop turns.
//
// Returns:
//   - unique: the deduplicated list (first occurrence of each unique call kept)
//   - mapping: for each index in the original list, the index in unique to
//     replicate its result from. Callers use: result[i] = result[unique[mapping[i]]]
//
// An empty input returns empty slices. Input with no duplicates returns unique
// as a copy of the input and mapping as [0, 1, 2, ..., n-1].
//
// Expected: parameters for deduplicateToolCalls.
// Side effects: None.
func deduplicateToolCalls(toolCalls []*provider.ToolCall) (unique []*provider.ToolCall, mapping []int) {
	if len(toolCalls) == 0 {
		return nil, nil
	}

	type key struct {
		name string
		args string // canonical JSON
	}
	seen := make(map[key]int) // first occurrence index in unique
	unique = make([]*provider.ToolCall, 0, len(toolCalls))
	mapping = make([]int, len(toolCalls))

	for i, tc := range toolCalls {
		if tc == nil {
			// Nil entries are treated as unique (should not occur in practice).
			mapping[i] = len(unique)
			unique = append(unique, nil)
			continue
		}
		args, err := json.Marshal(tc.Arguments)
		if err != nil {
			// Un-marshalable args: treat as unique on the string rendering.
			args = []byte(fmt.Sprintf("%#v", tc.Arguments))
		}
		k := key{name: tc.Name, args: string(args)}
		if idx, ok := seen[k]; ok {
			mapping[i] = idx
		} else {
			idx = len(unique)
			seen[k] = idx
			mapping[i] = idx
			unique = append(unique, tc)
		}
	}
	return unique, mapping
}

// toolCallExecResult holds the outcome of a single tool call execution. The
// nonDelegatedExec field carries the call's wall-clock execution time unless
// the resolved tool is delegation-shaped (see InheritsParentToolDeadline),
// in which case it is zero.
type toolCallExecResult struct {
	toolCall         *provider.ToolCall
	toolResult       tool.Result
	err              error
	nonDelegatedExec time.Duration
}

// executeDeduplicatedToolCalls deduplicates identical tool calls within a
// batch, executes the unique set, and replicates results back to every
// original index so downstream consumers see one result per original tool
// call with its correct ID. This prevents wasted executions when a provider
// emits multiple identical tool calls in a single turn (observed with Z.AI
// glm-5.2 generating 9 identical todo_update calls).
//
// Expected: parameters for executeDeduplicatedToolCalls.
// Returns: the per-call results plus the cumulative non-delegated execution
// time of the unique executions, for the caller's total-tool-time backstop.
// Side effects: None.
func (e *Engine) executeDeduplicatedToolCalls(
	ctx context.Context, sessionID string, toolCalls []*provider.ToolCall, outChan chan<- provider.StreamChunk,
) ([]toolCallExecResult, time.Duration) {
	uniqCalls, dedupMapping := deduplicateToolCalls(toolCalls)
	if len(uniqCalls) < len(toolCalls) {
		slog.Warn("deduplicated identical tool calls before execution",
			"session", sessionID,
			"total", len(toolCalls),
			"unique", len(uniqCalls),
		)
	}
	execResults, nonDelegatedExec := e.executeToolCallBatch(ctx, sessionID, uniqCalls, outChan)
	if len(uniqCalls) == len(toolCalls) {
		return execResults, nonDelegatedExec
	}
	fullResults := make([]toolCallExecResult, len(toolCalls))
	for i, repIdx := range dedupMapping {
		fullResults[i] = toolCallExecResult{
			toolCall:   toolCalls[i],
			toolResult: execResults[repIdx].toolResult,
			err:        execResults[repIdx].err,
		}
	}
	return fullResults, nonDelegatedExec
}

// batchGuardOutput concatenates the tool-result outputs of a batch so
// callers can classify which guard produced a rejection without
// inspecting individual entries.
//
// Expected: execResults is the batch whose outputs are being classified.
// Returns: the concatenation of every tool-result output in the batch.
// Side effects: None.
func batchGuardOutput(execResults []toolCallExecResult) string {
	var sb strings.Builder
	for _, er := range execResults {
		sb.WriteString(er.toolResult.Output)
	}
	return sb.String()
}

// executeToolCallBatch runs a batch of tool calls, sequentially when any
// target modifies shared state and in parallel otherwise.
//
// Expected: ctx carries the stream output channel, sessionID identifies
// the session, toolCalls is the batch to execute, outChan receives
// streaming chunks.
// Returns: one toolCallExecResult per call, in input order.
// Side effects: executes each tool call, which may have its own side
// effects, and emits tool lifecycle chunks to outChan.
func (e *Engine) executeToolCallBatch(
	ctx context.Context, sessionID string, toolCalls []*provider.ToolCall, outChan chan<- provider.StreamChunk,
) ([]toolCallExecResult, time.Duration) {
	results := make([]toolCallExecResult, len(toolCalls))
	switch {
	case len(toolCalls) == 1:
		results[0] = e.executeToolExecStage(WithStreamOutput(ctx, outChan), sessionID, toolCalls[0])
	case batchHasStateModifier(e.tools, toolCalls):
		for i, tc := range toolCalls {
			results[i] = e.executeToolExecStage(WithStreamOutput(ctx, outChan), sessionID, tc)
		}
	default:
		var wg sync.WaitGroup
		for i, tc := range toolCalls {
			wg.Add(1)
			go func(i int, tc *provider.ToolCall) {
				defer wg.Done()
				results[i] = e.executeToolExecStage(WithStreamOutput(ctx, outChan), sessionID, tc)
			}(i, tc)
		}
		wg.Wait()
	}
	var nonDelegatedExec time.Duration
	for i := range results {
		nonDelegatedExec += results[i].nonDelegatedExec
	}
	return results, nonDelegatedExec
}

// batchHasStateModifier reports whether any tool call in the batch targets a
// tool that implements tool.StateModifier with IsStateModifying returning true.
// When this returns true the caller must execute the batch sequentially to
// prevent race conditions between concurrent state mutations.
//
// Expected: parameters for batchHasStateModifier.
// Returns: result of batchHasStateModifier.
// Side effects: None.
func batchHasStateModifier(tools []tool.Tool, toolCalls []*provider.ToolCall) bool {
	for _, tc := range toolCalls {
		for _, t := range tools {
			if t.Name() != tc.Name {
				continue
			}
			if sm, ok := t.(tool.StateModifier); ok && sm.IsStateModifying() {
				return true
			}
			break
		}
	}
	return false
}

// evictCompletedBackgroundTasks calls EvictCompleted on the delegate tool's background manager
// if one is configured, preventing unbounded memory growth from completed task entries.
// Called via defer at the top of streamWithToolLoop so that tasks remain accessible
// throughout the entire tool loop and eviction covers all exit paths.
//
// Side effects:
//   - Removes terminal tasks from the background task manager if a delegate tool is present.
//
// Expected: parameters for evictCompletedBackgroundTasks.
// Returns: result of evictCompletedBackgroundTasks.
func (e *Engine) evictCompletedBackgroundTasks() {
	dt, ok := e.GetDelegateTool()
	if !ok {
		return
	}
	bm := dt.BackgroundManager()
	if bm != nil {
		bm.EvictCompleted()
	}
}

// retryStreamForToolResult publishes a retry event and opens a new provider stream
// after a tool call completes, continuing the tool loop.
//
// Expected:
//   - sessionID identifies the current session.
//   - messages includes the updated conversation with the tool result appended.
//   - attempt is the 1-based retry counter for observability.
//
// Returns:
//   - A channel of provider stream chunks for the next loop iteration.
//   - An error if the new stream cannot be initialised.
//
// Side effects:
//   - Publishes provider.request.retry and provider.request events on the bus.

// retryStreamForToolResultWithTools is the internal implementation
// shared by retryStreamForToolResult and retryStreamForToolResultNoSchemas.
// The caller supplies the tool schemas directly; passing nil or an empty
// slice produces a text-only retry with no tools advertised.
//
// Expected: ctx is a non-cancelled context; sessionID identifies the
// active session; messages contains the full conversation history for the
// retry; attempt is the 1-based retry number; tools is nil for manifest
// schemas, a non-nil slice for caller-supplied schemas.
// Returns: a stream chunk channel and nil error on success, or nil channel
// and error if the provider request fails.
// Side effects: publishes a ProviderRequestRetry event before sending the
// request to the provider.
func (e *Engine) retryStreamForToolResultWithTools(
	ctx context.Context, sessionID string, messages []provider.Message, attempt int, tools []provider.Tool,
) (<-chan provider.StreamChunk, error) {
	turnWatchdogFromContext(ctx).markProgress()
	e.bus.Publish(events.EventProviderRequestRetry, events.NewProviderRequestRetryEvent(events.ProviderRequestRetryEventData{
		SessionID:    sessionID,
		AgentID:      e.activeAgentID(ctx),
		ProviderName: e.lastProviderCtx(ctx),
		ModelName:    e.lastModelCtx(ctx),
		Reason:       "tool_loop_retry",
		Attempt:      attempt,
	}))
	toolReq := provider.ChatRequest{
		Provider: e.lastProviderCtx(ctx),
		Model:    e.lastModelCtx(ctx),
		Messages: messages,
		// When tools is nil the caller (retryStreamForToolResult) wants the
		// full schema from the manifest. When non-nil (including empty slice),
		// the caller has explicitly chosen what to advertise — used by the
		// forced-summary path to strip all tools and force a text-only response.
		Tools: tools,
	}
	// Re-apply the per-stream provider/model override on every tool-loop
	// continuation. Stream() stamps the override (Stream's gate at the
	// req construction site) so the FIRST turn of a delegated member runs
	// on its manifest's preferred_models (e.g. anthropic) — but the
	// continuation request built here defaults to e.LastProvider/LastModel
	// (the child engine's GLOBAL default, e.g. zai/glm-4.5). Without this
	// gate a swarm member ran turn 1 on anthropic and then every
	// tool-result continuation turn silently reverted to the global
	// default. Members are dominated by tool-loop turns (bash/file scans),
	// so the manifest's declared tier was effectively never honoured at
	// runtime — the symptom that survived commits 04adb404 (manifests) and
	// 7a82fba7 (preference prepend), both of which only fixed turn 1. The
	// override keys are carried on ctx (streamCtx) from Stream's seam;
	// empty values fall through to the engine default exactly as the
	// Stream-site gate does.
	if provOverride := session.ProviderOverrideFromContext(ctx); provOverride != "" {
		toolReq.Provider = provOverride
	}
	if modelOverride := session.ModelOverrideFromContext(ctx); modelOverride != "" {
		toolReq.Model = modelOverride
	}
	chunks, streamErr := e.streamFromProvider(ctx, &toolReq)
	e.publishProviderRequestEventCtx(ctx, sessionID, toolReq)
	if streamErr != nil {
		e.publishProviderErrorEventCtx(ctx, sessionID, "stream_init", &toolReq, streamErr)
		return nil, streamErr
	}
	return chunks, nil
}

// retryStreamForToolResult retries the provider stream with the full tool
// schema from the active manifest. This is the standard retry path used by
// all tool-loop continuation call sites.
//
// Expected: parameters for retryStreamForToolResult.
// Returns: result of retryStreamForToolResult.
// Side effects: None.
func (e *Engine) retryStreamForToolResult(
	ctx context.Context, sessionID string, messages []provider.Message, attempt int,
) (<-chan provider.StreamChunk, error) {
	return e.retryStreamForToolResultWithTools(ctx, sessionID, messages, attempt, e.buildToolSchemasCtx(ctx))
}

// retryStreamForToolResultNoSchemas retries the provider stream with no tool
// schemas advertised. The model can only produce text, making this suitable
// for the forced-summary step that fires when the tool-loop budget is exhausted
// and all other grace paths have been tried.
//
// Expected: parameters for retryStreamForToolResultNoSchemas.
// Returns: result of retryStreamForToolResultNoSchemas.
// Side effects: None.
func (e *Engine) retryStreamForToolResultNoSchemas(
	ctx context.Context, sessionID string, messages []provider.Message, attempt int,
) (<-chan provider.StreamChunk, error) {
	return e.retryStreamForToolResultWithTools(ctx, sessionID, messages, attempt, nil)
}

// postTurnUsageEmitter is the optional pre-Done hook the goroutine in
// Stream installs so a fresh `context_usage` chunk is forwarded before
// every terminal Done. Mirrors the TUI's per-redraw status-bar refresh
// (internal/tui/intents/chat/intent.go syncStatusBar) — the chip ticks
// up to reflect the just-extended message history rather than waiting
// for the user's next send.
//
// Parameters:
//   - outChan is the engine's output channel; the callback writes the
//     fresh context_usage chunk to it.
//   - postTurnContent / postTurnThinking carry the just-completed
//     assistant turn's accumulated text / thinking. The callback
//     synthesises a trailing assistant message from these so the
//     post-turn input-token estimate ticks up to "what the next
//     turn's send would cost".
//
// The callback is non-nil only when the engine has a token counter
// wired AND a resolvable limit; nil suppresses post-turn emission so
// degraded environments (no counter, no limit) match the pre-send
// behaviour.
type postTurnUsageEmitter func(outChan chan<- provider.StreamChunk, postTurnContent, postTurnThinking string)

// streamChunkResult carries the assembled output from processStreamChunks.
type streamChunkResult struct {
	toolCalls       []*provider.ToolCall // all tool calls emitted in one assistant turn
	responseContent string
	thinkingContent string
	stopReason      string // upstream provider stop reason from the terminal Done chunk
	done            bool
	contextOverflow bool // true when any chunk carried ErrorTypeContextWindowExceeded
}

// turnOpenMarker is the thinking payload surfaced on the synthetic flush
// emitted when a provider opens an assistant turn with a bare tool_use.
// The invariant pinned at the engine Stream seam (documented across the
// vault: Chat TUI Message Rendering Order Fix, Session Rendering
// Consistency, ADR - Streaming Architecture) requires the consumer to
// observe a content or thinking artefact before the first tool_use of a
// turn. A single space is the minimum non-empty payload that trips the
// consumer's FlushPartialResponse path without polluting the transcript:
// the session accumulator's thinkingBuf swallows it at the next boundary,
// and chat renderers treat whitespace-only thinking as a no-op.
const turnOpenMarker = " "

// processStreamChunks reads chunks from the provider stream until a tool call or completion.
//
// Expected:
//   - ctx is a valid context for the operation.
//   - providerChunks is a channel of chunks from the provider.
//   - outChan is the output channel for forwarding chunks.
//
// Returns:
//   - A ToolCall if one was encountered, or nil.
//   - The accumulated response content as a string.
//   - A boolean indicating whether streaming is complete.
//
// Side effects:
//   - Forwards chunks to outChan.
//   - Sends error chunks if context is cancelled.
//   - Forwards a synthetic thinking chunk ahead of the first tool_use of a
//     turn when no content or thinking has yet been surfaced. This pins the
//     canonical thinking/text -> tool_use ordering at the Stream seam so
//     every consumer (TUI, CLI, SSE, WS) observes a flushable artefact
//     before the tool artefact.
func (e *Engine) processStreamChunks(
	ctx context.Context, sessionID string, providerChunks <-chan provider.StreamChunk, outChan chan<- provider.StreamChunk,
	postTurnUsage postTurnUsageEmitter,
) streamChunkResult {
	var responseContent strings.Builder
	var thinkingContent strings.Builder
	// sawTextOrThinking tracks whether any Content or Thinking chunk has
	// been forwarded to outChan for the current turn. The assistant-turn
	// artefact-ordering invariant requires at least one such artefact to
	// precede the first tool_use surfaced to the consumer.
	var sawTextOrThinking bool
	var toolCalls []*provider.ToolCall
	var overflowDetected bool

	emitPostTurn := func() {
		if postTurnUsage != nil {
			postTurnUsage(outChan, responseContent.String(), thinkingContent.String())
		}
	}

	// Idle-stream watchdog (May 2026 mid-thinking-halt fix).
	//
	// The three pre-existing Done-emission paths in this loop all assume
	// forward progress from the provider stream:
	//
	//   1. ctx.Done() — but the dispatcher's WithoutCancel
	//      (internal/dispatch/dispatcher.go:541) decouples streamCtx
	//      from the request lifecycle, so a client-side disconnect
	//      never trips this arm.
	//   2. providerChunks close — cannot happen when the underlying
	//      HTTP body read parks on a silent connection (Anthropic SDK
	//      stream.Next() has no read deadline; see
	//      internal/provider/anthropic/anthropic.go:streamMessages).
	//   3. chunk.Done == true — never arrives if the provider never
	//      emits a terminal message_stop frame.
	//
	// When all three are blocked, this loop hangs indefinitely. The
	// user-visible symptom captured in the May 2026 SSE probe (3,240
	// lines: 103 thinking chunks + heartbeats, no [DONE]) was
	// "halting mid thinking..." with no recovery short of a page
	// reload + reconcileFromBackend trip.
	//
	// The watchdog adds a fourth arm: if no chunk arrives within
	// e.streamIdleTimeout, emit a synthetic Done{empty_turn} and
	// return so consumers can stop hanging. The timer resets on
	// every chunk received (via Reset on the underlying
	// *time.Timer) so a long stream of small chunks does not trip
	// it spuriously — only a genuine gap longer than the threshold
	// counts. Zero streamIdleTimeout disables the watchdog (defence-
	// in-depth gate for callers that have not opted in).
	var idleTimer *time.Timer
	var idleC <-chan time.Time
	if e.streamIdleTimeout > 0 {
		idleTimer = time.NewTimer(e.streamIdleTimeout)
		idleC = idleTimer.C
		defer idleTimer.Stop()
	}
	chunkWatchdog := turnWatchdogFromContext(ctx)
	sawFirstChunk := false

	for {
		select {
		case <-ctx.Done():
			emitPostTurn()
			if e.requiresDeliveryToolCtx(ctx) && !e.deliveryToolCompleted(sessionID) {
				e.warnDeliveryToolBypassCtx(ctx, sessionID)
			}
			if e.onStreamCancel != nil {
				e.onStreamCancel(sessionID)
			}
			if w := turnWatchdogFromContext(ctx); w.hasFired() && w.claimTerminal() {
				emitTerminalStreamChunk(ctx, outChan, provider.StreamChunk{
					Done:       true,
					StopReason: session.StopReasonToolLoopExceeded,
					ModelID:    e.LastModel(),
					ProviderID: e.LastProvider(),
				})
				return streamChunkResult{responseContent: responseContent.String(), thinkingContent: thinkingContent.String(), done: true}
			}
			emitTerminalStreamChunk(ctx, outChan, e.terminalRecoveryChunk(chunkWatchdog, ctx, ctx.Err()))
			return streamChunkResult{responseContent: responseContent.String(), thinkingContent: thinkingContent.String(), done: true}
		case <-idleC:
			// Idle-stream watchdog fired. Emit a synthetic Done so
			// SSE / TUI / CLI consumers stop hanging. StopReasonEmptyTurn
			// re-uses the placeholder-assistant path already wired through
			// the session accumulator (internal/session/accumulator.go:766),
			// which renders a soft-error bubble rather than a "killed"
			// state. Post-turn usage emission MUST run before the Done
			// chunk so the context_usage chip ticks up; the SSE consumer
			// returns on first Done.
			emitPostTurn()
			emitTerminalStreamChunk(ctx, outChan, provider.StreamChunk{
				Done:       true,
				StopReason: session.StopReasonEmptyTurn,
				ModelID:    e.LastModel(),
				ProviderID: e.LastProvider(),
			})
			return streamChunkResult{
				responseContent: responseContent.String(),
				thinkingContent: thinkingContent.String(),
				done:            true,
			}
		case chunk, ok := <-providerChunks:
			// Reset the idle-stream watchdog on every chunk (including
			// channel-close events — the loop's exit on close runs
			// immediately below, so a Reset here is a no-op for the
			// close path but keeps the reset rule uniform with the
			// chunk-received path). The Stop()-then-Reset pattern
			// follows the time package's documented sequence for
			// safely resetting an already-fired timer. The drain of
			// idleC inside the same select arm is unnecessary because
			// the watchdog only ever fires from its own case, not from
			// this one.
			if idleTimer != nil {
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(e.streamIdleTimeout)
			}
			if sawFirstChunk {
				chunkWatchdog.markChunkProgress()
			} else if ok {
				sawFirstChunk = true
				chunkWatchdog.markProgress()
			}
			if !ok {
				// Channel close handling splits on pending tool calls:
				//
				//   - No pending tool calls (true empty-response Path C):
				//     emit a synthetic Done{StopReasonEmptyTurn} so the
				//     accumulator can synthesise a placeholder assistant
				//     and the chat UI's in-flight bubble doesn't hang
				//     until the watchdog trips. This is the legitimate
				//     intent of commit 8c939f7d.
				//
				//   - Pending tool calls: do NOT emit Done here. The
				//     tool loop in streamWithToolLoop will execute the
				//     pending calls and open a follow-up provider stream
				//     via retryStreamForToolResult. The follow-up
				//     stream's natural Done is the turn's terminal
				//     event. If the retry stream-init itself fails,
				//     streamWithToolLoop emits Done{Error} from there —
				//     that's the genuine interrupted path. Emitting a
				//     synthetic Done{StopReasonTurnInterrupted} here
				//     produced a double-Done: SSE/Vue consumers `break`
				//     on first Done, so the second Done from the
				//     follow-up stream was dropped and the user saw an
				//     "interrupted" state for a turn that completed
				//     successfully (Bug C1, May 2026 bughunt).
				if len(toolCalls) == 0 && !overflowDetected {
					emitPostTurn()
					stopReason := session.StopReasonEmptyTurn
					if chunkWatchdog != nil && chunkWatchdog.hasFired() && chunkWatchdog.claimTerminal() {
						stopReason = session.StopReasonToolLoopExceeded
					}
					emitTerminalStreamChunk(ctx, outChan, provider.StreamChunk{
						Done:       true,
						StopReason: stopReason,
						ModelID:    e.LastModel(),
						ProviderID: e.LastProvider(),
					})
				}
				return streamChunkResult{
					toolCalls:       toolCalls,
					responseContent: responseContent.String(),
					thinkingContent: thinkingContent.String(),
					done:            len(toolCalls) == 0,
					contextOverflow: overflowDetected,
				}
			}

			chunk.ModelID = e.LastModel()
			chunk.ProviderID = e.LastProvider()

			// UI Parity PR5 — Live token counter (May 2026).
			// Record the in-flight cumulative output_tokens off
			// chunk.Usage so the next streaming.heartbeat tick can
			// thread the figure onto the bus payload. UsageDelta is a
			// snapshot (cumulative, not incremental) so unconditional
			// overwrite is the correct semantics. Gate on >0 so a
			// usage-less chunk (the common case for content/tool/
			// thinking chunks that carry no Usage) does not zero out
			// a prior recorded figure mid-turn.
			if chunk.Usage != nil && chunk.Usage.OutputTokens > 0 {
				e.recordSessionOutputTokens(sessionID, chunk.Usage.OutputTokens)
			}

			if chunk.Usage != nil && chunk.Usage.InputTokens > 0 {
				e.recordSessionInputTokens(sessionID, chunk.Usage.InputTokens)
			}

			// Quota Plan PR4 — spend accumulator. Mirror the gate
			// above: any chunk carrying Usage feeds the Tracker via
			// recordQuotaSpend (no-op when the tracker is unwired).
			// The snapshot-not-increment dedupe lives inside the
			// Tracker (spend.go RecordSpend) so a 3-chunk stream
			// produces the cost of the FINAL cumulative, not the
			// sum-of-deltas. Plan §"Engine integration / spend
			// accumulation rules (A4 resolution)" lines 299-318.
			if chunk.Usage != nil {
				e.recordQuotaSpend(ctx, chunk.ProviderID, chunk.ModelID, chunk.Usage)
			}

			// Dispatch by chunk shape rather than EventType so the loop
			// matches the session accumulator (internal/session/accumulator.go:98)
			// and cannot silently drop tool calls when a provider forgets to
			// stamp EventType. Non-anthropic OpenAI-compatible providers hit this
			// path; anthropic and ollama continue to stamp EventType so this check
			// is strictly more permissive for them without behavioural change.
			if chunk.ToolCall != nil {
				e.publishToolReasoningEvent(ctx, sessionID, chunk.ToolCall.Name, responseContent.String())
				e.forwardToolCallChunk(ctx, sessionID, chunk, &thinkingContent, sawTextOrThinking, outChan)
				sawTextOrThinking = true // the tool_call chunk acts as the turn-open marker
				toolCalls = append(toolCalls, chunk.ToolCall)
				continue // keep reading — there may be more tool calls in this turn
			}

			// streaming.IsControlEvent gate: harness_attempt_start /
			// harness_retry / plan_artifact / etc. carry structured
			// metadata in Content destined for out-of-band consumers
			// (status line, SSE event channel) — never for the
			// next-turn LLM context this loop assembles. The session
			// accumulator already filters at
			// internal/session/accumulator.go:192-194; mirror here so
			// in-flight responseContent and tool-loop callers stay
			// clean. See session 2d8dc0ac chat-UI leak triage.
			if streaming.IsControlEvent(chunk.EventType) {
				continue
			}
			// Anthropic ping events signal provider liveness during long
			// thinking phases. Emit a streaming.heartbeat event on the bus
			// for the watchdog adaptive timeout, but do NOT forward the chunk
			// to outChan — it's an internal signal, not user-visible output.
			if chunk.EventType == "ping" {
				e.publishStreamingHeartbeat(sessionID, e.activeAgentID(ctx), "thinking")
				continue
			}
			// Typed observability events (provider_changed, model_active)
			// carry structured metadata in chunk.Content for the chat UI's
			// failover toast and chip-pivot affordances. Concatenating
			// their JSON into responseContent leaked
			// {"from":...,"to":...} and {"provider":...,"model":...} into
			// the persisted assistant message body and the next-turn LLM
			// context. Forward verbatim and skip the content/thinking
			// accumulation. The session accumulator already filters at
			// internal/session/accumulator.go:216-218; this mirrors the
			// guard so in-flight responseContent stays clean.
			if chunk.EventType != "" {
				emitStreamChunk(ctx, outChan, chunk)
				continue
			}
			thinkingContent.WriteString(chunk.Thinking)
			responseContent.WriteString(chunk.Content)
			if chunk.Content != "" || chunk.Thinking != "" {
				sawTextOrThinking = true
			}

			if chunk.Error != nil {
				var pErr *provider.Error
				if errors.As(chunk.Error, &pErr) &&
					pErr.ErrorType == provider.ErrorTypeContextWindowExceeded {
					overflowDetected = true
				}
			}
			if chunk.Done {
				// Do NOT forward the Done chunk when tool calls are pending.
				// Emitting Done while the tool loop is still running would
				// cause every consumer (TUI, CLI, SSE) to treat the stream
				// as complete before tool results are appended, ending the
				// turn prematurely. The tool loop emits its own terminal
				// events after all results are collected.
				if len(toolCalls) > 0 {
					return streamChunkResult{
						toolCalls:       toolCalls,
						responseContent: responseContent.String(),
						thinkingContent: thinkingContent.String(),
					}
				}
				if overflowDetected {
					return streamChunkResult{
						toolCalls:       toolCalls,
						responseContent: responseContent.String(),
						thinkingContent: thinkingContent.String(),
						stopReason:      chunk.StopReason,
						done:            true,
						contextOverflow: overflowDetected,
					}
				}
				// Phase 3 — emit a fresh context_usage chunk before
				// the terminal Done so the chip ticks up to reflect
				// the just-extended message history. SSE consumers
				// return on Done, so this MUST land first.
				emitPostTurn()
				// Forced-summary recovery: when the tool loop ended the
				// turn through a duration/total-tool-time trip and the
				// forced summary round completed naturally, this Done is
				// the turn's terminal event and must carry the
				// tool_loop_exceeded sentinel. The claim guard keeps the
				// stamp single-shot and inert on ordinary completions
				// (mirrors the stream-error exit at ~2027).
				if chunkWatchdog != nil && chunkWatchdog.hasFired() && chunkWatchdog.claimTerminal() {
					chunk.StopReason = session.StopReasonToolLoopExceeded
				}
				emitTerminalStreamChunk(ctx, outChan, chunk)
				return streamChunkResult{
					responseContent: responseContent.String(),
					thinkingContent: thinkingContent.String(),
					stopReason:      chunk.StopReason,
					done:            true,
					contextOverflow: overflowDetected,
				}
			}
			emitStreamChunk(ctx, outChan, chunk)
		}
	}
}

// publishToolReasoningEvent announces the reasoning text a provider
// accumulated before calling a tool, so observability plugins can pair
// the tool_call with the model's preceding rationale. No-op when the
// event bus is unset or no reasoning text has accumulated.
//
// Stamps AgentID via activeAgentID(ctx) so the reasoning event carries
// the manifest the in-flight Stream() resolved at entry, not whichever
// manifest a concurrent Stream() most recently swung onto e.manifest.
//
// Expected:
//   - ctx may carry a per-stream manifest binding from Stream().
//   - sessionID identifies the session the reasoning belongs to.
//   - toolName is the name of the tool the model is about to call.
//   - reasoning is the response content accumulated prior to the tool_use.
//
// Side effects:
//   - Publishes EventToolReasoning on e.bus when reasoning is non-empty.
//
// Returns: result of publishToolReasoningEvent.
func (e *Engine) publishToolReasoningEvent(ctx context.Context, sessionID, toolName, reasoning string) {
	if e.bus == nil || reasoning == "" {
		return
	}
	e.bus.Publish(events.EventToolReasoning, events.NewToolReasoningEvent(events.ToolReasoningEventData{
		SessionID:        sessionID,
		AgentID:          e.activeAgentID(ctx),
		ToolName:         toolName,
		ReasoningContent: reasoning,
	}))
}

// forwardToolCallChunk emits the ordering-gate flush (when required),
// stamps the FlowState-internal id, and forwards the provider's tool_call
// chunk to the consumer. Extracted from processStreamChunks to keep the
// main loop's cognitive complexity within the project's gocognit budget.
//
// Expected:
//   - ctx is the turn context guarding the output sends.
//   - chunk.ToolCall is non-nil (caller must have already dispatched by
//     chunk shape).
//   - sawTextOrThinking reports whether any Content or Thinking chunk has
//     been forwarded to outChan for the current turn.
//
// Side effects:
//   - Forwards a synthetic thinking chunk carrying turnOpenMarker ahead of
//     the tool_use when sawTextOrThinking is false, and appends the same
//     marker to thinkingContent so the completeResponse path sees the
//     flushed payload.
//   - Forwards chunk (with InternalToolCallID stamped) to outChan unless
//     the turn context is cancelled first.
//
// Returns: result of forwardToolCallChunk.
func (e *Engine) forwardToolCallChunk(
	ctx context.Context,
	sessionID string, chunk provider.StreamChunk, thinkingContent *strings.Builder,
	sawTextOrThinking bool, outChan chan<- provider.StreamChunk,
) {
	// Flush a synthetic turn-open marker when no content or thinking has
	// yet been surfaced to the consumer for this turn. Providers that open
	// a turn with a bare function call (openaicompat with tool-first
	// agents, or any anthropic-shape stream where the model omits
	// preamble) would otherwise violate the canonical thinking/text ->
	// tool_use ordering documented in the vault. Emitting the marker here
	// -- before the tool_use reaches outChan -- gives every downstream
	// consumer a flushable artefact to anchor their partial-response
	// commit against.
	if !sawTextOrThinking {
		emitStreamChunk(ctx, outChan, provider.StreamChunk{
			Thinking:   turnOpenMarker,
			ModelID:    e.LastModel(),
			ProviderID: e.LastProvider(),
		})
		thinkingContent.WriteString(turnOpenMarker)
	}
	// P14: stamp the FlowState-internal id so downstream consumers can
	// pair this call with its eventual result even if a failover rewrites
	// the provider-scoped id.
	chunk.InternalToolCallID = e.toolCallCorrelator.InternalID(
		sessionID, chunk.ToolCallID, chunk.ToolCall.Name, chunk.ToolCall.Arguments,
	)
	emitStreamChunk(ctx, outChan, chunk)
}

// InheritsParentToolDeadline reports whether the tool opts out of every
// engine-injected per-tool deadline by implementing tool.TimeoutOverrider
// with a zero Timeout — the delegation shape (DelegateTool is the only
// implementation). It mirrors the Timeout() == 0 branch of deriveToolCtx
// and MUST stay in sync with it.
//
// The tool loop's total-tool-time backstop excludes execution time spent
// in such tools: a Timeout() == 0 child runs its own tool loop with its
// own duration, iteration, and repeat caps, so counting the child against
// the parent's cap would double-cap legitimate long child runs.
//
// Expected:
//   - t is the resolved Tool about to be executed; may be nil.
//
// Returns:
//   - true only when t implements tool.TimeoutOverrider and declares no
//     budget of its own.
//
// Side effects:
//   - None; pure classification.
func InheritsParentToolDeadline(t tool.Tool) bool {
	if override, ok := t.(tool.TimeoutOverrider); ok {
		return override.Timeout() <= 0
	}
	return false
}

// deriveToolCtx returns the context a single tool invocation runs under,
// along with the cancel func the caller MUST invoke after Execute returns.
//
// The engine's default behaviour wraps every tool call in a short
// per-tool deadline (e.toolTimeout, typically 2 minutes) — that budget
// fits bash/read/web-style tools whose latency is shell-bounded. Tools
// whose execution is structurally longer — notably DelegateTool, which
// runs a full multi-turn sub-agent conversation — implement
// tool.TimeoutOverrider to opt out of the default budget:
//
//   - Timeout() > 0: the engine applies that duration as the per-tool
//     deadline instead of the default. Parent ctx deadlines still
//     apply; whichever expires first wins.
//   - Timeout() == 0: the engine installs no deadline of its own. The
//     tool inherits the parent context unchanged — parent cancellation
//     still cascades, but no engine-injected wall clock caps execution.
//
// Expected:
//   - parent is the caller's context; never nil.
//   - t is the resolved Tool about to be executed.
//
// Returns:
//   - A context to hand to Tool.Execute.
//   - The cancel func to invoke after Execute returns (safe to call in every path).
//
// Side effects:
//   - None; pure context derivation.
func (e *Engine) deriveToolCtx(parent context.Context, t tool.Tool) (context.Context, context.CancelFunc) {
	if override, ok := t.(tool.TimeoutOverrider); ok {
		if budget := override.Timeout(); budget > 0 {
			return context.WithTimeout(parent, budget)
		}
		// Inherit parent context unchanged, but still return a cancel
		// func the caller can invoke in every path for symmetry.
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, e.toolTimeout)
}

// todoStrictGate tracks the per-session count of non-todo tool calls for
// informational display in the todo context message. It NEVER blocks tool
// dispatch — the hard gate was removed after evidence (June 2026 sessions)
// showed it actively harmed investigation agents by interrupting legitimate
// work and forcing wasted todo-compliance calls. The soft mechanisms
// (continuation loop, stale-continuation detection, prompt visibility)
// are the enforcement layer; this counter is purely for the progress
// nudge in renderTodoSystemMessage.
//
// Semantics:
//   - When toolName is a todo tool: reset the counter to 0.
//   - Otherwise: increment the counter.
//   - Invariably returns (zero Result, false) — never blocks.
//
// Expected: parameters for todoStrictGate.
// Returns: result of todoStrictGate.
// Side effects: None.
func (e *Engine) todoStrictGate(sessionID, toolName string) (tool.Result, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if isTodoTool(toolName) {
		delete(e.todoNonTodowriteToolCalls, sessionID)
		return tool.Result{}, false
	}
	e.todoNonTodowriteToolCalls[sessionID]++
	return tool.Result{}, false
}

// hasIncompleteTodos reports whether the session's todo list contains any
// item whose status is "pending" or "in_progress", and returns those items.
// Returns (false, nil) when no todoStore is configured or when every item
// is completed or cancelled.
//
// Stale-continuation skip detection: when a continuation was injected
// but the model made zero "work" tool calls (non-todowrite, non-todo_update)
// and all items appear completed, this method reports the items as
// incomplete anyway. This prevents the model from marking everything
// done via todo_update calls without doing any actual work, which would
// otherwise short-circuit the continuation loop.
//
// Expected: parameters for hasIncompleteTodos.
// Returns: result of hasIncompleteTodos.
// Side effects: None.
func (e *Engine) hasIncompleteTodos(sessionID string) (bool, []todo.Item) {
	if e.todoStore == nil {
		return false, nil
	}
	items := e.todoStore.Get(sessionID)
	var incomplete []todo.Item
	for _, it := range items {
		if it.Status == "pending" || it.Status == "in_progress" {
			incomplete = append(incomplete, it)
		}
	}

	// Stale-continuation check: if a continuation was injected, the
	// model made no work tool calls, and all items are now marked
	// completed/cancelled, the model tried to sidestep the continuation.
	// Force-report items as incomplete so the continuation fires again.
	if len(incomplete) == 0 {
		if e.isContinuationStale(sessionID) {
			// Return the now-completed items so the continuation
			// message enumerates what the model skipped.
			incomplete = append(incomplete, items...)
			return true, incomplete
		}
	}

	return len(incomplete) > 0, incomplete
}

// hasActiveBackgroundTasks reports whether any background (delegated) tasks
// are still running for the given session. A background task is a subagent
// spawned by a delegate tool call that has not yet completed.
//
// Expected: parameters for hasActiveBackgroundTasks.
// Returns: result of hasActiveBackgroundTasks.
// Side effects: None.
func (e *Engine) hasActiveBackgroundTasks(sessionID string) bool {
	dt, ok := e.GetDelegateTool()
	if !ok {
		return false
	}
	return dt.BackgroundManager().ActiveCountForSession(sessionID) > 0
}

// activeBackgroundTaskCount returns the number of background tasks still
// running for the given session. Callers use this to decide between a
// background-task continuation and a hard stop.
//
// Expected: parameters for activeBackgroundTaskCount.
// Returns: result of activeBackgroundTaskCount.
// Side effects: None.
func (e *Engine) activeBackgroundTaskCount(sessionID string) int {
	dt, ok := e.GetDelegateTool()
	if !ok {
		return 0
	}
	return dt.BackgroundManager().ActiveCountForSession(sessionID)
}

// isContinuationStale reports whether the session has a stale continuation:
// a continuation was injected (todoContinuationFired) but the model made
// zero non-todowrite/non-todo_update tool calls since. When true, the
// model likely called todo_update to mark items completed without doing
// any actual work, defeating the intent of the continuation loop.
//
// Returns:
//   - true when the continuation is stale (injected but no work done).

// allTodosTerminal returns true when every todo item has a terminal status
// (completed or cancelled). Used to distinguish stale-continuation retries
// from real pending-work retries when hasIncompleteTodos returns hasMore=true
// but all items are already resolved.
//
// Expected: parameters for allTodosTerminal.
// Returns: result of allTodosTerminal.
// Side effects: None.
func allTodosTerminal(items []todo.Item) bool {
	if len(items) == 0 {
		return false
	}
	for _, it := range items {
		if it.Status != "completed" && it.Status != "cancelled" {
			return false
		}
	}
	return true
}

// isContinuationStale ...
//
// Expected: parameters for isContinuationStale.
//
// Returns: result of isContinuationStale.
//
// Side effects: None.
func (e *Engine) isContinuationStale(sessionID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.todoContinuationFired[sessionID] && e.workToolCallsSinceContinuation[sessionID] == 0
}

// resetContinuationState resets the stale-continuation detection state
// for a session. Called when a new continuation is injected (prepares
// for the next detection cycle).
//
// Expected: parameters for resetContinuationState.
// Returns: result of resetContinuationState.
// Side effects: None.
func (e *Engine) resetContinuationState(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.todoContinuationFired[sessionID] = true
	e.workToolCallsSinceContinuation[sessionID] = 0
}

// buildTodoContinuationMessage constructs the user-role continuation prompt
// injected into the message history when the engine detects incomplete todos
// after a model turn ends without tool calls. When any item has in_progress
// status the message names the active task and demands the agent complete or
// cancel it before proceeding; when all items are pending the existing generic
// message is returned verbatim.
//
// The continuation message explicitly demands tool calls and warns against
// narration to prevent the agent from describing what it will do instead of
// actually doing it. This is a known failure mode where models produce prose
// like "I will now do X" without calling tools, causing stale-continuation
// loops.
//
// Expected: parameters for buildTodoContinuationMessage.
// Returns: result of buildTodoContinuationMessage.
// Side effects: None.
func buildTodoContinuationMessage(incomplete []todo.Item) provider.Message {
	hasActive := false
	for _, it := range incomplete {
		if it.Status == "in_progress" {
			hasActive = true
			break
		}
	}

	if hasActive {
		var sb strings.Builder
		sb.WriteString("You have an active task that must be completed before you can proceed:\n\n")

		for _, it := range incomplete {
			if it.Status == "in_progress" {
				sb.WriteString(fmt.Sprintf("  ▶ \"%s\" (%s priority)\n", it.Content, it.Priority))
			}
		}

		hasPending := false
		for _, it := range incomplete {
			if it.Status == "pending" {
				hasPending = true
				break
			}
		}

		if hasPending {
			sb.WriteString("\nAdditional queued tasks:\n")
			for _, it := range incomplete {
				if it.Status == "pending" {
					sb.WriteString(fmt.Sprintf("  ○ \"%s\" (%s priority)\n", it.Content, it.Priority))
				}
			}
		}

		sb.WriteString("\nYou must complete or cancel the active task now by calling the appropriate tools to do the actual work. ")
		sb.WriteString("Do NOT respond with prose describing what you will do — that is NOT completing the task. ")
		sb.WriteString("Call tools to make progress. You are not allowed to skip it or start unrelated work.\n")
		// CONTINUATION marker: the e2e scripted provider (continuationMarkers in
		// features/support/engine_steps.go) uses this to detect injected
		// todo-continuation prompts unambiguously.
		sb.WriteString("CONTINUATION: continue working on the pending todo items above.\n")
		return provider.Message{Role: "user", Content: sb.String()}
	}

	var sb strings.Builder
	sb.WriteString("You have incomplete tasks that still need to be completed:\n\n")
	for _, it := range incomplete {
		sb.WriteString(fmt.Sprintf("- [%s] %s (%s priority)\n", it.Status, it.Content, it.Priority))
	}
	sb.WriteString("\nResume working on these tasks now by calling tools to do the actual work. ")
	sb.WriteString("Do NOT respond with prose describing what you will do — that is NOT making progress. ")
	sb.WriteString("Call tools to complete these tasks.\n")
	sb.WriteString("CONTINUATION: continue working on the incomplete todo items above.\n")
	return provider.Message{Role: "user", Content: sb.String()}
}

// buildBackgroundTaskContinuationMessage renders a user-role message telling
// the model that background tasks are still running and it should wait for
// them to complete.
//
// Expected: parameters for buildBackgroundTaskContinuationMessage.
// Returns: result of buildBackgroundTaskContinuationMessage.
// Side effects: None.
func buildBackgroundTaskContinuationMessage(activeTasks int) provider.Message {
	var sb strings.Builder
	fmt.Fprintf(&sb, "You have %d background task(s) still running:\n", activeTasks)
	sb.WriteString("\nWait for these tasks to complete before proceeding. The system will notify you when they finish.")
	return provider.Message{Role: "user", Content: sb.String()}
}

// buildTodoContextMessage renders the current session's todo list as a
// system-role message for injection into the context window. Returns nil
// when the store is unconfigured or the session has no todos, so callers
// can skip the injection without a branch.
//
// Expected:
//   - sessionID identifies the active session.
//
// Returns:
//   - A pointer to a system-role provider.Message containing the rendered
//     list, or nil when there is nothing to inject.
//
// Side effects:
//   - Reads from e.todoStore (read-locked by the store implementation).
func (e *Engine) buildTodoContextMessage(sessionID string) *provider.Message {
	if e.todoStore == nil {
		return nil
	}
	items := e.todoStore.Get(sessionID)
	if len(items) == 0 {
		return nil
	}

	e.mu.RLock()
	toolCallCount := e.todoNonTodowriteToolCalls[sessionID]
	complexity := e.sessionComplexity[sessionID]
	e.mu.RUnlock()

	msg := renderTodoSystemMessage(items, toolCallCount, complexity)
	return &msg
}

// renderTodoSystemMessage formats the todo list as a system-role message
// with a clear header and per-item status indicators.
//
// Expected: parameters for renderTodoSystemMessage.
// Returns: result of renderTodoSystemMessage.
// Side effects: None.
func renderTodoSystemMessage(items []todo.Item, toolCallCount int, complexity TaskComplexity) provider.Message {
	var sb strings.Builder
	sb.WriteString("# Current Task List\n\n")
	sb.WriteString("This is your live todo list from the store. When updating statuses, use these exact items as your source of truth — do not reconstruct from memory.\n\n")
	for i, it := range items {
		marker := "[ ]"
		switch it.Status {
		case "completed":
			marker = "[x]"
		case "in_progress":
			marker = "[~]"
		case "cancelled":
			marker = "[-]"
		}
		sb.WriteString(fmt.Sprintf("%d. %s %s (%s priority)\n", i, marker, it.Content, it.Priority))
	}

	if toolCallCount > 0 {
		sb.WriteString(fmt.Sprintf(
			"\n📋 %d tool calls since your last todo list update. Update your progress via todo_update when you complete a task.\n",
			toolCallCount,
		))
	}

	return provider.Message{Role: "system", Content: sb.String()}
}

// appendTodoContext injects the current todo state as a system message
// immediately after the first system prompt in messages, when a todo
// store is configured and the session has todos. Returns messages
// unchanged when there is nothing to inject.
//
// Expected: parameters for appendTodoContext.
// Returns: result of appendTodoContext.
// Side effects: None.
func (e *Engine) appendTodoContext(messages []provider.Message, sessionID string) []provider.Message {
	todoMsg := e.buildTodoContextMessage(sessionID)
	if todoMsg == nil {
		return messages
	}
	if len(messages) == 0 {
		return []provider.Message{*todoMsg}
	}
	result := make([]provider.Message, 0, len(messages)+1)
	result = append(result, messages[0])
	result = append(result, *todoMsg)
	result = append(result, messages[1:]...)
	return result
}

// executeToolCall finds and executes the specified tool with the given arguments.
//
// Expected:
//   - ctx is a valid context for the operation.
//   - toolCall contains the tool name and arguments.
//
// Returns:
//   - A tool.Result with output or error.
//   - An error if the tool is not found.
//
// Side effects:
//   - Executes the tool, which may have its own side effects.
//   - When TodoStrictMode is enabled (D9), maintains a per-session
//     counter of non-todowrite tool calls and rejects calls that
//     cross the >3 threshold with a structured tool_result error.

// skillsLoadRequired returns true when the agent configuration includes
// permanently-active skills that must be loaded via skill_load before any
// other tool call can proceed.
//
// Returns: true if knownSkillsFunc is set and returns a non-empty slice.
// Side effects: None.
//
// Expected: parameters for skillsLoadRequired.
func (e *Engine) skillsLoadRequired() bool {
	if e.knownSkillsFunc == nil {
		return false
	}
	return len(e.knownSkillsFunc()) > 0
}

// skillLoadCompleted returns true if skill_load has been called at
// least once for the given session, indicating the skills-first gate
// should be skipped for subsequent tool calls.
//
// Expected: parameters for skillLoadCompleted.
// Returns: result of skillLoadCompleted.
// Side effects: None.
func (e *Engine) skillLoadCompleted(sessionID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.skillLoadCalled[sessionID]
}

// markSkillLoadCalled records that skill_load has been invoked for the
// session, allowing subsequent non-skill_load tool calls to proceed
// through the skills-first gate.
//
// Expected: parameters for markSkillLoadCalled.
// Returns: result of markSkillLoadCalled.
// Side effects: None.
func (e *Engine) markSkillLoadCalled(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.skillLoadCalled[sessionID] = true
	delete(e.skillGuardRejections, sessionID)
}

// requiresDeliveryTool reports whether the active manifest declares any
// delivery tools that must be called before session completion.
//
// Expected: parameters for requiresDeliveryTool.
// Returns: result of requiresDeliveryTool.
// Side effects: None.
func (e *Engine) requiresDeliveryTool() bool {
	return len(e.manifest.Capabilities.DeliveryTools) > 0
}

// requiresDeliveryToolCtx is the ctx-aware variant. It checks the
// bound manifest first, falling back to e.manifest when no binding
// is present.
//
// Expected: parameters for requiresDeliveryToolCtx.
// Returns: result of requiresDeliveryToolCtx.
// Side effects: None.
func (e *Engine) requiresDeliveryToolCtx(ctx context.Context) bool {
	if m, ok := manifestFromContext(ctx); ok {
		return len(m.Capabilities.DeliveryTools) > 0
	}
	return e.requiresDeliveryTool()
}

// deliveryToolsForCtx returns the bound manifest's delivery tools when
// present, falling back to the engine manifest otherwise.
//
// Expected: parameters for deliveryToolsForCtx.
// Returns: result of deliveryToolsForCtx.
// Side effects: None.
func (e *Engine) deliveryToolsForCtx(ctx context.Context) []string {
	if m, ok := manifestFromContext(ctx); ok {
		return append([]string(nil), m.Capabilities.DeliveryTools...)
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]string(nil), e.manifest.Capabilities.DeliveryTools...)
}

// deliveryToolCompleted reports whether a delivery tool was successfully
// called during the session identified by sessionID.
//
// Expected: parameters for deliveryToolCompleted.
// Returns: result of deliveryToolCompleted.
// Side effects: None.
func (e *Engine) deliveryToolCompleted(sessionID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deliveryToolCalled[sessionID]
}

// markDeliveryToolCalledCtx records that a delivery tool was called for the
// given session. The toolName must match one of the manifest's
// DeliveryTools entries; calls for non-delivery tools are ignored.
// Resolves the delivery tool list from the session manifest, prioritising
// sessionManifests (for child sessions in delegation) over ctx binding.
//
// The args parameter is inspected for tool-specific delivery semantics:
//   - coordination_store: only counts as delivery when operation=set (write).
//     Calls for get/list/delete are not delivery actions.
//
// Expected: parameters for markDeliveryToolCalledCtx.
// Returns: result of markDeliveryToolCalledCtx.
// Side effects: None.
func (e *Engine) markDeliveryToolCalledCtx(ctx context.Context, sessionID string, toolName string, args map[string]any) {
	var deliveryTools []string
	// Prioritise session-scoped manifest (child sessions in delegation)
	// over ctx binding (which carries parent manifest in delegation).
	e.mu.RLock()
	if m, ok := e.sessionManifests[sessionID]; ok {
		deliveryTools = m.Capabilities.DeliveryTools
	} else if m, ok := manifestFromContext(ctx); ok {
		deliveryTools = m.Capabilities.DeliveryTools
	} else {
		deliveryTools = e.manifest.Capabilities.DeliveryTools
	}
	e.mu.RUnlock()

	if len(deliveryTools) == 0 {
		return
	}
	for _, dt := range deliveryTools {
		if dt == toolName && e.isDeliveryCall(toolName, args) {
			e.mu.Lock()
			if e.deliveryToolCalled == nil {
				e.deliveryToolCalled = make(map[string]bool)
			}
			e.deliveryToolCalled[sessionID] = true
			e.mu.Unlock()
			return
		}
	}
}

// isDeliveryCall checks whether a tool call qualifies as a delivery action
// based on its arguments. For tools where any call is a delivery (e.g.,
// write, edit), this returns true without exception. For tools with read/write
// semantics (e.g., coordination_store), only write operations count.
//
// Expected: parameters for isDeliveryCall.
// Returns: result of isDeliveryCall.
// Side effects: None.
func (e *Engine) isDeliveryCall(toolName string, args map[string]any) bool {
	switch toolName {
	case "coordination_store":
		op, _ := args["operation"].(string)
		return op == "set"
	default:
		return true
	}
}

// warnDeliveryToolBypassCtx logs a prominent warning when a session is about to
// complete without having called any of its declared delivery tools.
// Resolves the manifest from the ctx binding to prevent cross-session bleed.
//
// Expected: parameters for warnDeliveryToolBypassCtx.
// Returns: result of warnDeliveryToolBypassCtx.
// Side effects: None.
func (e *Engine) warnDeliveryToolBypassCtx(ctx context.Context, sessionID string) {
	var agentID string
	var deliveryTools []string

	// Prioritise session-scoped manifest (child sessions in delegation)
	// over ctx binding (which carries parent manifest in delegation).
	e.mu.RLock()
	if m, ok := e.sessionManifests[sessionID]; ok {
		deliveryTools = m.Capabilities.DeliveryTools
		agentID = m.ID
	} else if m, ok := manifestFromContext(ctx); ok {
		deliveryTools = m.Capabilities.DeliveryTools
		agentID = m.ID
	} else {
		deliveryTools = e.manifest.Capabilities.DeliveryTools
		agentID = e.manifest.ID
	}
	e.mu.RUnlock()

	if len(deliveryTools) == 0 {
		return
	}
	if e.deliveryToolCompleted(sessionID) {
		return
	}
	slog.Warn("delivery tool gate skipped: session completing without calling delivery tools",
		"session", sessionID,
		"agent", agentID,
		"delivery_tools", deliveryTools,
	)
}
