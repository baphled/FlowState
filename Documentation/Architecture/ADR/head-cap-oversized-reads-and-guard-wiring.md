# ADR: Head-Cap Oversized Unbounded Reads and Warn on Oversized Tool Results

Date: 2026-09-26
Status: Accepted

## Context

Unbounded read tools (a file read with no offset/limit) can return an
entire multi-megabyte file into the model context, blowing the token
budget on a single tool call. Separately, the main engine was
dispatching guard-less tool instances in some paths, silently bypassed
by permissive providers emitting out-of-schema tool calls (see
`internal/tool/toolset/app_tools.go`, the comment block above
`BuildAppTools`). The read tool therefore needed an opt-in head-cap and
the guard wiring needed to be unconditional whenever a guard exists.

## Decision

`BuildAppTools` wires the three mutating-filesystem tools (bash, read,
write) through `NewWithGuard` whenever `guard` is non-nil, and the read
tool through `read.NewWithGuardAndLimits(guard, readOversizeThreshold,
readHeadLines)`:

1. **Head-cap applies unconditionally above the threshold.** When a
   positive `readOversizeThreshold` is configured and a read result
   exceeds it, the read tool returns the leading `readHeadLines` lines
   instead of the whole file — there is no per-call opt-out, because a
   caller that could opt out of the cap is exactly the oversized read
   the cap exists to stop. A threshold of zero disables the cap
   entirely (strictly opt-in per engine).
2. **`New` / `NewWithGuard` selection is deterministic.** The nil-guard
   branch always uses `read.New` (no cap, no guard) while the non-nil
   branch always uses the guarded constructor, so the head-cap is
   strictly opt-in and the two constructors are never mixed within one
   engine build.
3. **Guard wiring closes the bypass.** Without this wiring the engine
   dispatched guard-less tool instances and the permissions.yaml and
   Plan-mode overlays were silently bypassed — a permissive provider
   emitting an out-of-schema tool call would land on guard-less
   Execute and the file write would succeed. A nil guard preserves the
   legacy fully-permissive behaviour for tests and call sites that
   explicitly opt out of pathguard enforcement.
4. **Warn on oversized tool results.** Results exceeding the threshold
   are surfaced with a warning so the caller knows truncation occurred
   rather than receiving a silently shortened file.

Mirrors the per-manifest tool factory at `app.buildToolsForManifestWithStore`
so both engines (main + delegate) honour the same pathguard decisions.

## Consequences

- An oversized unbounded read costs at most `readHeadLines` lines of
  context instead of the whole file; the warning tells the model to
  re-read with an explicit offset/limit if it needs more.
- Operators control the cap via `readOversizeThreshold` / `readHeadLines`
  in config; zero threshold restores today's uncapped behaviour.
- Guard-less construction is only reachable by explicitly passing a nil
  guard, which is reserved for tests and call sites that document why
  they bypass pathguard.
