# ADR: Skill-guard circuit breaker and deterministic always-active skill injection

## Status

Accepted (September 2026)

## Context

The deterministic skills-first gate (see
`deterministic-skills-first-gate.md`) rejects any non-`skill_load` tool
call until the always-active skills are loaded. When a provider
repeatedly refuses the skills-first directive — re-issuing rejected
calls on every continuation — the gate could deadlock the turn: no tool
call was ever admitted, so the loop could neither progress nor
terminate cleanly.

## Decision

1. The skill gate carries a circuit breaker:
   `SkillGuardCircuitBreakerThreshold = 3`. After three consecutive
   skills-first guard rejections the gate is marked satisfied and the
   call proceeds, bounding the worst case.
2. Always-active skill bodies are deterministically injected as a
   session system message at session start, and the corresponding
   `skill_load` call is marked completed. This preserves lean
   injection: the skill content is present without each turn paying
   the token cost of re-loading it.

The words "unconditionally" and "always" in the code refer to the
existing always-active-skills vocabulary and the bounded circuit
breaker, not to bypassing checks.

## Consequences

- A provider that repeatedly ignores the skills-first directive now
  proceeds after three rejections instead of deadlocking the turn.
- Worst case is bounded at three guard rejections per session.
- Always-active skills are present in every session context without
  requiring an explicit tool call per turn.
- Behaviour is pinned by `features/skills_guarantee.feature`.
