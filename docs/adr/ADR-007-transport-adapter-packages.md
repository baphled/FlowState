---
id: adr-transport-adapter-packages
title: ADR - Transport Adapter Packages
type: adr
status: accepted
adr_group: Go CLI
created: 2026-11-19
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
  - topic/layering
  - topic/transport
  - flowstate/cli
  - flowstate/server
  - flowstate/adr
  - timeline/november-2026
aliases:
  - Transport Adapter Packages
  - internal/server Adapter Classification
---

# ADR - Transport Adapter Packages

## Status

**Accepted** — 2026-11-19. Amends [[ADR - Owning-Layer Boundary: CLI Wires Application Only]], Boundary invariant 1
  ("cmd imports only `internal/app`"). Two divergences from that invariant's letter are recorded here: `internal/config`
  was already cmd-importable in the enforced checker when this ADR was written (a pre-existing, previously unrecorded
  divergence), and `internal/server` becomes cmd-importable by this amendment. Both are now recorded facts of the
  contract rather than silent checker behavior.

## Context

ADR-001 fixed the CLI's import direction: `cmd/flowstate` wires through `internal/app` exclusively, enforced by
  `scripts/check_code_contract.py` on every changed production Go file. The milestone's next surface is an HTTP `serve`
  command. HTTP serving is a transport concern: it needs `net/http`, handlers, and eventually middleware — machinery
  that has no place in cmd (whose job is input parsing, output and stable exits) and must never leak into the domain
  (`internal/runtime`, `internal/session`, `internal/tool` already ban `net/http` outright).

ADR-001's invariant 1 as written admits one project package into cmd: `internal/app`. The enforced checker's allowlist,
  however, has admitted `internal/config` since that package's introduction — a divergence between the recorded
  decision and the enforced rule that was never written down. This amendment could not widen the allowlist honestly
  without recording that fact.

No `internal/server` package exists yet. This ADR pre-ratifies its classification so the serve surface lands inside a
  contract that already describes it, instead of retrofitting one after the first violation.

## Problem

1. **The serve surface has no legal home.** cmd cannot grow `net/http` without becoming a transport implementation
   itself; the domain must not grow it at all; `internal/app` is a composition root, not a socket listener.
2. **The allowlist already diverges from the record.** `internal/config` is cmd-importable in the checker but absent
   from invariant 1; any further widening would compound an unrecorded divergence.
3. **Post-hoc classification is expensive.** ADR-001 demonstrated that dormant violations surface at inconvenient
   times, when a packet inherits a layering fix as a prerequisite.

## Decision

**`internal/server` is classified as a transport adapter package, and cmd's project-import allowlist becomes
  `internal/app`, `internal/config` and `internal/server`.** The owning-layer checker
  (`scripts/code-contract/imports.go`) gains exactly three rules:

1. **Adapter classification.** `internal/server` joins `internal/store`, `internal/storage`, `internal/oauth` and
   `internal/provider/*` as an adapter: it may not import `internal/app`, `internal/runtime` or `cmd`, and may use the
   standard library including `net/http`. Its owner registration in `scripts/code-contract/layout.go` lands in the same
   change, so the area is classified before its first file exists.
2. **cmd may import it.** The cmd project-import allowlist adds `internal/server` — the socket is wired from cmd like
   any other adapter handle, while handlers stay inside the adapter.
3. **cmd may not serve HTTP itself.** The whole `net/http` subtree (`net/http`, `net/http/httptest`, `net/http/pprof`,
   …) is banned from cmd production files; cmd's HTTP surface is exclusively `internal/server`. Test files are excluded
   by the driver, so `httptest` in `cmd/**/_test.go` remains legal.

Scoped explicitly **out** of this amendment: the domain `net/http`/`database/sql` ban is unchanged; the
  unknown-package fall-through (packages that are neither domain, swarm nor adapter keep unrestricted project imports)
  is retained, and `internal/app` importing `internal/session` stays allowed through it; and any TUI surface is
  deferred — a future terminal-adapter area needs its own recorded classification, not a reuse of this one.

## Enforcement

`make check-code-contract BASE_SHA=<PR base>` applies the amended rules to every changed committed production Go file,
  including build-tagged variants. Fail-closed behavior, the inline-waiver ban and the SQL/ORM heuristic are unchanged
  from ADR-001. Six Ginkgo table entries pin the new matrix at the `allowedImport` unit; seven synthetic-repo oracle
  tests pin the committed pipeline (layout registration included) end to end.

## Consequences

### Positive

1. **The serve surface has a bounded home before it exists.** The first `internal/server` file lands inside a
   pre-ratified contract; no packet inherits a classification prerequisite.
2. **HTTP cannot accrete into cmd.** The subtree ban makes "just one handler in main" a gate failure rather than a
   review argument.
3. **The record matches the checker.** The `internal/config` divergence is now written down where the next widening
   decision will be read.

### Negative

1. **Widen-then-tighten is a one-way door.** Allowing cmd→`internal/server` cannot be cheaply reversed once the serve
   command exists; tightening later means touching every consuming file (changed-files-only enforcement exempts
   untouched files, so a future tightening would surface file by file, not all at once).
2. **`internal/server` can become a kitchen sink.** An adapter that may import everything except app/runtime/cmd and
   the domain's transport ban can still accrete unrelated transport concerns; the import ban mitigates but does not
   prevent this. Cohesion remains a review responsibility.
3. **The fall-through is a residual hole.** Any future internal package that is neither domain, swarm nor adapter gets
   unrestricted project imports until classified; this amendment deliberately keeps that behavior rather than
   expanding scope.

### Neutral

1. Enforcement stays changed-files-only: dormant violations in untouched files remain refactoring debt, per ADR-001.
2. No runtime code changes; the amendment is contract, tests and record only.

## Alternatives considered

### A. Let cmd import net/http directly and keep handlers in cmd

**Rejected.** It contradicts ADR-001's separation (cmd parses input and renders output; it does not implement
  transports) and once merged cannot be tightened cheaply.

### B. Serve from internal/app

**Rejected.** The composition root would own sockets, handlers and middleware — a second, larger boundary erosion.

### C. Ratify the classification after the serve surface exists

**Rejected.** ADR-001's own history shows post-hoc classification surfaces as gate failures inside unrelated packets.

### D. Close the unknown-package fall-through in the same change

**Rejected for scope.** Defensible, but it multiplies this change's blast radius across every unclassified package and
  belongs to its own recorded decision.

## Cross-links

- [[ADR - Owning-Layer Boundary: CLI Wires Application Only]] — the amended decision; invariant 1's allowlist is now
  app, config and server, recorded here.
- [[ADR - Session Run Lifecycle]] — the bounded-run semantics a future serve surface exposes without owning them.
- Dropbox `rules/ARCHITECTURE.md` — the ownership row this amendment adds for `internal/server`.

## Sources

- `scripts/code-contract/imports.go` — cmd allowlist, adapter classification, `net/http` subtree ban.
- `scripts/code-contract/layout.go` — `internal/server/` owner registration.
- `scripts/code-contract/layout_test.go` — six new layer-dependency entries plus the owner-registration entry.
- `scripts/tests/test_check_code_contract.py` — six new synthetic-repo oracle tests (plus the domain control).
- `docs/adr/README.md`, `rules/ARCHITECTURE.md`, `docs/architecture/OVERVIEW.md` — index and ownership records.

## Amendment — 2026-11-22: `internal/config` removed

`internal/config` was deleted in #391 (the unused provider file loader). Provider settings now come from the
environment only, so cmd has no configuration package to import, and the references above describe the decision as
it stood on 2026-11-19. The decision itself is unchanged: cmd may import `internal/app` and `internal/server`. The
checker's allowlist entry for `internal/config` was removed in #410.

## Amendment — 2026-11-22: `internal/storage` removed

`internal/storage` was deleted in #415; `internal/store` is the only persistence adapter package. The classification
above names it as it stood on 2026-11-19. The decision is unchanged. The checker no longer registers
`internal/storage`, so a new package of that name is an unregistered owner and is rejected until it is classified.

## Related Plans

- *Ratified ahead of implementation:* the serve surface packet will create `internal/server` inside this
  classification and extend it with its own behavior contracts.
- *Deferred:* terminal/TUI adapter classification; fall-through closure.
