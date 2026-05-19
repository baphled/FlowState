package api

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// brokerSubscriberGracePeriod bounds how long a Publish call will wait on a
// single subscriber's full channel before recording a drop. The original
// implementation had a `default:` clause (zero grace) — a slow subscriber
// silently lost chunks. A small grace period absorbs micro-bursts in normal
// streaming load without letting a permanently-stuck subscriber starve its
// siblings (the brief's Drop #4 constraint).
const brokerSubscriberGracePeriod = 50 * time.Millisecond

// SessionBroker distributes live session events to registered subscribers.
//
// # Concurrency contract (formal, post-audit May 2026)
//
// The broker holds the following invariants under any concurrent
// interleaving of Subscribe, Unsubscribe, Publish, IsPublishing, and
// DroppedCount calls:
//
//   - I1 Single-closer per channel. A subscriber channel is closed
//     EXACTLY ONCE, and only by the LAST in-flight Publish run for the
//     session as it exits. Unsubscribe never closes; concurrent Publish
//     runs cooperatively coordinate via the active refcount so only the
//     last to exit owns the close.
//
//   - I2 Concurrent Publish for the same sessionID is safe. Chunks from
//     N concurrent runs may interleave on subscriber channels in any
//     order, but no panic, no double-close, and no leaked subscribers
//     are possible. (Production wires one Publish per turn; this
//     invariant defends against future wiring regressions.)
//
//   - I3 Subscribe-during-terminal-close is safe. The terminal close
//     loop runs while holding b.mu; a Subscribe racing it either sees
//     IsPublishing == true (and joins the in-flight run) or sees
//     IsPublishing == false (and starts a fresh entry that no current
//     Publish will close). No subscriber channel is ever closed twice
//     and no live subscriber is ever orphaned in the map without an
//     unsubscribe path.
//
//   - I4 Subscribe / Unsubscribe / Publish hold b.mu only across map
//     mutations and channel closes. Channel SENDS happen outside the
//     lock — sends are protected by the close-by-sender invariant
//     (I1), not by b.mu.
//
//   - I5 No RWMutex upgrade. The broker uses sync.Mutex (not RWMutex)
//     and never calls user code under the lock. The RWMutex deadlock
//     class documented in the engine bug-fix note ("RLock then call
//     that acquires WLock") is structurally inapplicable.
//
//   - I6 Live-events-only contract. The broker fans out events
//     emitted strictly after a subscriber registers; it does not
//     replay history. Historical content lives on
//     GET /api/v1/sessions/{id}/messages. The broker has no replay
//     branch and must not grow one (see SSE Broker Replays Sealed Turn
//     bug-fix note).
//
//   - I7 Bounded backpressure. A subscriber that stays full past
//     brokerSubscriberGracePeriod loses the chunk on a counted +
//     logged drop. Sibling subscribers are never starved by a stuck
//     consumer (see Streaming Signal-Drop Fix Drop #4).
//
//   - I8 Subscribers-map cleanliness. After a Publish run exits AND
//     all its subscribers unsubscribe, the broker's subscribers map
//     contains no entry for that session. Empty-slice entries are
//     dropped on Unsubscribe so long-running servers do not accumulate
//     dead session keys.
//
// # Field synchronisation
//
//   - mu: sync.Mutex protecting all map mutations (subscribers, active)
//     and channel closes.
//   - subscribers: map session-id -> []chan; written under mu, read
//     under mu (snapshot copy returned for outside-lock fan-out).
//   - active: refcount of in-flight Publish runs per session; written
//     under mu, read under mu.
//   - dropped: process-lifetime monotonic atomic counter for backpressure
//     drops; safe for concurrent reads/writes without mu.
//
// # Why a refcount, not a bool, for `active`
//
// The pre-audit implementation used `active map[string]bool`. Two
// concurrent Publish runs for the same sessionID would both set the
// flag to true and both run the terminal close loop, calling close()
// on every channel in the snapshot — a "close of closed channel" panic
// on the second run. A bool cannot express "still in flight" once a
// second run starts; a refcount can. With the refcount, only the run
// that decrements active to zero owns the close.
type SessionBroker struct {
	mu          sync.Mutex
	subscribers map[string][]chan provider.StreamChunk
	active      map[string]int
	dropped     atomic.Uint64
}

// NewSessionBroker creates a new SessionBroker with empty maps.
//
// Returns:
//   - A ready-to-use SessionBroker.
//
// Side effects:
//   - None.
func NewSessionBroker() *SessionBroker {
	return &SessionBroker{
		subscribers: make(map[string][]chan provider.StreamChunk),
		active:      make(map[string]int),
	}
}

// IsPublishing reports whether at least one Publish call is currently in
// progress for the given session.
//
// Callers use this to distinguish two cases after subscribing:
//
//   - true  → at least one Publish run is mid-flight; the select loop
//     will receive its chunks.
//   - false → either Publish hasn't started yet (pending message,
//     caller should wait) or every prior run has finished before
//     Subscribe was called (caller should check session state and
//     fast-path [DONE] if complete).
//
// Returns true while the active refcount is > 0; the count is
// incremented at Publish entry and decremented at Publish exit, both
// under b.mu.
func (b *SessionBroker) IsPublishing(sessionID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.active[sessionID] > 0
}

// SubscriberCount returns the number of registered subscribers for the
// given session. Intended for invariant assertions in concurrency tests
// (see broker_test.go's "concurrency audit" Describe block) — it is
// the only safe way to observe the subscribers map without exporting
// it.
//
// Production code MUST NOT branch on this value: doing so reintroduces
// a TOCTOU window between the count read and any subsequent action,
// and the broker's contract intentionally hides subscriber identity
// from callers. The accessor is exported for test-introspection only.
func (b *SessionBroker) SubscriberCount(sessionID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subscribers[sessionID])
}

// SubscribeIfPublishing atomically checks whether a Publish is currently
// in-flight for the session and, if so, registers a fresh subscriber.
// Returns (ch, unsub, true) when a publisher is active and a subscriber
// has been added; returns (nil, no-op, false) when no publisher is
// active and no subscription was created.
//
// # Why this exists — closing the IsPublishing TOCTOU
//
// Pre-fix, callers wrote:
//
//	ch, unsub := broker.Subscribe(id)
//	defer unsub()
//	if !broker.IsPublishing(id) {
//	    return // emit [DONE]
//	}
//	// consume from ch
//
// Between the IsPublishing read and the conditional return, a Publish
// run could start. The subscriber was already registered (Subscribe
// completed before IsPublishing), so the new run fans chunks into ch —
// but the caller has already decided to return [DONE], drops out, and
// the chunks pile up in the buffered channel until backpressure drops
// them. The caller's semantics ("I want to know if a publisher will
// drive me") are not expressible as separate Subscribe + IsPublishing
// calls; the two reads observe different states.
//
// SubscribeIfPublishing collapses both decisions into a single critical
// section under b.mu: the active refcount is read AND, if positive, the
// subscriber slice is mutated atomically. There is no observable state
// between "is publishing?" and "am I a subscriber?" — they are answered
// together or not at all.
//
// # Contract
//
// On (ok == true): a fresh subscriber channel and unsubscribe function
// are returned. The unsub function has the same semantics as the one
// from Subscribe (idempotent, does not close the channel; close is the
// publisher's responsibility per invariant I1). The caller MUST
// eventually call unsub.
//
// On (ok == false): nil channel and a no-op unsubscribe function are
// returned. No subscriber state has changed. The caller may safely call
// the no-op unsub (it is provided so callers can `defer unsub()`
// uniformly without nil-checks).
//
// # Invariant compliance
//
//   - I1 (single-closer): unchanged. The new method only inserts into
//     the subscribers slice; it never closes a channel.
//   - I3 (Subscribe-during-terminal-close): preserved. The whole
//     check+insert runs under b.mu; if a Publish is in its terminal
//     close (also under b.mu), this call serialises behind it and sees
//     active == 0, returning (nil, no-op, false) — the correct outcome
//     since the publisher cohort that would have closed the new channel
//     has already exited.
//   - I4 (lock scope): the lock is held only for the map read and the
//     conditional append; there are no sends and no user callbacks.
//   - I8 (cleanliness): unchanged; Unsubscribe still drops empty-slice
//     entries.
//
// Expected:
//   - sessionID is a non-empty string identifying an existing session.
//
// Returns:
//   - A buffered (capacity 64) receive channel and an unsubscribe
//     function when a Publish is active for the session.
//   - nil channel and a no-op unsubscribe function when no Publish is
//     active for the session.
//   - A boolean indicating which case occurred.
//
// Side effects:
//   - On (ok == true): adds a subscriber channel to the session's
//     subscriber list.
//   - On (ok == false): no state change.
func (b *SessionBroker) SubscribeIfPublishing(sessionID string) (<-chan provider.StreamChunk, func(), bool) {
	b.mu.Lock()
	if b.active[sessionID] == 0 {
		b.mu.Unlock()
		return nil, func() {}, false
	}
	ch := make(chan provider.StreamChunk, 64)
	b.subscribers[sessionID] = append(b.subscribers[sessionID], ch)
	b.mu.Unlock()

	var unsubOnce sync.Once
	unsubscribe := func() {
		unsubOnce.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			subs := b.subscribers[sessionID]
			for i, sub := range subs {
				if sub == ch {
					b.subscribers[sessionID] = append(subs[:i], subs[i+1:]...)
					if len(b.subscribers[sessionID]) == 0 {
						delete(b.subscribers, sessionID)
					}
					return
				}
			}
		})
	}
	return ch, unsubscribe, true
}

// DroppedCount returns the cumulative number of chunks the broker has
// discarded because at least one subscriber's channel stayed full past
// the per-subscriber grace period. The counter is process-lifetime
// monotonic — it is never reset.
//
// The intended consumers are tests pinning observability behaviour and a
// Prometheus collector exporting the value as a counter metric. Drop #4
// of the streaming signal-drop fix introduced this accessor; pre-fix the
// broker silently dropped chunks with no observable signal at all.
func (b *SessionBroker) DroppedCount() uint64 {
	return b.dropped.Load()
}

// Subscribe registers a new subscriber for a session and returns a
// receive channel and an unsubscribe function.
//
// # Channel-close ownership
//
// The publisher is the sole closer of the returned channel (invariant
// I1). Unsubscribe removes the subscriber from the fan-out set but does
// NOT close the channel. This is the close-by-sender pattern — the only
// safe way to share a channel across a sender and a concurrent canceller
// without a close-during-send race.
//
// Pre-audit Unsubscribe called close(ch) while holding the broker
// mutex, while Publish snapshotted the subscriber slice under the same
// mutex and then sent to each entry inside deliverWithBackpressure
// WITHOUT holding the mutex. Between Publish's lock release and its
// `case sub <- chunk:` send, a concurrent Unsubscribe could remove the
// subscriber and call close(ch). The next send then panicked with
// "send on closed channel". See "Session Messages Data Race in SSE
// Fast-Path (May 2026)" § "Broker close-during-send race".
//
// # Concurrency with terminal close
//
// Subscribe acquires b.mu before appending to the subscribers slice.
// Publish's terminal close also acquires b.mu before snapshotting and
// closing. Therefore Subscribe is serialised against terminal close:
//
//   - Subscribe runs first → channel is in the subscribers slice → if
//     a Publish run was in flight when Subscribe ran, the channel is
//     in that run's terminal snapshot and gets closed by it.
//   - Subscribe runs after the LAST Publish run's terminal close → the
//     subscribers entry is fresh (post-delete) → the channel is not in
//     any current Publish run's snapshot → the channel is not closed
//     by THIS publisher cohort. It will be closed only if a future
//     Publish run starts and completes (delivering chunks of the next
//     turn) OR garbage-collected when no goroutine references it
//     (after `defer unsubscribe()` runs in handleSessionStream).
//
// The only production caller (handleSessionStream in server.go) does
// not rely on the channel closing on unsubscribe — it breaks out via
// `ctx.Done()` and lets `defer unsubscribe()` run. The contract change
// is invisible to that caller.
//
// Expected:
//   - sessionID is a non-empty string identifying an existing session.
//
// Returns:
//   - A buffered channel that receives StreamChunk values as they are
//     published. Capacity 64.
//   - A function that removes this subscriber from the fan-out set when
//     called. Idempotent: safe to call multiple times.
//
// Side effects:
//   - Adds the subscriber channel to the session's subscriber list.
func (b *SessionBroker) Subscribe(sessionID string) (<-chan provider.StreamChunk, func()) {
	ch := make(chan provider.StreamChunk, 64)

	b.mu.Lock()
	b.subscribers[sessionID] = append(b.subscribers[sessionID], ch)
	b.mu.Unlock()

	var unsubOnce sync.Once
	unsubscribe := func() {
		unsubOnce.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			subs := b.subscribers[sessionID]
			for i, sub := range subs {
				if sub == ch {
					b.subscribers[sessionID] = append(subs[:i], subs[i+1:]...)
					// Invariant I8: drop empty-slice entries to keep
					// the subscribers map clean. Pre-audit a long-
					// running server accumulated zero-length entries
					// for every session that ever had a subscriber.
					if len(b.subscribers[sessionID]) == 0 {
						delete(b.subscribers, sessionID)
					}
					// Invariant I1: NEVER close(ch) here. Publish is
					// the sole closer; closing here would race with
					// Publish's concurrent send (close-during-send
					// panic).
					return
				}
			}
		})
	}

	return ch, unsubscribe
}

// Publish forwards all chunks from the given source channel to all
// subscribers of a session, then closes every subscriber channel
// owned by the LAST in-flight Publish run as it exits.
//
// # Lifecycle and invariants
//
// On entry: increments active[sessionID] under b.mu (refcount). Concurrent
// Publish runs for the same sessionID push the count above 1 and BOTH fan
// chunks out to all current subscribers. Their chunks interleave in fan-
// out order; receivers MUST tolerate this ordering (production wires one
// Publish per turn so this only matters as a safety guarantee).
//
// Per-chunk: snapshots the current subscriber slice under b.mu, releases
// the lock, then calls deliverWithBackpressure for each entry. The lock
// is held only for the snapshot copy, never during the send — so a slow
// or stuck subscriber cannot block Subscribe or other concurrent Publish
// runs.
//
// On exit (when the source channel closes): decrements active[sessionID]
// under b.mu. The run that decrements to zero is the LAST out and owns
// the terminal close — it captures the current subscriber slice, deletes
// the map entries (active and subscribers), then closes every captured
// channel. All map mutations AND closes happen under b.mu so a
// concurrent Subscribe cannot insert into a stale map entry between
// delete and close.
//
// Earlier-exiting concurrent runs (count > 0 after decrement) just
// return — they neither close nor delete map state. This is the
// refcount-based extension of the close-by-sender invariant: there are
// many senders but exactly one closer, the LAST sender out.
//
// # Why close inside the lock
//
// The pre-audit implementation released b.mu before the close loop. A
// concurrent Subscribe between the unlock and the close loop could
// append a fresh channel to b.subscribers[sessionID] (re-creating the
// just-deleted entry). That channel was orphaned: not closed by THIS
// run's terminal loop, but visible to the next Publish run if any. By
// holding the lock across the close loop, we make Subscribe wait until
// terminal close completes — Subscribe then either sees an empty map
// entry (if it was the LAST run to close) or the in-flight Publish's
// active refcount > 0 (if a concurrent Publish is still running). Both
// cases are safe.
//
// Closing inside the lock is safe because close(chan) is a constant-
// time non-blocking operation; it never calls user code, never blocks
// on a receiver, and never triggers a deadlock against the broker
// itself (no broker code calls back into b.mu while a close is
// pending).
//
// Expected:
//   - sessionID is a non-empty string.
//   - chunks is a readable channel of StreamChunk values that will be
//     drained.
//
// Returns:
//   - Nothing. Synchronous; returns when the source channel closes.
//
// Side effects:
//   - Reads chunks from the source channel until close. Late chunks
//     that arrive AFTER the terminal Done still fan out to current
//     subscribers (the engine emits a post-Done provider_quota chunk
//     via makePostTurnQuotaEmitter — engine.go:2720 — and other
//     post-terminal observability chunks may follow).
//   - Sends each chunk to every active subscriber channel for the
//     session via deliverWithBackpressure.
//   - On observing a chunk with Done == true: decrements the per-
//     session active refcount IMMEDIATELY so IsPublishing reports
//     false from that point onward, even though Publish continues
//     consuming the source. See "Done releases active accounting"
//     below.
//   - On source close (last out, after the post-Done drain finishes):
//     removes the session's entries from active and subscribers maps
//     (when not already removed by the Done branch), and closes every
//     captured subscriber channel.
//
// # Done releases active accounting (May 2026 isStreaming-stuck fix)
//
// Before this fix Publish decremented active ONLY when the source
// channel closed. Any upstream stage that synthesised a terminal
// Done{...} chunk and then failed to close its outgoing channel
// promptly (engine outChan, session manager finalCh, dispatcher wrap
// out — every stage has a `defer close(...)` but any cascade defect
// holds the close open) left active[sessionID] > 0 indefinitely. GET
// /api/v1/sessions reported isStreaming:true long after the actual
// stream completed.
//
// The bug originally surfaced via the engine idle-stream watchdog
// (commit 3408c793): on a stalled provider HTTP body the engine
// emits a synthetic Done{StopReason: empty_turn} from
// processStreamChunks. The user-observed coordinator session
// 3b6ecb2c-1b63-462f-96fc-0f614eca10ef sat at isStreaming:true while a
// fresh SSE subscription returned [DONE] within the 250ms sealed-turn
// grace — proof the publisher was alive but not producing chunks even
// though IsPublishing reported true.
//
// Fix shape: split the receive-loop exit from the active-refcount
// decrement. The refcount drops to zero on first Done observed,
// so IsPublishing reports false immediately. The receive loop
// continues to fan out any post-Done chunks (engine post-turn quota
// emission, late observability chunks) but accounts as inactive for
// the API surface that builds session summaries.
//
// The contract is StopReason-agnostic on purpose: provider
// `message_stop`, dispatcher ctx-cancel emission, tool-execute error
// path, and the watchdog all set chunk.Done = true; the broker has
// no business inspecting WHY the stream ended. Any Done is the
// terminal signal for IsPublishing accounting.
//
// # Last-out / terminal-close interaction
//
// The last-out branch runs at source-close as before. When Done has
// already decremented active, the branch is a no-op for active map
// management; the terminal close still fires to close every subscriber
// channel. The subscriber-close MUST run at source-close (not at Done)
// because post-Done chunks still need to fan out to subscribers
// (engine's makePostTurnQuotaEmitter emits provider_quota chunks
// AFTER the terminal Done) — closing on Done would strand those.
func (b *SessionBroker) Publish(sessionID string, chunks <-chan provider.StreamChunk) {
	b.mu.Lock()
	b.active[sessionID]++
	b.mu.Unlock()

	// activeReleased flips to true on the first Done chunk so the
	// source-close branch does not double-decrement active. The fan-
	// out of post-Done chunks continues; only the IsPublishing
	// accounting is affected.
	activeReleased := false

	for chunk := range chunks {
		b.mu.Lock()
		subs := make([]chan provider.StreamChunk, len(b.subscribers[sessionID]))
		copy(subs, b.subscribers[sessionID])
		b.mu.Unlock()

		for _, sub := range subs {
			b.deliverWithBackpressure(sessionID, sub, chunk)
		}

		// Done-releases-active: the terminal frame has fanned out to
		// every current subscriber. Drop the active accounting NOW so
		// GET /api/v1/sessions stops reporting isStreaming:true,
		// regardless of how long the upstream takes to close the
		// source. Post-Done chunks (engine post-turn quota, late
		// observability) still flow through the receive loop and fan
		// out — but the publisher is no longer "active" by the broker
		// contract.
		if chunk.Done && !activeReleased {
			b.releaseActive(sessionID)
			activeReleased = true
		}
	}

	// Source closed. Run the terminal-close branch. When Done already
	// released active, the b.releaseActive call is a no-op for active
	// accounting (already removed); the subscriber-close still fires
	// from the last-out path below.
	if !activeReleased {
		b.releaseActive(sessionID)
	}
	b.terminalCloseIfLastOut(sessionID)
}

// releaseActive decrements active[sessionID] under b.mu. When the
// count drops to zero, the entry is removed from the map so a
// subsequent Subscribe sees a fresh map state.
//
// Subscribers are NOT closed here — that happens at terminal close
// only after the source channel has fully drained (so post-Done
// chunks reach subscribers). The split between active accounting and
// subscriber close is the load-bearing shape of the May 2026 fix:
// IsPublishing flips on Done; subscribers stay live until source
// close.
//
// Side effects:
//   - Decrements active[sessionID].
//   - When count reaches zero, deletes the active map entry.
func (b *SessionBroker) releaseActive(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active[sessionID] <= 0 {
		// Defensive — should not happen given Publish increments at
		// entry and releases at most once per call. Idempotent guard
		// so a future double-call cannot push active negative.
		return
	}
	b.active[sessionID]--
	if b.active[sessionID] == 0 {
		delete(b.active, sessionID)
	}
}

// terminalCloseIfLastOut performs the subscriber-close branch when
// this Publish call is the last out (no other Publish run is in
// flight for the same sessionID). Called at source-close — never on
// Done — so post-Done chunks still reach subscribers via the receive
// loop's fan-out.
//
// Side effects:
//   - When no other Publish run is in flight: captures the current
//     subscriber slice, removes it from the subscribers map, and
//     closes every captured channel under b.mu.
//   - When another Publish run is still active: no-op.
func (b *SessionBroker) terminalCloseIfLastOut(sessionID string) {
	b.mu.Lock()
	if b.active[sessionID] > 0 {
		// Another Publish run is still in flight for this session.
		// It owns the terminal close.
		b.mu.Unlock()
		return
	}
	subs := b.subscribers[sessionID]
	delete(b.subscribers, sessionID)
	// Close inside the lock. The godoc on Publish explains why this
	// is the right shape (eliminates the Subscribe-during-terminal-
	// close orphan window) and why it is deadlock-free (close is
	// constant-time, no callback into broker code).
	for _, sub := range subs {
		close(sub)
	}
	b.mu.Unlock()
}

// deliverWithBackpressure attempts to send a chunk to a single subscriber
// channel under bounded backpressure. The original Publish loop used a
// non-blocking `select { case sub <- chunk: default: }` send — a slow
// subscriber's chunk was silently lost with zero observability. Drop #4
// replaces that with:
//
//   - A non-blocking fast path (the common case: subscriber is keeping
//     up with the producer).
//   - A short grace period blocking send for transient bursts (a
//     subscriber that's momentarily full but draining).
//   - A counted+logged drop if the channel is still full at the deadline.
//
// Crucially the grace period is per-subscriber, not per-broker — a
// permanently stuck subscriber waits at most brokerSubscriberGracePeriod
// before its chunk drops, so siblings of a stuck subscriber never starve.
//
// # Send safety under the close-by-sender invariant
//
// The send happens WITHOUT b.mu held. That is safe because of invariant
// I1 (single-closer per channel): the sub channel is only closed by the
// LAST Publish run's terminal close, which holds b.mu. Sub channels are
// never closed by Unsubscribe. Therefore, while a Publish run is mid-
// flight (active > 0 for this session), no concurrent close on this
// sub can fire — the close path is guarded by `b.active[sessionID] == 0`
// AFTER the current run's decrement.
//
// Expected:
//   - sessionID identifies the session for log attribution.
//   - sub is a non-nil subscriber channel.
//   - chunk is the StreamChunk to deliver.
//
// Side effects:
//   - May send chunk on sub.
//   - On deadline: increments b.dropped atomically and emits a slog
//     warning carrying sessionID and an EventType marker so the drop is
//     attributable in production logs.
func (b *SessionBroker) deliverWithBackpressure(sessionID string, sub chan provider.StreamChunk, chunk provider.StreamChunk) {
	// Fast path — the common case under normal load. Equivalent to the
	// original `default:` non-blocking send when capacity is available.
	select {
	case sub <- chunk:
		return
	default:
	}

	// Slow path — channel is momentarily full. Wait up to the grace
	// period for the subscriber to drain. Using time.NewTimer over
	// time.After lets us reclaim the timer to keep allocation pressure
	// down on a hot fan-out path.
	timer := time.NewTimer(brokerSubscriberGracePeriod)
	defer timer.Stop()
	select {
	case sub <- chunk:
		return
	case <-timer.C:
		b.dropped.Add(1)
		// Per-drop logging is intentional: the previous silent drop was
		// the structural-invisibility failure mode. A handful of dropped
		// chunks per session is tolerable noise; the alternative —
		// log nothing, count nothing — is the bug we're fixing. The
		// EventType marker `streaming.broker.drop` lets log scrapers
		// attribute and aggregate without parsing free-text.
		slog.Warn("session broker dropped chunk under sustained backpressure",
			"session_id", sessionID,
			"event_type", "streaming.broker.drop",
			"grace_period_ms", brokerSubscriberGracePeriod.Milliseconds(),
			"total_drops", b.dropped.Load(),
		)
	}
}
