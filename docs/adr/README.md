---
id: adr-index-go-cli
title: ADR Index — Go CLI
type: adr-index
status: current
adr_group: Go CLI
created: 2026-11-18
modified: 2026-11-19T23:40
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
  - flowstate/cli
  - flowstate/adr
  - timeline/november-2026
aliases:
  - Go CLI ADR Index
  - docs/adr Index
---

# ADR Index — Go CLI

The Go CLI's architecture decision records, in the vault template. The prototype-scale ADR corpus lives in the
  Obsidian vault (`Architecture/ADR/`); the mapping from vault ADRs to Go CLI implementation status is in [[ADR -
  Vault-to-Go-CLI Comparison]].

## Accepted

| # | ADR | Decision in one line |
|---|---|---|
| 001 | [[ADR - Owning-Layer Boundary: CLI Wires Application Only]] | cmd/flowstate wires internal/app exclusively; whole-file gate enforces it |
| 002 | [[ADR - Session Run Lifecycle]] | Five-status machine; ordered phases; fresh budget on resume; exit-code contract |
| 003 | [[ADR - Schema Validation of JSON Output]] | Hand-authored schemas/cli.json; e2e validation via santhosh-tekuri/jsonschema |
| 004 | [[ADR - Merge Trailer Policy]] | Single Project-Metadata trailer; (#N) subject; make merge-pr is the only merge path |
| 005 | [[ADR - Config-Driven Session Composition]] | flowstate.json defaults; flags override; SHA-256 composition pin; secret keys rejected pre-decode |
| 006 | [[ADR - Crash-Recovery Detection and Recovery]] | Run-activity gate + dangling-turn scan; idempotent session.interrupted append |
| 007 | [[ADR - Transport Adapter Packages]] | internal/server classified as transport adapter; cmd allowlist app+config+server; net/http subtree banned from cmd |

## Reading order

New contributors: 001 → 002 → 005 covers the layering, runtime, and configuration model. Reviewers: 004 covers the
  merge evidence chain. Operators: 006 covers crash semantics.

## Vault relationship

The vault corpus (78 ADRs — 37 accepted, 22 proposed, 19 superseded) documents the prototype architecture. This
  index's seven ADRs are the greenfield Go CLI's own decisions, written in the same template and cross-linked to their
  vault ancestors. Where a vault ADR is superseded (TUI-era patterns), it has no Go CLI descendant; where it is
  accepted and carried forward, the Go CLI ADR cites it under Cross-links.
