# Swarm Manifest Reference

A swarm manifest is a YAML file that defines a multi-agent collaboration
topology. Manifests must be placed in `~/.config/flowstate/swarms/` and use
the `.yml` or `.yaml` extension.

## Minimal Manifest

```yaml
schema_version: "1.0.0"
id: my-swarm
lead: planner
members:
  - explorer
  - analyst
```

This swarm runs the `planner` agent as lead, which can delegate to `explorer`
and `analyst` in any order it chooses.

## Complete Schema

### Top-Level Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `schema_version` | `string` | Yes | — | Must be `"1.0.0"`. Unknown or missing values are rejected. |
| `id` | `string` | Yes | — | Globally unique swarm identifier. Must not collide with any registered agent ID. |
| `description` | `string` | No | `""` | Human-readable description of the swarm's purpose. |
| `lead` | `string` | Yes | — | Agent ID (or swarm ID) that runs first. Must resolve to a registered agent or swarm. |
| `members` | `[]string` | No | `[]` | Roster of agent IDs or swarm IDs the lead can delegate to. |
| `swarm_type` | `string` | No | `"analysis"` | One of: `analysis`, `codegen`, `orchestration`. Maps to per-type depth defaults. |
| `max_depth` | `int` | No | per-type | Manifest-level override for delegation depth ceiling. Must be >= 0. |
| `harness` | `HarnessConfig` | No | — | Dispatch configuration (parallel mode, gates). |
| `context` | `ContextConfig` | No | — | Coordination store namespace configuration. |
| `retry` | `*RetryPolicy` | No | nil | Per-member retry policy. When nil, retry is disabled. |
| `circuit_breaker` | `*CircuitBreakerConfig` | No | nil | Swarm-wide circuit breaker configuration. When nil, circuit breaker is disabled. |

### HarnessConfig

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `parallel` | `bool` | `false` | When `true`, dispatches members concurrently using a semaphore pattern. When `false`, members run sequentially in member list order. |
| `max_parallel` | `int` | no cap | Bounds concurrent fan-out when `parallel` is `true`. Clamped to `spawnLimits.MaxTotalBudget`. |
| `gates` | `[]GateSpec` | `[]` | Lifecycle quality gates. See [GateSpec](#gatespec) below. |

### ContextConfig

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `chain_prefix` | `string` | swarm ID | Coordination-store namespace prefix. Sub-swarms append their ID with a slash (e.g. `engineering/planning`). |

### RetryPolicy

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_attempts` | `int` | `3` | Maximum number of execution attempts before giving up. |
| `initial_backoff` | `duration` | `1s` | Initial delay before the first retry. |
| `max_backoff` | `duration` | `60s` | Maximum delay between retries. |
| `multiplier` | `float` | `2.0` | Multiplier applied to backoff on each successive retry. |
| `jitter` | `bool` | `true` (only when entire retry block is omitted) | When true, adds +/-25% random jitter to backoff durations. **Note:** When `retry` is present but `jitter` is not specified, the Go zero value (`false`) applies.

### CircuitBreakerConfig

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `threshold` | `int` | `5` | Number of consecutive retryable failures before the circuit opens. |
| `cooldown` | `duration` | `30s` | Time the circuit stays open before transitioning to half-open. |
| `half_open_attempts` | `int` | `1` | Number of probe attempts allowed in half-open state. |

### GateSpec

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `name` | `string` | Yes | — | Unique gate identifier within the manifest. |
| `kind` | `string` | Yes | — | Must start with `builtin:` or `ext:`. See [Gate Kinds](gates.md#gate-kinds). |
| `when` | `string` | No | `"pre"` | Lifecycle point: `pre`, `post`, `pre-member`, `post-member`. |
| `target` | `string` | Conditional | — | Member ID. Required for `pre-member`/`post-member`; forbidden for `pre`/`post`. |
| `output_key` | `string` | No | `"output"` | Coord-store sub-key for reading member output. |
| `precedence` | `Precedence` | No | `"MEDIUM"` | One of: `CRITICAL`, `HIGH`, `MEDIUM`, `LOW`. Higher precedence gates run first. |
| `failurePolicy` | `FailurePolicy` | No | `"halt"` | One of: `halt`, `continue`, `warn`. Note the camelCase YAML key. |
| `timeout` | `duration` | No | no timeout | Per-gate timeout (e.g. `"5s"`, `"30s"`). |
| `schema_ref` | `string` | No | — | For `builtin:result-schema` gates: registered JSON Schema name. |

## Validation Rules

### Self-Reference Checks

- `id` must not equal `lead`
- No member may equal the swarm's own `id`

### Gate Prefix Rules

- `kind` must start with `builtin:` or `ext:`
- `builtin:` gates are handled internally (currently only `builtin:result-schema`)
- `ext:` gates invoke external executables registered in `~/.config/flowstate/gates/`

### Lifecycle-Target Pairing

| When | Target |
|------|--------|
| `pre` | Must not be set |
| `post` | Must not be set |
| `pre-member` | Must be set to a valid member ID |
| `post-member` | Must be set to a valid member ID |

### Cross-Registry Resolution

After all manifests are loaded, the registry checks:

- `lead` resolves to a registered agent or swarm
- Every member resolves to a registered agent or swarm
- No swarm ID collides with an agent ID
- No cycles exist in the membership graph (checked via depth-bounded DFS, max depth 64)

## Swarm Type Depth Defaults

| Swarm Type | Default Max Depth |
|------------|-------------------|
| `analysis` | 8 |
| `codegen` | 16 |
| `orchestration` | 32 |

The depth ceiling limits how deeply swarms can nest. It can be overridden per
manifest with `max_depth`.

## Example Manifests

### Simple Sequential Swarm

```yaml
schema_version: "1.0.0"
id: a-team
description: >
  A versatile generalist swarm for tasks that don't fit a fixed workflow.
lead: coordinator
members:
  - researcher
  - strategist
  - critic
  - writer
  - executor
harness:
  parallel: false
  gates:
    - name: relevance-check
      kind: ext:relevance-gate
      when: post-member
      target: researcher
context:
  chain_prefix: a-team
```

### Swarm-of-Swarms

```yaml
schema_version: "1.0.0"
id: engineering
description: >
  Top-level engineering orchestrator. Coordinates planning, implementation,
  and quality assurance as three sequential sub-swarms.
lead: planner
members:
  - engineering-planning
  - engineering-implementation
  - engineering-quality
harness:
  parallel: false
  gates:
    - name: ci-gate
      kind: ext:ci-gate
      when: post-member
      target: engineering-implementation
      output_key: output
context:
  chain_prefix: engineering
```

### Parallel Dispatch

```yaml
schema_version: "1.0.0"
id: research-cluster
description: Parallel research with bounded fan-out.
lead: research-lead
members:
  - web-researcher
  - codebase-explorer
  - literature-reviewer
harness:
  parallel: true
  max_parallel: 3
```
