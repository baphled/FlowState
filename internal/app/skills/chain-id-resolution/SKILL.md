---
name: chain-id-resolution
description: Resolve the planner-provided chainID before reading or writing the coordination store — never leave the {chainID} placeholder literal, never invent a namespace
category: Agent Coordination
tier: domain
when_to_use: Any time a specialist prompt references a {chainID}/... coordination_store key; always-active for specialists that the planner delegates to
related_skills:
  - discipline
  - pre-action
---

# Skill: chain-id-resolution

## What I do

I enforce the contract that every specialist's coordination_store read or write uses the concrete chainID passed by the planner in the delegate message — never the literal `{chainID}/...` placeholder and never a free-form namespace.

## When to use me

- Always-active for evidence-writing specialists (explorer, librarian, analyst) and plan-handling specialists (plan-writer, plan-reviewer).
- Before calling `coordination_store` read or write with any key that contains a `{chainID}` placeholder in the prompt instruction.
- When a delegate message lacks an explicit `chainID` — the correct response is to ask the caller, not to invent one.

## Chain ID Resolution (MANDATORY)

Any `{chainID}/...` key that appears in your system prompt is a **placeholder**, not the literal string to read or write. Read the delegate message you were invoked with: the planner passes the concrete `chainID` value (for example `chainID=plan-auth-2026-04-23`) alongside the target coordination_store key. Substitute that value into the key before calling `coordination_store`.

- If the delegate message does not state a `chainID`, **ask the caller via your reply** — do NOT invent one and do NOT read or write the literal `{chainID}/...` key.
- All your reads and writes MUST live under the planner-provided chainID prefix (`<chainID>/...`). Writing to any other namespace — for example `flowstate/codebase-findings`, `flowstate/external-refs`, `flowstate/analysis`, `flowstate/plan`, `flowstate/review`, or a free-form `research-findings-<topic>` key — is a contract violation that strands your output where downstream specialists and the planner's circuit-breaker will not find it.

## Why this skill exists

Historically the same clause was copy-pasted across five specialist manifests (explorer, librarian, analyst, plan-writer, plan-reviewer). Prose drift between copies is the entry vector for regressions — the live failure documented in "Coordination Store Namespace Drift in Delegation Prompts (April 2026)" had explorer writing to `flowstate/codebase-findings` and librarian writing to a free-form `research-findings-<topic>` key while the planner-declared keys sat empty at 2 bytes.

Consolidating into a single skill means:

- One canonical source edited in one place.
- A new specialist that delegates through the planner opts in by adding `chain-id-resolution` to `always_active_skills` — no copy-paste.
- The prose reaches the model through `engine.BuildSystemPrompt`, which bakes every declared always-active skill into the system prompt at delegation time (engine.go:1238-1240).

## Related skills

- `discipline` — Step-execution discipline that forbids silent shortcuts, including inventing a chainID instead of asking.
- `pre-action` — Stop-and-clarify before the first coordination_store call: confirm which chainID you actually have.

## KB Reference

`~/vaults/baphled/1. Projects/FlowState/Refactors/Chain ID Resolution Skill Consolidation (April 2026).md`
