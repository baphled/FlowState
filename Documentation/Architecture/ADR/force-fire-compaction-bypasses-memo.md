# ADR: Force-fire compaction path skips the H2 memoisation cache

## Status

Accepted

## Context

Phase 3 of the compaction refactor introduces memoisation for summary
generation (H2): identical compaction inputs reuse a cached summary
instead of re-running the summariser chain. During review the question
arose whether the manual `/compact` force-fire path should also hit the
memo.

The word "always" (a high-risk policy keyword) appears in the code
comment describing this behaviour, so this ADR records the decision.

## Decision

The force-fire path (the only caller today) **always** bypasses the
memo so a manual `/compact` unconditionally regenerates a fresh
summary. Only automatic (budget-triggered) compaction consults the
memo cache.

## Consequences

- Manual compaction is never served a stale summary: the user's
  explicit request always reflects current session state.
- Memo invalidation (phase3 memo_invalidation.feature) covers the
  automatic path; the force path needs no invalidation because it
  never reads the cache.
- Slight extra cost per manual /compact is acceptable — manual
  compaction is rare and user-initiated.
