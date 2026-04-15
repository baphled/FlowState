---
title: ADR - Tool-Call Atomicity in Context Compaction
created: 2026-04-14
modified: 2026-04-14T10:12
tags:
  - project/flowstate
  - topic/architecture
  - topic/adr
  - topic/context-compaction
  - topic/provider
type: adr
status: proposed
adr_group: Context Management
---

# ADR: Tool-Call Atomicity in Context Compaction

## Context

FlowState's context compression system (see [[Context Compression System]]) is being designed to keep long-running sessions within provider token budgets by replacing older message history with placeholders (L1 micro-compaction) or model-generated summaries (L2 auto-compaction). The compression layer sits between the session store and the provider wire renderer — it mutates the ordered list of `provider.Message` values that will eventually be serialised for the chat completion call.

That ordered list is not a flat stream of free-form messages. It encodes a strict protocol required by every supported provider:

- An `assistant` message carrying one or more `tool_use` blocks MUST be followed immediately by the matching `tool_result` message(s).
- Each `tool_result` carries a `tool_call_id` (or `tool_use_id`) that refers back to a specific `tool_use` in the preceding assistant turn.
- When an assistant turn fans out N parallel tool calls, exactly N `tool_result` entries — one per id — must appear together as the next message block.

FlowState's Anthropic provider has historically enforced this invariant for *live* traffic, as documented in [[Anthropic Provider Tool Call Message Handling]]:

> Tool results must immediately follow their tool_use (no interspersed messages) — Missing assistant message → API doesn't know what tool was called → 400 error.

However, nothing today extends that invariant to context manipulation. The compression plan is silent on it. If L1 drops a `tool_result` while keeping its `tool_use`, or L2 summarises an assistant turn whilst leaving its trailing `tool_result` messages intact, we re-introduce an entire class of bugs the provider layer was recently taught to avoid.

### The orphan-id failure mode

Commit `cb33d19` ("fix(provider): translate tool-call ids on cross-provider failover") added a deterministic id translator in `internal/provider/shared/tool_ids.go:65-81`. The translator is stateless and pure — `sha256(canonical)[0:24]` with a `toolu_` or `call_` prefix. Determinism is the *only* correctness mechanism. The wire id for a given tool call is reproducible from the canonical id and nothing else. There is no side table mapping translated ids back to the tool calls they belong to.

Two consumers depend on that determinism:

1. **Assistant rendering** (`internal/provider/openaicompat/openaicompat.go:65-78`) — when an assistant message carries `tool_use` blocks, each block's id is hashed into `tool_calls[].id` on the outgoing wire payload.
2. **Tool-result rendering** (`internal/provider/openaicompat/openaicompat.go:41-49`) — for a message with `Role: "tool"`, the renderer emits **one `ToolMessage(content, wireID)` per entry in `m.ToolCalls`**. Parallel tool calls fan out into one outgoing message per id.

The renderer has no cross-message awareness. It cannot detect an orphan id. If compaction deletes the assistant message that declared `tool_use` id `toolu_xyz` but leaves the `tool_result` carrying that same id, the OpenAI-compatible wire format will contain a `tool` message referring to a `tool_call_id` that appears nowhere in the conversation. OpenAI-compatible endpoints respond with HTTP 400. Anthropic's endpoint rejects the reverse — a `tool_use` with no matching `tool_result` — with the same class of error.

The memory entry `project_flowstate_failover_bugs` records that tool_use_id translation was never done across providers before `cb33d19`. We should not ship a compaction layer that needs a second cross-cutting fix of the same shape.

### Why this needs an ADR and not just code

The adjacency invariant exists in the provider renderer as a natural consequence of how messages are emitted one-by-one. The compaction layer, by contrast, reasons about *ranges* of messages, replaces them with substitutes, and decides what to drop. It is the first layer in FlowState that can produce a structurally valid `[]Message` which nonetheless violates the protocol. We need an explicit invariant that every implementation of compaction — present and future — must preserve, and a shared vocabulary ("compactable unit") to describe it.

## Problem

1. **Compaction operates on message ranges** — without a pairing rule, a range boundary can fall between a `tool_use` and its `tool_result`.
2. **Parallel tool calls span multiple messages** — an assistant turn with N tool calls and its N trailing `tool_result` messages form a group of size N+1; a naive per-message compactor will split the group.
3. **Id translation is one-way** — the sha256 prefix means there is no way to reconstruct a missing half of a pair from the other half.
4. **Summaries cannot reference ids** — once compacted, the canonical tool_use_ids are gone from the wire. An L2 summary that mentions `"the tool call toolu_xyz returned …"` becomes a dangling reference.
5. **Both tiers need the same rule** — L1 (placeholder substitution) and L2 (LLM-generated summary) use different strategies but both produce a modified `[]Message`; the invariant applies equally.
6. **Silent failure mode is a 400** — no partial output, no graceful degradation. A single orphan id fails the entire turn at the provider boundary, losing all in-flight work.

## Decision: Compactable Units, not Compactable Messages

We define a **compactable unit** as the smallest atomic grouping the compaction layer is permitted to act on. Compaction operates at the unit level; the raw message list is never partitioned mid-unit.

### Unit definition

A compactable unit is exactly one of:

- **(a) Solo unit** — a single message whose `Role` is one of `user`, `assistant` (with no `tool_use` blocks), or `system`. This is a self-contained message with no cross-message dependency.
- **(b) Single tool pair** — the two-message sequence `(assistant-with-one-tool_use, tool_result)`. The assistant message carries exactly one `tool_use` block; the immediately following message has `Role: "tool"` and carries the matching `tool_call_id`.
- **(c) Parallel fan-out group** — the (N+1)-message sequence `(assistant-with-N-tool_use-blocks, tool_result_1, …, tool_result_N)` where every `tool_result_i` references one of the N `tool_use` ids in the preceding assistant turn. The renderer's "one outgoing `ToolMessage` per id" contract (`openaicompat.go:45-48`) maps cleanly onto the unit's internal structure.

Units of type (b) and (c) are **indivisible**. No compaction pass may:
- Drop a proper subset of the messages in a unit.
- Insert a placeholder or summary in the middle of a unit.
- Reorder messages such that a unit is interrupted by a message from another unit.

### The invariant

> **Compaction Atomicity Invariant.** For every compactable unit *U* in the pre-compaction message list, the post-compaction message list contains either *U* in its entirety, or a single substitute message that replaces the whole of *U*. No intermediate state is permitted.

### Implications for L1 (micro-compaction)

L1 replaces older units with short placeholders such as `[elided: 3 messages]`. Rules:
- Before replacing any unit, the L1 pass identifies unit boundaries using the structural walker defined below.
- Placeholder substitution replaces the *entire* unit atomically. A type-(b) or type-(c) unit becomes a single `user` or `system` placeholder message; its `tool_use` and `tool_result` entries are dropped together or kept together.
- The placeholder carries no `tool_call_id` and is not itself a tool message.

### Implications for L2 (auto-compaction)

L2 replaces older ranges with a summary generated by the model. Rules:
- The summary range is extended outward to the nearest unit boundary on both ends before the summary is requested.
- **Summaries MUST NOT contain references to specific tool_call_ids** that existed in the compacted history. Enforced at the summary-prompt level (system prompt forbids reproducing raw `toolu_*` / `call_*` strings) and checked at substitution time by a regex guard; any summary containing a residual id is regenerated.
- The summary message is emitted as a solo (type-a) unit.

### Structural unit walker

Identifying units is a linear pass over the message list. The walker lives at `internal/context/compaction_units.go` and is the single authoritative source of unit boundaries. Pseudocode:

```go
func walkUnits(msgs []provider.Message) []Unit {
    var units []Unit
    i := 0
    for i < len(msgs) {
        m := msgs[i]
        if m.Role == "assistant" && len(m.ToolCalls) > 0 {
            n := len(m.ToolCalls)
            end := i + 1 + n
            if end > len(msgs) {
                return nil // malformed — refuse to compact
            }
            units = append(units, Unit{Kind: ToolGroup, Start: i, End: end})
            i = end
            continue
        }
        units = append(units, Unit{Kind: Solo, Start: i, End: i + 1})
        i++
    }
    return units
}
```

Compaction tiers MUST consume its output rather than re-deriving boundaries ad hoc.

### Interaction with provider id translation

Because compaction operates above the provider layer, the canonical ids it sees are whatever the session store holds. The provider-level translator (`tool_ids.go`) continues to run *after* compaction, on whatever units survived. The invariant guarantees the translator only ever sees balanced pairs.

## Consequences

### Positive

- **No orphan ids across compaction.** The class of failure that `cb33d19` fixed for failover cannot re-emerge via the compaction boundary.
- **Provider-agnostic correctness.** The invariant holds identically for Anthropic (strict `tool_use` / `tool_result` sequencing), OpenAI-compatible endpoints, and any future provider whose wire format distinguishes tool calls from tool results.
- **Shared vocabulary.** "Compactable unit" gives L1, L2, future hierarchical compaction tiers, and any non-compaction consumers a common primitive for reasoning about message groups.
- **Walker is testable in isolation.** Unit identification is decoupled from replacement strategy.
- **Prevents accidental regression.** The guard against tool_call_id references in summaries catches the subtle failure mode where an LLM helpfully "cites its sources" and produces a wire-breaking dangling reference.

### Negative

- **Coarser compaction granularity.** The minimum compactable region is the unit, not the message. A parallel fan-out group of 8 tool calls is a 9-message unit that is either kept or dropped together.
- **Occasional unit exceeds threshold.** A single large fan-out group may exceed the L1 threshold on its own. Such units are simply uncompactable at L1; L2 is the escape hatch.
- **Summary prompt complexity.** L2 summary prompts must be written so the model does not emit raw `tool_call_id` strings.
- **Slight latency at the boundary.** The unit walker runs on every compaction pass. Linear in message count, negligible in practice.
- **Validator needed.** A post-compaction assertion runs `walkUnits(post)` and verifies every `Role: "tool"` message is part of a complete unit. Debug-build-only in production; always-on in tests.

## Alternatives Considered

### A. Per-message compaction with a best-effort repair pass

Compact at the raw message level, then run a "repair" pass that detects orphaned halves and drops them.

**Rejected.** The repair pass is itself a second failure surface. Any bug in orphan detection produces a wire-breaking payload. Additionally, a tool_result dropped as "orphaned" may have been the only carrier of a piece of reasoning the summary needed.

### B. Strip tool_call_ids during compaction

Remove the `tool_call_id` fields from any messages that survive compaction, letting the wire format fall back to "loose" tool-result matching.

**Rejected.** Anthropic's API enforces strict id-based matching. OpenAI-compatible endpoints similarly require `tool_call_id` on every `tool` role message. There is no "loose" mode to fall back to.

### C. Keep `tool_use` messages, drop `tool_result` content

Preserve the assistant's decision to call a tool but drop the tool's output.

**Rejected.** Unbalanced. A `tool_use` without its matching `tool_result` is an error in both provider wire formats.

### D. Keep `tool_result` messages, drop the `tool_use` assistant turn

The mirror of (C).

**Rejected.** Also unbalanced. A `tool_result` whose preceding `tool_use` is absent refers to a `tool_call_id` the provider has never seen on the wire. Instant 400.

### E. Defer compaction of tool-bearing ranges

Never compact units of type (b) or (c). Only apply compaction to consecutive runs of solo units.

**Rejected.** In practice tool-call traffic dominates token cost for agent sessions. A compaction strategy that refuses to touch tool traffic would effectively disable compaction for the agent use case that needs it most.

### F. Cross-provider canonicalisation before compaction

Rewrite every id to a single canonical namespace before compaction.

**Rejected as unnecessary.** Within a single session the canonical ids are already stable. Pairing inside compaction uses position plus tool-use count, not the ids themselves.

## Implementation Notes

- The unit walker lives in `internal/context/compaction_units.go` and is the single caller-facing entry point for identifying units. L1 (`internal/context/micro_compaction.go`) and L2 (`internal/context/auto_compaction.go`) import it.
- Unit tests cover: solo runs, single pair, 2-way fan-out, 8-way fan-out, two adjacent pairs, pair followed by solos, solos followed by pair, malformed truncated group (returns nil), empty input.
- The id-reference guard for L2 summaries is a single regex: `(toolu|call)_[A-Za-z0-9]{16,}`. Any match triggers regeneration. False positives cost one extra LLM call; false negatives would leak a dangling reference onto the wire.
- The debug-build post-compaction validator runs `walkUnits(post)` and asserts (i) every tool-role message belongs to a unit, (ii) no unit straddles a placeholder or summary boundary.

## Status

- **Proposed** — pending review before being promoted to accepted and wired into the compression system design.

## Related

- [[Context Compression System]] — the plan this ADR constrains
- [[ADR - View-Only Context Compaction]] — sibling invariant on compaction scope
- [[Anthropic Provider Tool Call Message Handling]] — the live-traffic adjacency invariant this extends
- [[ADR - Recall Context Management]] — external context store, the layer below compaction
- Commit `cb33d19` — "fix(provider): translate tool-call ids on cross-provider failover"
- `internal/provider/shared/tool_ids.go:65-81` — deterministic id translator
- `internal/provider/openaicompat/openaicompat.go:41-49` — per-id tool message fan-out
- `internal/provider/openaicompat/openaicompat.go:65-78` — assistant `tool_calls[].id` rendering

---

*Snapshot note: the primary, editable copy of this ADR lives in the FlowState
Obsidian vault at `Documentation/Architecture/ADR/`. This file is a verbatim
snapshot checked into the repository so clone-only readers can see the
invariants that regression tests encode. Refresh in the same change-set as
any vault edit; see `docs/adr/README.md` for the rule.*
