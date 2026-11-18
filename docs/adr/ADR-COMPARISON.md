# ADR Comparison: fswt Vault ADRs vs Go CLI

Maps the fswt prototype vault ADRs to the greenfield Go CLI implementation.

## Accepted ADRs (37) — carry forward

Provider Registry Schema, Streaming Architecture, Tool-Capability Allowlist Gate, BDD Declarative Steps, Testing Strategy, Z.AI Plan Routing, App Composition Root Boundary, Engine-Owned Workflow Mechanics, FlowState Engine Boundary, Engine Bus Event Taxonomy, Dual-Scope Gate Runner, FlowState-Owned GitHub OAuth App, Agent Manifest Format, Agent Discovery Pattern, Agent Runtime Lifecycle, Coordination Store Boundary, Delegation Chain Contract, Hierarchical Session Navigation (M2), Multi-Agent Orchestration (M2), Swarm Dispatch Across Access Methods (M2), Hook System (M2), MCP Tool Gating (not applicable), Multi-Access Method (not applicable), OpenCode Credential Integration (superseded by env-only auth), Recall Context Management (not applicable), Embedding Provider Separation (not applicable), Skills as XDG_CONFIG (not applicable), Skill Classification (not applicable), Session Orchestrator (partially implemented), Streaming Liveness (partially implemented), System Prompt Budget (partially implemented), Three-Layer Tracker (superseded by exec loop), TUI Big-Bang Decommissioning (implemented), BUSL 1.1 Licensing (implemented), Swarm Activity Event Model (M2).

## Proposed ADRs (20) — future decisions

Turn Model (implemented in internal/app/turn.go), Tool Execution Control Loop (implemented in internal/tool/loop.go), Streaming Event Infrastructure (partially covered), remaining: M2+ scope.

## Superseded ADRs (18) — correctly retired

All TUI-era patterns (Bubble Tea, Vue, UIKit, modals, screens, views, intents).
