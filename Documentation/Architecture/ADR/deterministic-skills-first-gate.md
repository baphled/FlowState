# ADR: Deterministic Skills-First Gate with Circuit Breaker

Date: 2026-09-09
Status: Accepted

## Context

The skills-first guard (July 2026) rejected any non-`skill_load` tool call
until the agent had loaded its always-active skills. In practice models
frequently ignored the rejection and re-issued the same tool call,
wedging the session: the guard rejected forever and no useful work ran.

## Decision

The guard becomes deterministic and self-limiting in
`internal/engine/skill_gate.go`:

1. **Auto-inject first.** When always-active skill content is resolvable
   (construction-time skills slice or `SkillsResolver`), the gate bakes
   the full skill bodies into the session history as a system message,
   marks skill_load satisfied, and proceeds with the original call. The
   happy path therefore never rejects.
2. **Circuit breaker.** When no content is injectable and the guard has
   rejected three consecutive calls (`SkillGuardCircuitBreakerThreshold`)
   without the model complying, the gate auto-satisfies itself, appends
   a notice to the session, and lets the fourth call through.
3. **Streak reset.** A successful `skill_load` clears the per-session
   rejection counter.

## Consequences

- The guard can no longer deadlock a session.
- Session history grows by one system message on first non-compliant
  call (bounded by the always-active skill set size).
- The rejection path remains for the first three calls, preserving the
  behavioural nudge for compliant models.
