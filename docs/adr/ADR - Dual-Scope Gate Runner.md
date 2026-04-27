---
title: ADR - Dual-Scope Gate Runner
created: 2026-04-27
modified: 2026-04-27
tags:
  - project/flowstate
  - topic/architecture
  - topic/adr
  - topic/swarm
  - topic/orchestration
type: adr
status: accepted
adr_group: Agent Platform
---

# ADR: Dual-Scope Gate Runner

## Status

**Accepted** — 27 April 2026

## Date

27 April 2026

## Context

[[ADR - Multi-Agent Orchestration]] formalised the swarm pattern but left the gate semantics implicit. Concretely: when does a swarm advance? Who validates that a member's output is fit for downstream consumption? The first cut of swarm orchestration treated each agent's output as authoritative — if the agent finished, the swarm moved on. Bug sessions in April demonstrated that this breaks for the planning loop, where a plan-writer can produce JSON-shaped prose that *looks* like a plan but fails schema validation, and where a reviewer's verdict needs to short-circuit the loop without the orchestrator having to introspect free-form text.

The swarm engine needs lifecycle hooks that:

1. Run at deterministic points (before/after the swarm, before/after each member).
2. Can fail-closed on invalid output (e.g. a plan-writer output that doesn't match `result-schema.plan.v1`).
3. Are extensible without coupling the swarm engine to specific gate kinds.

## Problem

1. **Gate scope ambiguity** — A "gate" could mean "approve before the swarm starts" or "validate this member's output". The same word is used at different lifecycle points with different state available.
2. **No structured output validation** — The plan-writer / planner / plan-reviewer pipeline produced JSON-as-prose with no enforcement. Downstream consumers parsed and recovered ad-hoc, leaking schema knowledge into many call sites.
3. **No extension surface** — Hardcoding gates in the swarm engine would re-couple orchestration to domain logic, which [[ADR - Multi-Agent Orchestration]] explicitly forbids.
4. **Discovery vs registration** — Schemas need to ship with the binary AND be operator-extendable without rebuild. Two seeding strategies must coexist deterministically.

## Decision

The swarm engine exposes a single `swarm.GateRunner` interface, invoked at four named lifecycle points. Gates declare a `kind:` field; the engine looks the kind up in a registry and dispatches.

### Lifecycle points

| Point | When | State available |
|---|---|---|
| `pre` | Before the swarm starts. | Initial swarm context, no member outputs. |
| `post` | After all members finish. | All member outputs, terminal state. |
| `pre-member` | Before member N's turn. | Outputs of members 0..N-1. |
| `post-member` | After member N's turn. | Output of member N (and prior members). |

Each lifecycle slot is independently optional. A gate set may bind a `result-schema` validator to `post-member` for `plan-writer` and a no-op for everyone else.

### Gate kinds

Gates carry a `kind:` discriminator:

- `kind: builtin:result-schema` — validate the addressed member's output against a registered JSON schema. Failure aborts the swarm with a structured error citing the schema id.
- `kind: ext:*` — **deferred**. The namespace is reserved for operator-supplied gate kinds (binary plugin, WASM, sub-process). No implementation in this ADR. The reservation exists so that the manifest format does not need to break later.

### Schema registry

Schemas are addressed by id (e.g. `result-schema.plan.v1`). The registry is seeded from two sources, in deterministic precedence order:

1. **Programmatic seed** — `internal/swarm/schemas.go` registers the canonical built-in schemas at package init. These ship with the binary and are guaranteed present.
2. **File discovery** — `internal/swarm/schema_loader.go` scans the swarm config directory at app boot. A file declaring an id that already exists in the programmatic seed **overrides** it. This is the operator's escape hatch: amend a schema without rebuilding.

The override direction is "file > programmatic". Documented explicitly because the obvious-but-wrong default (programmatic > file) would silently ignore operator changes.

## Implementation

```go
// internal/swarm/gates.go
type GateRunner interface {
    Run(ctx context.Context, scope GateScope, state GateState) error
}

type GateScope string
const (
    GatePre        GateScope = "pre"
    GatePost       GateScope = "post"
    GatePreMember  GateScope = "pre-member"
    GatePostMember GateScope = "post-member"
)
```

The result-schema gate (`internal/swarm/gate_result_schema.go`) reads the addressed member's output, parses it as JSON, and validates against the named schema. A validation failure produces a structured error with the schema id, the failing JSON path, and the violated constraint.

The schema loader (`internal/swarm/schema_loader.go`) walks the swarm config directory at boot, parses each file, and registers it — overwriting any programmatic registration with the same id.

Wiring: `internal/engine/delegation.go` invokes the gate runner at each scope as the swarm advances. A non-nil error from any scope aborts the swarm and surfaces the error to the orchestrator.

## Consequences

### Positive

- **Deterministic validation** — The plan-writer cannot produce schema-invalid JSON undetected. The gate fires before downstream consumers see the output.
- **Domain logic stays out of the engine** — Gate kinds are data; the engine dispatches by kind without knowing what `result-schema.plan.v1` means.
- **Operator override** — File discovery means amending a schema does not require a rebuild.
- **Extensibility reservation** — `kind: ext:*` is reserved without committing to an implementation, so manifests that anticipate it remain forward-compatible.

### Negative

- **Two seeding paths** — Programmatic seed plus file discovery means two places to look when debugging "why isn't this schema registered". Mitigated by the explicit precedence rule.
- **Gate ordering coupling** — `pre-member` for member N+1 fires after `post-member` for member N. The ordering is documented but is a contract callers depend on.
- **`ext:*` is a promise** — Reserving the namespace without implementing it means the first real `ext:*` gate will discover requirements (sandboxing, transport) that may force schema changes.

## References

- Commit `2906fbf` — T-swarm-3 post-member gate runner (Phase 1).
- Commit `b8d8d43` — T-swarm-3 lifecycle expansion + schema discovery (Phase 2).
- Commit `7e95f66` — post-member result-schema gates per planning-loop member.

## Related

- [[ADR - Multi-Agent Orchestration]] — the parent ADR; this ADR formalises the gate semantics it left implicit.
- [[ADR - Coordination Store Boundary]] — gates may inspect coordination store entries written by prior members.
- [[ADR - Agent Manifest Format]] — manifests reference schema ids that this registry resolves.
