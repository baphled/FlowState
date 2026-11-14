# Deferred findings — provider exec-loop (2026-11-14)

Two review findings from the cancelled-stream hardening pass were deferred as
non-blocking. They do not affect the scoped outcome (cancelled streams return
`ctx.Err()` and never persist partial assistant messages).

## Finding 1 — `replayInto` pending-content flush is implicit

`replayInto` buffers the last assistant message's content in a local `pending`
variable and flushes it only when the next tool-call message arrives. A
trailing assistant message with no following tool calls is simply not appended
to history (correct for provider calls, but the invariant is undocumented
beyond a one-line comment). If a future event type is introduced between
`assistant.message` and `assistant.tool_calls`, the pending content could be
attached to the wrong call.

**Action:** extract the pending/flush logic into a small named type with
documented invariants, or assert the ordering in the contract checker.

## Finding 4 — contract-checker coverage for cancelled streams

The contract checker validates replay/history shape for the happy path and
budget/fenced stops, but has no case for a cancelled mid-turn stream. The
unit spec (`a cancelled stream returns ctx.Err() without persisting a partial
assistant message`) covers the behaviour; the checker-level invariant
(replayed history after a cancelled turn contains no orphan/partial
assistant message) is not separately enforced.

**Action:** add a cancelled-stream fixture to the contract checker so the
invariant is validated independently of the unit spec.
