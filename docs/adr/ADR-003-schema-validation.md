---
id: adr-schema-validation-of-json-output
title: ADR - Schema Validation of JSON Output
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
  - topic/testing
  - topic/cli
  - topic/schemas
  - flowstate/cli
  - flowstate/adr
  - timeline/november-2026
aliases:
  - Schema Validation of JSON Output
  - CLI JSON Schemas
  - cli.json Contract Tests
---

# ADR - Schema Validation of JSON Output

## Status

**Accepted** — 2026-11-18. Landed as PR #295. Completes the last unchecked item in the Dropbox
  `CLI-AND-API-CONTRACT.md` §5 done checklist. The contract-testing philosophy descends from [[ADR - BDD Declarative
  Steps]] and the testing strategy in [[ADR - Testing Strategy]]; the owning-layer constraint that shaped the design
  is recorded in [[ADR - Owning-Layer Boundary: CLI Wires Application Only]].

## Context

### The contract requirement

`CLI-AND-API-CONTRACT.md` §5 states: "--json output of every command validates against generated schemas." Before PR
  #295, no schema validation existed. The `--json` output shapes were defined only by the Go code that produced them —
  `sessionJSON` in `cmd/flowstate/sessioncmds.go` and the `writeJSON` event envelope in `cmd/flowstate/sessionrun.go`
  — with no independent verification that emitted JSON matched the expected structure.

### The risk this closes

Without schema validation, a refactoring could silently rename a field, drop a required field, or change a value type
  — and no test would fail. The output would remain *valid JSON* while becoming semantically wrong for consumers. The
  exposure is highest on the two machine-parseable interfaces every downstream consumer touches:

- the **event envelope** (seven pinned fields including the payload SHA-256 that replay integrity depends on), and
- the **session record** (eight fields, the unit of `session list`/`session inspect` output).

### Why now

The done checklist had one unchecked item left. Schema validation was deferred during earlier packets because it
  looked like it needed a generator, and the generator attempt failed on layering grounds (see Alternatives A). The
  hand-authored fallback unblocked it in a single packet.

## Problem

1. **Silent shape drift.** Field renames and type changes compile and pass behaviour tests because the tests exercise
  Go structs, not the JSON bytes.
2. **No executable contract.** The contract document says outputs validate against schemas, but no schemas existed —
  the sentence was aspirational.
3. **Consumer trust.** Tooling that parses `--json` output (the receipt validator, external automation) has no way to
  pin the shapes it depends on.
4. **Replay integrity coupling.** The payload SHA-256 field is load-bearing for replay verification; its accidental
  removal would break replay silently.

## Decision

**Hand-author JSON Schema documents in `schemas/cli.json` (one combined file, one key per command output shape) and
  validate actual command outputs against them in the e2e suite using `github.com/santhosh-tekuri/jsonschema/v6` as a
  test-only dependency.**

### Schema inventory

| Schema key | Shape | Required fields |
|---|---|---|
| `session-new` | object | `id`, `status` |
| `session-cancel` | object | `id`, `status` |
| `session-list` | array of session records | eight fields each |
| `session-events` | array of event envelopes | seven fields each |
| `session-replay` | array of event envelopes | seven fields each |
| `receipt-validate` | array of verdict records | `path`, `verdict` (enum: `valid`/`invalid`) |

### The event envelope schema

The envelope is the most constrained object in the file. It requires all seven projection fields — `session_id`,
  `seq`, `created_at_utc`, `actor`, `type`, `payload`, `payload_sha256` — with `payload` typed as `object`, `seq` as
  `integer`, the rest as `string`. `additionalProperties` is false: an envelope with an extra field fails validation,
  so additive drift is caught as loudly as destructive drift.

### Where validation runs

Validation runs in the e2e suite (`cmd/flowstate` Ginkgo specs) against real command output captured through the built
  binary. It runs at test time only; there is no runtime validation in the binary itself. The schemas ship in the
  repository (`schemas/cli.json`), so they double as the machine-readable documentation of the CLI's JSON interfaces.

### The failed generator attempt, and why hand-authoring won

A reflection-based generator (`invopop/jsonschema`) was attempted first. It violated the owning-layer contract: the
  generator imported an external library into `cmd/flowstate`, which the whole-file gate rejects. Moving the generator
  to `internal/app` would have required exporting the cmd wire types into app — inverting the dependency the owning
  layer exists to protect. The generator and its mirror types were removed; the schemas were hand-authored from the
  wire structs. See [[ADR - Owning-Layer Boundary: CLI Wires Application Only]] for the boundary rationale and
  Alternatives A below for the full post-mortem.

## Consequences

### Positive

1. **The contract sentence is true.** Every command's `--json` output is verified against a committed schema on every
  CI run.
2. **Shape drift fails loudly.** Field addition, removal, or type change breaks the validation spec immediately —
  including additive drift, which golden files would tolerate.
3. **Executable interface documentation.** `schemas/cli.json` is the canonical statement of the CLI's
  machine-parseable surface; consumers can pin against it.
4. **Zero production overhead.** The validator is a test-only dependency; the binary's import graph is unchanged
  (verified by the code-contract gate).
5. **Replay integrity is schema-pinned.** The envelope's `payload_sha256` field is required by schema, so a refactor
  that drops it fails tests rather than silently weakening replay.

### Negative

1. **Schemas are hand-maintained.** An output change requires editing both the Go struct and the schema; the
  validation spec catches forgetting the schema, but only at test time.
2. **One more test dependency.** `santhosh-tekuri/jsonschema/v6` adds ~12 lines to `go.sum`. It is test-scoped
  (`go.mod` references it for the e2e test build).
3. **Structure-only validation.** The schemas check field names, types, and requiredness — not value semantics. A
  timestamp string that is not valid RFC3339 passes. Semantic correctness is covered by the store's replay
  verification, not by schema.
4. **Test-time only.** A runtime bug can still emit non-conforming output between releases; the schema catches it in
  CI, not in production.

### Neutral

1. `additionalProperties: false` on the envelope means additive optional fields require a schema change — deliberate;
  the envelope is a frozen contract, not an extensible bag.
2. The combined single-file layout (`schemas/cli.json`) mirrors the CLI's single-binary deployment; per-command files
  were considered and rejected as overhead without benefit.
3. The receipt validator predates this ADR and already emitted the verdict shape the `receipt-validate` schema pins.

## Alternatives considered

### A. Reflection-based generation (`invopop/jsonschema`) from Go wire structs

**Attempted, abandoned.** The generator lived in `cmd/flowstate` and imported `github.com/invopop/jsonschema`,
  violating the owning-layer contract — the code-contract gate rejected the file. Moving it to `internal/app` would
  fix the import but require exporting cmd's wire types into app, inverting the dependency direction the boundary
  protects. The mirror types in `types.go` were flagged unused by golangci-lint once the generator was removed,
  requiring cleanup. Hand-authoring sidesteps all three problems at the cost of manual maintenance, which the
  validation spec makes safe (drift fails CI).

### B. Golden-file comparison

**Rejected.** Committing fixture outputs and comparing byte-for-byte catches *some* drift but: requires updating
  fixtures on every intentional change (high-maintenance), cannot distinguish intentional from accidental change (both
  fail identically), and tolerates additive drift if fixtures are regenerated carelessly. Schema validation expresses
  *intent* (this field, this type, no others) rather than *incidents* (this exact byte sequence).

### C. Protobuf-generated types and schemas

**Rejected.** Generating Go types and JSON Schema from `.proto` definitions would give cross-language type safety, but
  the CLI has no cross-language consumers, the schema surface is six shapes, and the build-system cost (protoc
  toolchain, codegen step in CI) is not justified for a single-binary CLI. Revisit if an external API surface
  materialises.

### D. Runtime validation in the binary

**Rejected.** Embedding the validator and schemas in the binary would catch non-conforming output at emission time,
  but adds a production dependency, startup cost, and a failure mode (output refused at runtime) that is worse than
  the disease. Test-time validation plus the contract's stable wire structs is the right trade for a CLI whose output
  shapes change via reviewed PRs.

### E. Validate only the event envelope (highest risk, least work)

**Rejected as insufficient.** The envelope is the riskiest shape, but `session list`/`receipt validate` outputs are
  equally machine-consumed. The marginal cost of six schemas over one is small; the marginal protection is the whole
  §5 checklist item.

## Cross-links

### Sibling ADRs (Go CLI group)

- [[ADR - Owning-Layer Boundary: CLI Wires Application Only]] — the constraint that ruled out generator approach A;
  also explains why the schemas live in `schemas/` rather than being derived in cmd.
- [[ADR - Session Run Lifecycle]] — the event envelope validated by `session-events`/`session-replay` schemas is the
  lifecycle's audit spine.
- [[ADR - Crash-Recovery Detection and Recovery]] — the `session.interrupted` event flows through the same envelope
  schema.

### Prototype ADRs

- [[ADR - Testing Strategy]] — Ginkgo v2/Gomega as the test substrate; the validation specs are ordinary Ginkgo specs
  in the e2e matrix.
- [[ADR - BDD Declarative Steps]] — executable-specification philosophy; schemas are the declarative form of "every
  command's output validates".

## Sources

- `schemas/cli.json` — the six schema keys; envelope requires seven fields with `additionalProperties: false`.
- `go.mod:11` — `github.com/santhosh-tekuri/jsonschema/v6 v6.0.3` (test dependency).
- `cmd/flowstate/schemagen*.go` — absent; the abandoned generator's removal is verified by the file not existing and
  by PR #295's diff.
- Dropbox `CLI-AND-API-CONTRACT.md` §5 — the done-checklist item this ADR completes.
- PR #295 — the landing packet (schemas + e2e validation specs).

## Related Plans

- *Completed:* PR #295; contract §5 checklist now fully checked.
- *Possible follow-up:* value-semantics validators (RFC3339, SHA-256 format) as schema `format` assertions if
  downstream consumers need them; deliberately deferred.
