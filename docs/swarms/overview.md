# Swarms

Swarms are multi-agent collaboration topologies in FlowState. A swarm coordinates
a set of agents — called **members** — under a single **lead** agent that decides
how and when to delegate work. Swarms enable complex, multi-phase workflows that a
single agent cannot handle alone, such as adversarial review, parallel research,
or sequential planning-implementation-quality pipelines.

## Core Concepts

### Lead

Every swarm has exactly one lead. The lead is the first agent to run and is the
only agent the user interacts with directly. The lead receives an augmented system
prompt containing the swarm ID, member roster, and delegation instructions. It
decides which members to engage and in what order.

### Members

Members are the agents the lead can delegate to. They are listed in the swarm's
`members` field and may be individual agent IDs or other swarm IDs (enabling
**swarm-of-swarms** composition). Members operate within the swarm's context,
sharing a coordination store namespace.

### Swarm-of-Swarms

A swarm can delegate to other swarms. When a member ID resolves to a registered
swarm rather than a single agent, FlowState recursively dispatches that sub-swarm
with a nested context. The parent swarm's chain prefix becomes a slash-delimited
path (e.g. `engineering/planning`), enabling full traceability across nesting
levels up to a depth ceiling.

### Delegation

The lead delegates to members via the `delegate` tool. In a swarm context,
delegations are intercepted and enriched: the engine injects the swarm context
into the member's system prompt, wraps execution through the swarm runner (for
retry and circuit-breaker semantics), and fires lifecycle gates before and after
each member.

### Gates

Gates are quality checkpoints evaluated at defined lifecycle boundaries. They
validate member output, enforce policies, or run external commands. Gates can
halt the swarm, warn, or continue on failure depending on their configured
failure policy.

Built-in gate kinds:
- `builtin:result-schema` — validates member output against a JSON Schema
- `ext:<name>` — external gates (user-authored scripts in any language)

### Coordination Store

Swarms use the coordination store as a shared message bus. The lead writes a
task plan before delegating; members read their briefs and write results to
agreed keys. The chain prefix (derived from the swarm ID) namespaces all keys
so concurrent swarms do not collide. The store is persisted as a single JSON
file at `~/.local/share/flowstate/coordination.json`.

### Prerequisites

For a swarm to function, the lead agent must have delegation enabled in its
manifest (`can_delegate: true`). Without this, the `delegate` tool is not
wired and the lead cannot dispatch to members.

### SwarmType and Depth Limits

Each swarm declares a `swarm_type` (`analysis`, `codegen`, or `orchestration`)
which determines the default depth ceiling for nested delegation:

| Swarm Type | Default Depth Limit |
|---|---|
| `analysis` | 8 |
| `codegen` | 16 |
| `orchestration` | 32 |

This ceiling prevents unbounded recursion in swarm-of-swarms compositions.

### Retry and Circuit-Breaker Policies

Per-member retry behaviour is configurable via `retry` and `circuit_breaker`
blocks in the manifest. Retry policies support `max_attempts`,
`initial_backoff`, `max_backoff`, and `jitter`. Circuit breakers define a
`threshold` (failure count to trip) and `cooldown` period before allowing
traffic again.

### Gate Precedence and Failure Policies

Gates declare a `precedence` level (`critical`, `high`, `medium`, or `low`)
and a `failure_policy` (`halt`, `continue`, or `warn`). Precedence determines
evaluation order; failure policy dictates what happens when a gate fails.

### Dispatch Modes

Members dispatch sequentially by default. Set `harness.parallel: true` in the
manifest to enable parallel dispatch, and use `harness.max_parallel` to cap
concurrent member execution.

## Architecture

```
User message (@a-team "research X")
  │
  ├─ @-mention resolver:
  │   ├─ ExtractAtMentions() scans message for @-mentions
  │   ├─ Resolve() checks agent registry first, then swarm registry
  │   └─ Returns KindSwarm if ID matches a registered swarm
  ├─ Swarm context built:
  │   SwarmID: a-team
  │   Lead: coordinator
  │   Members: [researcher, strategist, critic, writer, executor]
  │   Chain prefix: a-team/{chainID}
  │
  ├─ Lead engine starts (coordinator agent)
  │   ├─ System prompt augmented with swarm metadata
  │   └─ Delegates to members via delegate tool
  │
  ├─ Member dispatch (sequential or parallel)
  │   ├─ Pre-member gates (if any)
  │   ├─ Member streams through engine
  │   ├─ Post-member gates (if any)
  │   └─ Output persisted to coord-store
  │
  └─ Post-swarm gates fire
      └─ Final output returned to user
```

## Swarm Registry

Swarms are discovered from YAML manifests on disk. At startup, FlowState:

1. Seeds embedded `*.yml` swarm manifests to `~/.config/flowstate/swarms/`
   (note: only `.yml` files are embedded in the binary, not `.yaml`)
2. Loads all `*.yml` and `*.yaml` files from the swarm directory
3. Validates each manifest (schema, field types, self-references)
4. Registers all manifests into the swarm registry
5. Re-validates with the agent registry for cross-reference resolution
   (lead exists, members resolve, no cycles, no ID collisions)

A failed validation at step 5 does not prevent startup — the swarm remains in
the registry and a warning is logged. Use `flowstate swarm validate` (ID is
optional) to diagnose issues.

## When to Use a Swarm

Use a swarm when:
- The task requires multiple phases (research, write, review, implement)
- You need adversarial critique or quality gates
- The work can be decomposed into independent sub-tasks
- You want traceable, auditable delegation decisions

Use a single agent when:
- The task is straightforward and does not need decomposition
- You need a quick answer, not a structured workflow
- The agent already has all the tools and skills it needs

## Related Documentation

- [Getting Started](getting-started.md) — setup, install, and run your first swarm
- [Manifest Reference](manifest-reference.md) — complete schema reference
- [Gates](gates.md) — gate system and custom gate authoring
- [Testing](testing.md) — validating and testing swarm configurations
