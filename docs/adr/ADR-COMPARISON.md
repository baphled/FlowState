# ADR Comparison: fswt Vault ADRs vs Go CLI

Maps the fswt prototype vault ADRs to the greenfield Go CLI implementation.

The vault contains 78 ADR files: 37 accepted, 20 proposed, 18 superseded, and 3 without a status field. Each is classified below by its relevance to the Go CLI.

## Accepted ADRs (37) — carry forward to Go CLI

| Vault ADR | Go CLI Status | Notes |
|---|---|---|
| ADR-001: Multi-Access Method Architecture | Not applicable | CLI-only in M1 |
| ADR-003: OpenCode Credential Integration | Superseded | Go CLI uses env-only auth (ZAI_API_KEY) |
| ADR-004: BUSL 1.1 Licensing | Implemented | Repo LICENSE matches |
| App Composition Root Boundary | Implemented | `internal/app` is the composition root |
| BDD Declarative Steps | Implemented | Ginkgo v2/Gomega contract enforced |
| Dual-Scope Gate Runner | Implemented | `check_code_contract` + `check_test_contract` |
| Engine Bus Event Taxonomy | Implemented | Event types in `internal/app/errors.go` |
| Engine-Owned Workflow Mechanics | Implemented | `internal/app` owns the workflow |
| FlowState Engine Boundary | Implemented | `internal/app` is the boundary |
| FlowState-Owned GitHub OAuth App | Implemented | Admission runner uses app 5728089 |
| Provider Registry Schema | Implemented | `internal/provider/registry` |
| Session Orchestrator for Surface Parity | Partially implemented | Exec loop + resume; no orchestrator |
| Streaming Architecture | Implemented | SSE provider stream → event store |
| Streaming Liveness and Heartbeat | Partially implemented | Provider streaming works; no heartbeat |
| Testing Strategy | Implemented | Ginkgo v2/Gomega, e2e matrix, race |
| Tool-Capability Allowlist Gate | Implemented | `internal/tool` allowlist gate |
| TUI Big-Bang Decommissioning (May 2026) | Implemented | CLI/headless only in M1 |
| Z.AI Plan Routing as First-Class Field | Implemented | `ZAI_PLAN` env + provider config |

(17 accepted ADRs not listed are M2-scope agent/coordination/skills/swarm concerns that have no Go CLI equivalent yet.)

## Proposed ADRs (20) — future decisions

Not yet implemented in either codebase; remain in the vault as planning references. The closest Go CLI anchors are: Agent Turn Lifecycle Architecture → ADR-002, Streaming Event Infrastructure → ADR-002, Tool Execution Control Loop → `internal/tool/loop.go`, Turn Model → `internal/app/turn.go`. The remaining 16 are M2+ scope (compression, failover, dispatcher, plugin dispatch, recall/vector store, skills, observability, rate-limit visibility).

## Superseded ADRs (18) — correctly retired

All superseded ADRs cover TUI-era patterns (Bubble Tea, Vue, UIKit, modals, screens, views, intents). They are correctly retired and have no bearing on the Go CLI.

## Go CLI Decisions Without Vault ADRs

| Go CLI ADR | Decision |
|---|---|
| ADR-001 | Owning-layer boundary (cmd wires app only) |
| ADR-002 | Session run lifecycle (turn loop, resume, crash recovery) |
| ADR-003 | Schema validation of --json output |
| ADR-004 | Merge trailer policy (single Project-Metadata, #N suffix) |
| ADR-005 | Config-driven session composition (flowstate.json) |
| ADR-006 | Crash-recovery detection and recovery |
