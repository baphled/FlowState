# ADR: Always keep one hot-tail message in the token-bounded overflow rebuild

**Status:** Accepted
**Date:** 2026-09-09
**Context:** dev-swarm-893016a5baf8 U3 (context-resilience)

## Context

When the proactive overflow gate refuses a mid-tool-loop request, the
engine force-compacts and rebuilds the live message slice via
`rebuildContextWindowTokenBounded` (internal/engine/compaction.go).
The rebuild drops oldest hot-tail messages until the estimated token
cost of prefix + summary + tail fits the usable budget
(`limit - outputReserveFor`).

The tail-dropping loop's guard is `len(messages) > 1`: it may shrink
the tail down to a single message but never to zero, even when the
estimate of that single message alone exceeds the usable budget.

## Decision

At least one tail message is **always** kept. The word-counter-based
estimate is an approximation; the newest message (the live user turn /
most recent tool exchange) carries the turn's operative context, and
dropping it would desynchronise the tool loop from the tool call it is
currently answering. When even a single message overflows the estimate,
the rebuild intentionally returns an over-budget slice, and the
post-compaction retry stamps `WithSkipContextWindowOverflowCheck` so
the real provider — whose actual limit may exceed the fallback-derived
estimate — renders the final verdict. `streamWithToolLoop`'s
`max_overflow_retries` budget bounds the loop; the rebuild never
manufactures an empty-tail window in pursuit of a fit.

## Alternatives considered

- Drop to zero tail messages when nothing fits: desynchronises the
  tool loop and can produce a request with no operative context.
- Clamp the output reserve (`limit/4`): breaks the four canonical
  reserve specs pinned in engine_test.go; explicitly rejected
  (fixed 4096 default retained).

## Consequences

- The rebuild is guaranteed non-degenerate: the provider always sees
  the newest exchange.
- Pathological single-message overflows delegate the verdict to the
  provider via the skip-gate retry, bounded by max_overflow_retries.
