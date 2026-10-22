# ADR: Budget Gate Always Preserves the Final User Message

Date: 2026-09-27
Status: Accepted

## Context

The pre-dispatch failover budget gate trims oversized requests before
the provider call. A trim that dropped the final user message would
send the model a conversation whose last turn is a tool result or an
assistant reply, so the model would have nothing new to answer — a
silent, unrecoverable failure from the user's perspective.

## Decision

The budget gate's trim path (`internal/engine/budget_gate.go`,
`TrimForDispatchBudget`) always preserves the final non-system message
when it is the pending user turn. Fixed-head semantics, mirroring the
head-cap behaviour used elsewhere in the engine, apply:

1. The final user message is always kept, even under budgets tight
   enough that older messages must be dropped wholesale.
2. Trimming proceeds only as far as the budget requires; it never
   removes more history than necessary to fit.
3. If even the final user message alone exceeds the budget, the gate
   fails loudly (error) rather than silently dispatching a request
   with no user turn.

The word "always" in the code comments describes this invariant: the
final user message has no per-call opt-out because a dispatch without
it is by definition wrong.

## Consequences

- Dispatches under tight budgets lose the oldest history first and
  keep the pending turn; the model always has something to respond to.
- Oversized single messages surface as explicit errors to the caller
  instead of silent context corruption.
