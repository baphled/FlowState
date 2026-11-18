---
id: adr-owning-layer-cli-wires-application-only
title: ADR - Owning-Layer Boundary: CLI Wires Application Only
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
  - topic/composition-root
  - topic/layering
  - flowstate/app
  - flowstate/cli
  - flowstate/adr
  - timeline/november-2026
aliases:
  - Owning-Layer Boundary
  - CLI Wires Application Only
  - sessioncmds Owning-Layer Correction
---

# ADR - Owning-Layer Boundary: CLI Wires Application Only

## Status

**Accepted** — 2026-11-18. Enforced by `scripts/check_code_contract.py` on every changed production Go file. Surfaced
  as a gate failure during the config-composition packet and corrected in the owning-layer prerequisite PR (#290).
  This ADR records the Go CLI's instantiation of the owning-layer principle; the prototype-scale statement of the same
  principle lives in [[ADR - App Composition Root Boundary]] and [[ADR - CLI TUI Business Logic Separation]].

## Context

The greenfield Go CLI (`cmd/flowstate`, `internal/...` on `next`) follows a three-layer architecture with strict
  import direction:

```text
cmd/flowstate          (input parsing, output, stable exits)
    ↓ imports only
internal/app           (composition root and use cases)
    ↓ wires ports to adapters
internal/session       (session identity and state invariants)
internal/store         (SQLite persistence adapters)
internal/tool          (tool port and permission-gated loop)
internal/provider      (provider port; zai adapter subpackage)
```

The owning-layer contract in the Dropbox `rules/ARCHITECTURE.md` states that `cmd/flowstate` is responsible for "input
  parsing, output and stable exits" with "no direct engine, provider or store wiring". `internal/app` is the
  composition root: it wires domain ports to concrete adapters explicitly, so the CLI delegates all domain work
  upward. This mirrors, at greenfield scale, the boundary ratified for the prototype in [[ADR - App Composition Root
  Boundary]] — there, `internal/app` was the composition root assembling providers, tools, plugins and MCP clients;
  here, `internal/app` is the composition root assembling the session store, provider registry, and tool registry for
  a single-binary CLI.

### The violation

Before the correction, `cmd/flowstate/sessioncmds.go` imported both `internal/session` and `internal/store` directly,
  bypassing the app layer. This happened organically: the session commands were written before the owning-layer rule
  was formalised, and because `sessioncmds.go` was not modified between the rule's introduction and the
  config-composition packet, the whole-file gate never surfaced the violation.

The violation had three concrete effects:

1. **Composition hash dead.** `session.Create` was called with `""` as the `configSHA` parameter — the M1-COMPOSITION
  capability (deterministic wiring pin) existed in the schema but was never populated. See [[ADR - Config-Driven
  Session Composition]].
2. **Provider/model resolution had no config fallback.** Every `session new` required explicit `--provider` and
  `--model` flags because the resolution logic that could consult defaults lived behind the app layer the CLI was not
  using.
3. **Store type leaked into cmd signatures.** `*store.Store` appeared directly in CLI-layer function signatures,
  coupling the binary's command surface to the persistence adapter's concrete type.

### Why the gate did not catch it earlier

`scripts/check_code_contract.py` uses whole-file checking: if a file is in the PR's changed-file set, the *entire
  file* must satisfy the import contract. Pre-existing violations only surface when a packet modifies the offending
  file. No packet touched `sessioncmds.go` between the contract's introduction and the config-composition packet. This
  is a deliberate design choice — whole-file checking is simpler than incremental ownership tracking and cannot be
  gamed by splitting a violation across untouched lines — but it means dormant violations surface only when the owning
  file is next touched.

### The discovery

The owning-layer correction was not planned. It surfaced as a gate failure while implementing the config-composition
  packet: modifying `sessioncmds.go` for config resolution caused the whole-file gate to reject the file's
  pre-existing `internal/session` and `internal/store` imports. Following the packet-discipline rule that refactoring
  is a separate prerequisite, not padding, the correction was split into its own PR (#290) so the config-composition
  feature (#292) could build on a clean layering base with a reviewable, single-concern diff.

## Problem

Without an explicitly enforced owning layer at CLI scale:

1. **Composition logic fragments.** When the CLI wires the store and session domain directly, there is no single place
  where wiring decisions (storage path resolution, provider fallback, budget defaults) live. Each new capability
  forces a choice between duplicating wiring in cmd or pushing it down — and the violating path wins by default.
2. **Capabilities rot silently.** The `configSHA` parameter sat at `""` through multiple releases because no caller
  exercised it. Dead plumbing behind a layer boundary is invisible to both review and tests.
3. **The store's API surface becomes CLI API.** Every store method reachable from cmd is effectively public interface,
  constraining store refactors.
4. **Test impact is diffuse.** CLI-layer wiring tests must construct real store instances rather than the app-level
  fakes the app layer already exports.

## Decision

**`cmd/flowstate` wires through `internal/app` exclusively. CLI session commands call four app-layer wrappers —
  `CreateSession`, `ListSessions`, `InspectSession`, `CloseSession` — and never import `internal/session` or
  `internal/store` directly.**

### The wrapper surface

All four wrappers live in `internal/app/sessioncmd.go`:

| Wrapper | Delegates to | Notes |
|---|---|---|
| `CreateSession` | `session.Create` + `store.CreateSession` | computes composition hash first; persists it |
| `ListSessions` | `store.ListSessions` + event projection | returns wire-ready records |
| `InspectSession` | `store.GetSession` + event log read | replay path for `session events`/`session replay` |
| `CloseSession` | store status transition | enforces terminal-status invariants |

### The type-alias seam

The store type is aliased in app (`type Store = store.Store` in `internal/app/turn_types.go`), so the CLI's
  `*app.Store` parameter resolves to the same underlying type without importing the store package. This keeps the
  CLI's signatures honest (it really is handling a store handle for DB-path plumbing) while preserving the
  import-direction invariant. The alias is the deliberate exception that proves the rule: values flow through app's
  named type, types do not leak.

### Enforcement

`scripts/check_code_contract.py` runs on every changed production Go file and rejects any file whose import set
  violates the layer rules. A violation in any file in the PR's changed-file set blocks the merge via the Lint &
  Format check. The gate is fail-closed: an unreachable checker fails the check rather than passing it.

### Boundary invariants

1. **cmd imports only `internal/app` (plus stdlib and the CLI framework).** No exceptions for "just this one call".
2. **App is the only composition point.** Provider/model/budget/allowlist resolution happens in app functions, not cmd
  flag handlers.
3. **Whole-file checking.** Any touched file must be fully conformant; violations cannot hide in untouched regions of
  modified files.
4. **The alias seam is one-way.** `app.Store` may alias a store type; store may never alias or import app.

## Consequences

### Positive

1. **The CLI surface is stable.** Adding internal capabilities does not change the CLI's import set; the CLI's
  compile-time coupling is one package.
2. **Capabilities come alive at the right layer.** Wiring the composition hash through `CreateSession` made
  M1-COMPOSITION real in one PR instead of a cross-layer hunt.
3. **The whole-file gate catches accretion.** Any future violation surfaces the moment its file is touched —
  demonstrated empirically by this very correction.
4. **Store refactors are invisible to cmd.** The store's API can evolve behind the four wrappers plus the alias.

### Negative

1. **One-function-per-operation wrappers add boilerplate.** Four wrappers exist where direct calls would be two lines
  each. The indirection is the price of the boundary.
2. **App's public surface grows with each CLI capability.** The app package exports one symbol per operation;
  namespace discipline is required to keep it navigable.
3. **Cross-layer debugging traverses the wrapper.** A stack from a CLI failure passes through app before reaching the
  domain; mitigated by wrappers being pure delegation.
4. **Dormant violations surface at inconvenient times.** Whole-file checking means an unrelated packet can inherit a
  layering fix as a prerequisite, as happened here. The packet-splitting rule absorbs this, but it adds scheduling
  friction.

### Neutral

1. `sessioncmds.go` lost no functionality — the wrappers expose exactly what the direct calls exposed, plus the
  composition hash.
2. The alias adds one line to app's types file and no runtime cost (type aliases are compile-time).
3. Nothing about the store or session packages changed in the correction; they were always layer-clean.

## Alternatives considered

### A. Allow cmd to import session and store directly

**Rejected.** It would eliminate the wrapper boilerplate but reintroduce all four effects in the Problem section. The
  documented owning-layer contract exists precisely to prevent this coupling; waiving it for convenience would make
  the contract advisory.

### B. Move `cmd/flowstate` into `internal/`

**Rejected.** It would make the CLI a non-public binary and require module restructuring. The owning layer is about
  import direction, not about public/private placement; moving the directory solves neither.

### C. Define a cmd-local interface instead of app wrappers

**Rejected.** An interface in cmd abstracting session operations would still require cmd to import the session types
  for method signatures — violating the spirit of the rule while adding an abstraction layer. The app wrapper achieves
  the same decoupling with less machinery.

### D. Incremental (per-line) contract checking instead of whole-file

**Rejected.** Incremental checking would let dormant violations persist indefinitely — exactly the failure mode this
  correction exposed. Whole-file checking is the enforcement choice that made the dormant violation surface at all.

### E. Enforce with `depguard` lint rules instead of the project checker

**Rejected for now.** A lint rule would work, but the project's checker is already wired into Lint & Format, runs on
  the exact PR diff, and produces the failure message the team knows. Adding a parallel lint dependency for the same
  guarantee is duplication. Revisit if the checker's regex approach proves insufficient for deeper rules.

## Cross-links

### Sibling ADRs (Go CLI group)

- [[ADR - Config-Driven Session Composition]] — the capability that surfaced this violation; the composition hash it
  introduced flows through the `CreateSession` wrapper.
- [[ADR - Session Run Lifecycle]] — run-entry invariants (terminal rejection, recovery) live at the app layer for the
  same reason.
- [[ADR - Schema Validation of JSON Output]] — the schema-validation dependency choice was shaped by this boundary
  (reflection-based generation imported an external library into cmd and was rejected).

### Prototype ADRs carrying the same principle

- [[ADR - App Composition Root Boundary]] — the prototype-scale statement: `internal/app` is the composition root;
  registration responsibilities decompose into focused sub-packages the root assembles.
- [[ADR - CLI TUI Business Logic Separation]] — package-graph rules and entry-point thinness; the Go CLI's cmd-layer
  contract is its greenfield descendant.
- [[ADR - Layer Dependency Rules]] — the original (TUI-era, superseded) layering statement; its inward-dependency
  hierarchy survives in the Go CLI's import rules.

## Sources

- `cmd/flowstate/sessioncmds.go` at `0aeb26aa^` — pre-correction imports (`internal/session`, `internal/store`) and
  `session.Create(..., "", task)` call.
- `cmd/flowstate/sessioncmds.go` at `0aeb26aa` (PR #290) — post-correction: sole project import is `internal/app`.
- `internal/app/sessioncmd.go:16-35` — the four wrappers.
- `internal/app/turn_types.go:60` — `type Store = store.Store` alias.
- `scripts/check_code_contract.py` — whole-file import-contract checker wired into Lint & Format.
- Dropbox `rules/ARCHITECTURE.md` — owning-layer table (source of the contract).
- PR #290 (correction), PR #292 (config composition; the packet that surfaced the violation).

## Related Plans

- *Completed:* owning-layer prerequisite packet (PR #290) — one PR, one concern, gate-clean base for config
  composition.
- *Ongoing:* any packet touching `cmd/flowstate/*.go` inherits the whole-file gate; the boundary table above is the
  reference for "where does this go" questions.
