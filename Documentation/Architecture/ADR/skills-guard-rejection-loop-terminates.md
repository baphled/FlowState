# ADR: Trip consecutive_tool_rejection on skills-guard rejection loops

## Status

Accepted (September 2026)

## Context

The deterministic skills-first gate (see
`deterministic-skills-first-gate.md`) rejects any non-`skill_load` tool
call with the output string `You must load your always-active skills
via skill_load(name=...)` until the always-active skills are loaded.
When a provider ignores that directive and re-issues the same rejected
tool call every continuation, the `batchAllRejected` classification in
`internal/engine/toolloop.go` did not recognise the skills-guard
rejection as a rejection: only the runtime-gate string (`not available
to agent`) was classified. As a result `consecutiveRejectedToolCalls`
never incremented, and after the forced-summary grace round the
guard's own circuit breaker auto-satisfied the gate, after which the
loop executed `bash` on every iteration with no cap left to trip
(hundreds of thousands of provider calls observed in the RED spec).

## Decision

1. The skills-guard rejection output (`must load your always-active
   skills via`) is classified in `batchAllRejected` so
   `consecutiveRejectedToolCalls` increments on every rejected batch.
2. When three consecutive skills-guard rejections accumulate, the loop
   terminates immediately with `StopReasonToolLoopExceeded`, checked
   after the batch and BEFORE the guard's circuit-breaker fourth-call
   auto-satisfy can wedge the session into an unbounded execute loop.

The word "always" in the matched string refers to the existing
always-active-skills vocabulary, not a new unconditional policy.

## Consequences

- A provider that repeatedly ignores the skill_load directive now
  terminates deterministically after three rejected batches instead of
  spinning after the circuit breaker trips.
- Other rejection kinds (tool-not-found, validation) retain the
  existing forced-summary grace behaviour.
- Covered by the spec `stops after 3 consecutive batches rejected with
  the skill_load directive` in `internal/engine/tool_loop_cap_test.go`.
