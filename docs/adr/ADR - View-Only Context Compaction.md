---
title: ADR - View-Only Context Compaction
created: 2026-04-14
modified: 2026-04-14T10:12
tags:
  - project/flowstate
  - topic/architecture
  - topic/adr
  - topic/recall
  - topic/context-compression
type: adr
status: proposed
adr_group: Provider & Backend
---

# ADR: View-Only Context Compaction

## Context

FlowState is introducing a context compression system (see [[Context Compression System]]) that shortens the message history sent to the provider once a session grows past a threshold. The plan describes "replacing" older turns with summaries, offloading cold messages to files, and promoting durable facts into a knowledge store.

That language is ambiguous on a question the architecture has never explicitly answered: **does compaction mutate the session's canonical transcript, or does it only transform the view of the transcript that is handed to the LLM?**

FlowState's existing code already answers the question *implicitly* — the raw transcript is canonical — but nothing enforces the invariant and no document names it. The compression plan can be read either way, and a reasonable engineer implementing T1–T10 could legitimately choose the destructive interpretation and break several guarantees at once.

### What the code currently guarantees

1. **Session persistence is metadata-only.** `internal/session/persistence.go:14-20, 34-54` shows that `SessionRecord` persists `{ID, ParentID, AgentID, Status, CreatedAt}`. Messages are never written to disk as part of session persistence.
2. **Message history lives only in memory.** `internal/session/accumulator.go:36-38, 64-81` appends streamed assistant turns onto the in-memory `session.Messages` slice held by `Manager`.
3. **The `session.Message` struct carries no provider-specific correlation state.** `internal/session/manager.go:31-39` defines `{ID, Role, Content, ToolName, ToolInput, Timestamp}`. There is no `ToolCalls` field and no `tool_call_id`.
4. **Tool-call ids are tracked in a separate store.** `internal/recall/FileContextStore` is the authority for the provider-neutral ↔ provider-specific id mapping.
5. **The fidelity test asserts byte-exact reconstruction.** `internal/session/session_resumption_fidelity_integration_test.go:38-43, 75-107, 159-182` drains a session, resumes it, and asserts that `session.Messages` is reconstructed with the exact length, exact `Role`, exact `Content`, and exact `ToolName`/`ToolInput` of the original.

### What the compression plan says

The plan's T1–T10 task breakdown (`Context Compression System.md`) uses phrases such as "replace cold messages with summary" and "compress older turns". A grep of the plan for `session_resumption`, `GetSession`, and `session.Messages` returns zero hits. The plan never says "mutate the transcript" and never says "don't mutate the transcript". T10 (L736-749) already operates on a transient `recentMessages` slice constructed inside `buildContextWindow()` — so the plan *accidentally* aligns with a view-only implementation, but this is not stated as a binding invariant.

### Why ambiguity is a problem

Without an explicit rule, the next engineer implementing compaction has three plausible paths:

1. **Destructive in-place** — rewrite `session.Messages` to drop cold turns and insert summary messages.
2. **Dual-store persistence** — add a second "compacted session" file on disk.
3. **View-only** — `session.Messages` is untouched; compaction produces a parallel slice used only when building the provider request.

Path (1) silently breaks the fidelity test, the TUI scrollback, and the correlation between `session.Messages` and `FileContextStore`. Path (2) introduces two load paths and a confusing question of "which is the real session".

## Problem

1. **No explicit invariant.** Nothing in the architecture states that the canonical transcript must be preserved across compaction.
2. **Fidelity test exposure.** `session_resumption_fidelity_integration_test.go` asserts exact state reconstruction and will break silently the first time any compaction step rewrites `session.Messages`.
3. **TUI scrollback exposure.** The TUI renders from `session.Messages`. Destructive compaction would delete user-visible history from the scrollback buffer without the user asking for it.
4. **Persistence coupling.** Compaction that mutates session state would entangle the compaction system with the (currently metadata-only) persistence format.
5. **Correlation store drift.** Tool-call ids live in `FileContextStore`. If `session.Messages` is rewritten but the recall store is not, the two desynchronise and cross-provider failover (the reason `cb33d19` exists) breaks.
6. **Audit and debugging.** Investigators rely on `session.Messages` being the ground-truth record.

## Decision: Context Compaction Is View-Only

The context compression system is a **view-only transformation** over the LLM request window. It does not mutate session state; it produces a parallel, ephemeral projection used only when assembling the request sent to the provider.

### Invariants

The **core three** (load-bearing — violations break existing tests and user-visible features):

1. **Compaction MUST NOT call `Manager.AppendMessage`.** Only the accumulator (streaming assistant output) and user-input handlers may append to the transcript.
2. **Compaction MUST NOT mutate `session.Messages` in place.** No index assignment, no `append` to the slice held by `Manager`, no replacement of entries.
3. **Compaction MUST NOT modify the `recall.FileContextStore` persistence format.** Tool-call id mappings and recall embeddings are not compaction's to edit.

Expansions of the core rule:

4. **The raw transcript returned by `Manager.GetSession(id).Messages` is canonical** for TUI scrollback rendering, session resumption fidelity, human audit, and any future export or replay feature.
5. **Compaction artefacts are parallel state.** Summary JSON, cold-message spillover files, and knowledge-store entries sit alongside the transcript.
6. **Scope of write.** Compaction is permitted to write only to `~/.flowstate/compacted/{session-id}/` and `~/.flowstate/session-memory/{session-id}/`, plus its own in-memory caches.
7. **Compaction MUST NOT write to `~/.flowstate/sessions/{id}.json`**, either directly or via `Manager`.
8. **Scope of read.** Compaction may read `session.Messages` via `Manager.GetSession`. It must treat the slice as immutable — **copy before transforming**.

### Architectural Flow

```mermaid
flowchart TB
    subgraph "Canonical State (unchanged by compaction)"
        A[session.Messages]
        B[recall.FileContextStore]
    end

    subgraph "Compaction Layer (view-only)"
        C[Read recent N messages]
        D[Summarise cold turns]
        E[Fetch knowledge entries]
        F[Assemble request window]
    end

    subgraph "Compaction Artefacts (parallel, never replace A/B)"
        G[compacted/{session-id}/]
        H[session-memory/{session-id}/]
    end

    subgraph "Outbound Request"
        I[Provider.Chat messages]
    end

    subgraph "Consumers of Canonical State"
        J[TUI scrollback]
        K[Session resumption]
        L[Audit / replay]
    end

    A --> C
    A --> J
    A --> K
    A --> L
    B --> E
    C --> F
    D --> F
    E --> F
    D --> G
    D --> H
    F --> I
```

Note the one-way arrows out of `session.Messages` and `FileContextStore`. Compaction reads; it never writes back.

### Applied to the Current Plan

The compression plan's T10 (L736-749) is already compatible: it operates on a transient `recentMessages` slice built inside `buildContextWindow()`. This ADR promotes that accidental alignment to a binding rule and extends it to every other task in the plan.

## Key Decisions

### 1. Canonical Transcript Is the In-Memory `session.Messages`

The authoritative transcript is the slice held by `Manager`. This is already the de facto truth (see `accumulator.go:36-38`); this ADR names it.

Session persistence being metadata-only (`persistence.go:14-20`) means "canonical" here refers to the live, in-memory representation. Future work that persists messages inherits the same invariant.

### 2. Compaction Output Is Ephemeral Per-Request

Each call to build the provider request produces a fresh compacted view. Callers of `Provider.Chat` see compaction output; nothing else does.

### 3. Caching Is a Permitted Extension

Recomputing the compacted view on every turn is acceptable for correctness but may be expensive. A compacted-view cache keyed on `(session-id, transcript-length, manifest-version)` is allowed, provided:

- Invalidation is deterministic — any append to `session.Messages` invalidates the cache for that session.
- The cache is not treated as canonical. On cache miss, recomputation from `session.Messages` must produce an equivalent result.
- The cache is never the sole copy of a summary that has also been written to `~/.flowstate/session-memory/` — the on-disk artefact is the durable projection.

### 4. Artefact Directories, Not a Unified Store

| Directory | Contents | Lifecycle |
|---|---|---|
| `~/.flowstate/compacted/{session-id}/` | Spilled cold messages, compaction run metadata | Session-scoped |
| `~/.flowstate/session-memory/{session-id}/` | Summaries, promoted-knowledge entries | Session-scoped, may outlive session |
| `~/.flowstate/sessions/{id}.json` | Session metadata only — **off-limits to compaction** | Managed by persistence layer |

### 5. Recall Store Remains Independent

`recall.FileContextStore` keeps the tool-call id translation table and embedding vectors. Compaction reads from it but never writes. The cross-provider failover fix (`cb33d19`) depends on this store being the single authority for id mapping.

### 6. No New Fields on `session.Message`

`session.Message` is not extended to carry "compacted" or "summarised" flags. Adding such fields would leak compaction concerns into canonical state.

## Consequences

### Positive

- **Fidelity test unchanged.** `session_resumption_fidelity_integration_test.go` continues to pass without modification.
- **TUI scrollback intact.** Users see the real conversation.
- **Clean separation of concerns.** Session management, recall, and compaction each own distinct data.
- **Safe provider-layer evolution.** Cross-provider failover, tool-call id translation, and retries continue to read from canonical state.
- **Auditability.** A human or agent inspecting a session always sees what was actually said.
- **Safe rollback.** Disabling compaction is a one-line change with no state migration.
- **Independent testability.** Compaction can be tested as a pure function over a transcript.

### Negative

- **Recomputation cost on the hot path.** Mitigated by the threshold trigger and the permitted cache (Key Decision 3).
- **Summariser latency visible per-turn.** Mitigated by caching summary artefacts to `~/.flowstate/session-memory/` and by running summarisation asynchronously where possible.
- **Storage duplication.** Summary artefacts and cold-message spillover live on disk in addition to the in-memory transcript.
- **Two mental models.** Engineers must hold both "the real transcript" and "the compacted view".

### Neutral

- Compaction becomes a pure projection. Standard treatment in systems that need replayable history.

## Alternatives Considered

### A. Destructive In-Place Compaction

`session.Messages` is rewritten on each compaction pass.

**Rejected.** Breaks the fidelity test immediately, deletes user-visible scrollback, desynchronises `session.Messages` and `FileContextStore`, entangles compaction with persistence evolution, destroys audit trail.

### B. Separate "Compacted Session" Persistence File

Compaction produces a second file (`~/.flowstate/sessions/{id}.compacted.json`).

**Rejected.** Two load paths, ownership ambiguity, doubled crash-recovery surface, no benefit over view-only with caching.

### C. Compaction Column on `session.Message`

Extend `session.Message` with `Compacted bool` and `Summary string` fields.

**Rejected.** Leaks compaction concerns into canonical state; consumers (TUI, audit) would need to know about compaction to filter correctly.

### D. View-Only with Per-Session Compacted View Cache — **Accepted as Extension**

Identical to the accepted decision with an added in-memory cache (Key Decision 3). Caching is orthogonal to the view-only invariant.

## Relationship to Other Documents

- [[ADR - Tool-Call Atomicity in Context Compaction]] — sibling invariant on compactable-unit granularity
- [[ADR - Recall Context Management]] — describes recall summaries as retrievable products; view-only compaction is consistent with and extends that philosophy
- [[FlowState Recall Architecture]] — treats context as external state queried by the model; this ADR sharpens the rule
- [[Context Compression System]] — binding constraint on T1–T10
- [[ADR - Agent Manifest Format]] — defines `context_management` settings that configure compaction

## Enforcement

1. **Unit test** asserting that for any `Manager` method exposed to compaction code, calling the compaction build function leaves `session.Messages` pointer-equal and element-equal to its prior state.
2. **Grep-level lint** in CI: compaction packages must not import `Manager.AppendMessage` or mutate `*session.Session` fields.
3. **The existing fidelity test** acts as a backstop — any destructive compaction implementation breaks it, loudly.
4. **Code review gate.** Any change under `internal/context/` (or wherever the compaction package lands) that touches `internal/session/` in a non-read-only way triggers mandatory Principal-Engineer review citing this ADR.

## Status

- **Proposed** — Awaiting review. Once accepted, implementation of the context compression plan (T1–T10) is bound by the invariants listed in **Decision**.

## Links

- Plan: [[Context Compression System]]
- Sibling: [[ADR - Tool-Call Atomicity in Context Compaction]]
- Fidelity test: `internal/session/session_resumption_fidelity_integration_test.go:38-43, 75-107, 159-182`
- Session persistence: `internal/session/persistence.go:14-20, 34-54`
- Stream accumulator: `internal/session/accumulator.go:36-38, 64-81`
- Session types: `internal/session/manager.go:31-39`
- Related ADR: [[ADR - Recall Context Management]]
- Related ADR: [[ADR - Agent Manifest Format]]
- Related architecture: [[FlowState Recall Architecture]]

---

*Snapshot note: the primary, editable copy of this ADR lives in the FlowState
Obsidian vault at `Documentation/Architecture/ADR/`. This file is a verbatim
snapshot checked into the repository so clone-only readers can see the
invariants that regression tests encode. Refresh in the same change-set as
any vault edit; see `docs/adr/README.md` for the rule.*
