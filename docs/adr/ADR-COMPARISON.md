---
id: adr-vault-to-go-cli-comparison
title: ADR - Vault-to-Go-CLI Comparison
type: adr
status: accepted
adr_group: Go CLI
created: 2026-11-18
modified: 2026-11-18T21:40
deciders:
  - Engineering Team
consulted:
  - Architecture Review Board
informed:
  - FlowState Contributors
tags:
  - type/adr
  - project/flowstate
  - topic/architecture
  - topic/adr
  - topic/mapping
  - flowstate/cli
  - flowstate/adr
  - timeline/november-2026
aliases:
  - Vault-to-Go-CLI Comparison
  - ADR Comparison Table
  - ADR-COMPARISON
---

# ADR - Vault-to-Go-CLI Comparison

## Status

**Accepted** — 2026-11-18. This is a mapping document, not a decision record in the strict sense: it ratifies *which*
  prototype ADRs carry forward, which are retired with the TUI, and which concerns the Go CLI ADRs cover that the
  vault never addressed.

## Context

FlowState has two ADR corpora:

1. **The Obsidian vault** (`Architecture/ADR/`, 78 files): the prototype architecture — Bubble Tea TUI era through the
  agentic harness daemon. Groups: Agent Platform, TUI Architecture, Testing, Governance, and ungrouped pattern ADRs.
2. **The repository** (`docs/adr/`): the greenfield Go CLI's own decisions, six ADRs to date ([[ADR - Owning-Layer
  Boundary: CLI Wires Application Only]], [[ADR - Session Run Lifecycle]], [[ADR - Schema Validation of JSON Output]],
  [[ADR - Merge Trailer Policy]], [[ADR - Config-Driven Session Composition]], [[ADR - Crash-Recovery Detection and
  Recovery]]).

The Go CLI is not a port of the prototype: it is a greenfield CLI sharing the product's concepts (sessions, providers,
  tools, budgets, events) but none of its TUI, plugin, or swarm surface. The corpora overlap in *principles*
  (composition root, allowlist gating, streaming, testing strategy) but almost never in *mechanism*.

## Decision

**Vault ADRs are classified into four dispositions: Implemented (carried forward), Partially implemented, M2+ scope
  (future), and Retired (TUI-era). The Go CLI's own decisions are enumerated separately — they are new ground, not
  ports.**

### Accepted vault ADRs (37) — disposition

| Vault ADR | Disposition | Go CLI anchor |
|---|---|---|
| ADR-003: OpenCode Auth Integration | Superseded for Go CLI | env-only auth (`ZAI_API_KEY` via `os.Getenv`) |
| ADR-004: BUSL 1.1 Licensing | Implemented | repo LICENSE |
| Agent Discovery Pattern | M2+ scope | agent manifests are M2 ([[ADR - Agent Discovery Pattern]]) |
| Agent Manifest Format | M2+ scope | — |
| Agent Runtime Lifecycle | M2+ scope | — |
| App Composition Root Boundary | Implemented (principle) | `internal/app` is the composition root; see [[ADR - Owning-Layer Boundary: CLI Wires Application Only]] |
| BDD Declarative Steps | Implemented | Ginkgo v2/Gomega contract; schemas as executable spec |
| Coordination Store Boundary | M2+ scope | coordination store is fswt harness surface |
| Delegation Chain Contract | M2+ scope | delegation is M2 |
| Dual-Scope Gate Runner | Implemented (pattern) | `check_code_contract` + `check_test_contract` |
| Engine Bus Event Taxonomy | Implemented (vocabulary) | event types in `internal/app/errors.go` + store writer |
| Engine-Owned Workflow Mechanics | Implemented | `internal/app` owns the run loop |
| FlowState Engine Boundary | Implemented (principle) | provider port/stream seam in `internal/provider` + `internal/app/turn.go` |
| FlowState-Owned GitHub OAuth App | Implemented | admission runner uses app 5728089; see [[ADR - Merge Trailer Policy]] |
| Hierarchical Session Navigation | M2 scope | child sessions ([[ADR - Hierarchical Session Navigation]]) |
| Hook System | M2+ scope | no hooks in the Go CLI yet |
| MCP Tool Gating by Agent Manifest | Not applicable | no MCP surface in M1/M2 CLI |
| Multi-Agent Orchestration | M2+ scope | swarm is fswt harness |
| Multi-Agent Recall Context Sharing | Not applicable | no recall in the CLI |
| Provider Registry Schema | Implemented | `internal/provider/registry` |
| Recall Context Management | Not applicable | no recall in the CLI |
| Session Orchestrator for Surface Parity | Partially implemented | exec loop + resume; no orchestrator service |
| Skill Classification and Always-Active Skills | Not applicable | no skills surface in the CLI |
| Skills as XDG_CONFIG with Embedded Seeding | Not applicable | — |
| Streaming Architecture | Implemented | SSE provider stream into the turn loop |
| Streaming Liveness and Heartbeat | Partially implemented | streaming works; no heartbeat events |
| Swarm Dispatch Across Access Methods | Not applicable | swarm is fswt harness |
| System Prompt Budget Resolution | Partially implemented | `--max-tokens`; no system-prompt budget surface |
| Testing Strategy | Implemented | Ginkgo v2/Gomega, e2e matrix, `-race`; see [[ADR - Schema Validation of JSON Output]] for the schema layer |
| Three-Layer Tracker Architecture Refactor | Superseded | Go CLI uses the simpler exec loop |
| Tool-Capability Allowlist Gate | Implemented | `internal/tool` deny-by-default gate |
| TUI Big-Bang Decommissioning (May 2026) | Implemented (historical) | CLI/headless only; no TUI exists |
| Z.AI Plan Routing as First-Class Field | Implemented | `ZAI_PLAN` environment variable |
| (3 unlisted accepted ADRs) | M2+ scope | agent/coordination/skills concerns with no CLI equivalent yet |

### Proposed vault ADRs (22) — disposition

Not implemented in either codebase; they remain planning references. The closest Go CLI anchors: [[ADR - Agent Turn
  Lifecycle Architecture]] → lifecycle events in [[ADR - Session Run Lifecycle]]; Streaming Event Infrastructure → the
  envelope in [[ADR - Schema Validation of JSON Output]]; Tool Execution Control Loop → `internal/tool/loop.go`; Turn
  Model → `internal/app/turn.go`. The remaining eighteen (compression, failover health, dispatcher, plugin dispatch,
  recall/vector store, skills resolution, observability, rate-limit visibility, context window, deterministic workflow
  harness, event-bus delivery, stream pipeline, todo store, tool-call atomicity, unified rate limits, view-only
  compaction, skills runtime, engine compression) are M2+ and unanchored.

### Superseded vault ADRs (19) — disposition

All cover TUI-era patterns (Bubble Tea, Vue, UIKit, intents, screens, views, modals, layer dependency rules for the
  TUI). Correctly retired; no Go CLI descendant. [[ADR - Layer Dependency Rules]] is the notable one whose *principle*
  survives (inward dependency hierarchy) even though its mechanism (TUI layer table) does not — the Go CLI's
  descendant is [[ADR - Owning-Layer Boundary: CLI Wires Application Only]].

### Go CLI decisions without vault ancestors

| Go CLI ADR | Why it is new ground |
|---|---|
| [[ADR - Owning-Layer Boundary: CLI Wires Application Only]] | The prototype's cmd/ boundary was stated but never whole-file-gate-enforced |
| [[ADR - Session Run Lifecycle]] | The prototype had orchestrators; the CLI's five-status machine is its own design |
| [[ADR - Schema Validation of JSON Output]] | The prototype had no schema-validated wire output |
| [[ADR - Merge Trailer Policy]] | Admission evidence chain is a Go-CLI-era governance invention |
| [[ADR - Config-Driven Session Composition]] | The prototype composed at construction time; the CLI composes per invocation |
| [[ADR - Crash-Recovery Detection and Recovery]] | The prototype's daemon never died the way a CLI process does |

## Consequences

### Positive

1. **Corpus roles are explicit.** The vault is the prototype's history and the M2+ idea backlog; `docs/adr/` is the Go
  CLI's operative record. No one asks "which ADR is authoritative?" for a CLI question.
2. **Carry-forward principles have named anchors.** Each Implemented row cites its Go CLI anchor, so "does the CLI do
  X?" resolves to a file, not an argument.
3. **M2 planning has a source list.** The Proposed table is the intake queue; new M2 work starts from it rather than
  rediscovering ideas.

### Negative

1. **The mapping rots silently.** Dispositions are statements of fact at 2026-11-18; a merged M2 feature can
  invalidate rows without anyone editing this file. Mitigation: the Go CLI ADRs cite their vault ancestors, so new
  ADRs refresh the relevant rows as a side effect of cross-linking.
2. **Two templates, one format.** The corpus shares a format but not an index; finding "all ADRs about streaming"
  spans both corpora. The index ([[ADR Index — Go CLI]]) covers only the repo side.

### Neutral

1. Vault counts (78 files; 37/22/19, frontmatter-verified) are as-of writing; the vault is living documentation and
  will drift.
2. Dispositions use four labels, not three, because "partially implemented" is genuinely distinct from both poles
  (streaming works, heartbeats do not).

## Alternatives considered

### A. Port the vault ADRs into the repo verbatim

**Rejected.** Most vault ADRs describe mechanisms the Go CLI does not have (TUI, plugins, MCP, swarm, recall). Porting
  them would seed `docs/adr/` with operative-looking records for nonexistent surface, which is worse than absence.

### B. Keep the corpora fully separate (no mapping)

**Rejected.** The principles genuinely carry forward — composition root, allowlist gate, streaming, testing strategy —
  and reviewers need the mapping to know which vault statements are normative for CLI work versus historical.

### C. Merge the corpora into one numbered sequence

**Rejected.** The vault is Obsidian-native (wikilinks, frontmatter conventions, group taxonomy) and owner-curated; the
  repo corpus is version-controlled with the code it governs. Merging would either break the vault's workflow or fork
  it into the repo. Cross-linking gets the navigability without the migration.

### D. Status-sync automation (CI checks vault dispositions against repo reality)

**Noted, not decided.** Desirable in principle; infeasible until dispositions are machine-readable (structured
  frontmatter in the repo pointing at vault IDs). Deferred.

## Cross-links

### The Go CLI corpus

- [[ADR Index — Go CLI]] — the operative index this mapping complements.
- All six Go CLI ADRs, per the tables above.

### Vault anchors cited

- [[ADR - App Composition Root Boundary]], [[ADR - CLI TUI Business Logic Separation]], [[ADR - Layer Dependency
  Rules]], [[ADR - Agent Turn Lifecycle Architecture]], [[ADR - Streaming Architecture]], [[ADR - Testing Strategy]],
  [[ADR - BDD Declarative Steps]], [[ADR - FlowState-Owned GitHub OAuth App]], [[ADR - Hierarchical Session
  Navigation]], [[ADR - Z.AI Plan Routing as First-Class Field]], [[ADR - Dual-Scope Gate Runner]], [[ADR - Agent
  Discovery Pattern]].

## Sources

- Obsidian vault `Architecture/ADR/` — 78 files; disposition table compiled from status frontmatter
  (accepted/proposed/superseded/absent) at 2026-11-18.
- `docs/adr/` — the six Go CLI ADRs and this index.
- `internal/provider/zai/zai.go:93`, `internal/provider/registry/registry_compat.go` (`zaiPlan`) — `ZAI_PLAN` routing anchor.
- `internal/tool/exec.go:69-71`, `internal/tool/loop.go` — allowlist gate and control loop anchors.
- `go.mod:9-11`, `Makefile:78-109` — testing-strategy anchors (Ginkgo/Gomega versions, race + e2e matrix + check
  aggregation).

## Related Plans

- *Ongoing:* M2 features draw from the Proposed table; each new Go CLI ADR refreshes the affected rows here.
- *Deferred:* machine-readable disposition sync (Alternative D).
