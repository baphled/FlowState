# Swarm Gate Design

> Design notes on lifecycle gates, cross-swarm dependencies, and the defer policy.

## The Problem

Some swarms have **pre-member gates** that depend on prior wave outputs. When invoked standalone (without running the prerequisite swarms first), these gates fail because the expected coordination-store keys don't exist.

### Example: dev-feature

`dev-feature` implements a 5-wave workflow:

```
Wave 1 → Wave 2 → Wave 3 → Wave 4 → Wave 5
         ↓         ↓        ↓
       writer   reviewer → Engineer → quality reviewers
                            ↓
                       BLOCKED: needs
                       plan-reviewer output
```

The pre-member gate `ext:dev/plan-approved` reads `dev-feature/plan-reviewer/plan-review` and blocks Senior-Engineer unless it exists.

**When you run:**
```bash
@dev-feature "create hello world"
```

You're jumping straight to Wave 4. The gate fails because:
1. Waves 1-3 never ran
2. No `plan-review` output exists
3. Delegation is blocked

**The same issue occurs** whenever a swarm has:
- Pre-member gates checking for prior member outputs
- Dependencies on other swarm runs

## Solution 1: Chain the Swarms

Run planning-loop first, then dev-feature:

```bash
@planning-loop "add rate limiting to the API gateway"
# → plan-reviewer produces verdict="approved"

@dev-feature "implement rate limiting"
# → gate passes, Senior-Engineer runs
```

## Solution 2: `failurePolicy: defer`

When a gate depends on output that may not exist yet, use `failurePolicy: defer` to **poll until it passes** instead of failing immediately:

```yaml
gates:
  - name: pre-senior-engineer-plan-approved
    kind: ext:dev/plan-approved
    when: pre-member
    target: Senior-Engineer
    output_key: plan-review
    failurePolicy: defer
    defer_interval: 2s    # poll every 2 seconds (default: 1s)
    defer_timeout: 5m     # give up after 5 minutes (default: no timeout)
```

### How it works

1. Gate runs and fails (output not ready)
2. Dispatcher enters polling loop
3. Every `defer_interval`, re-runs the gate
4. On pass: records `DeferredEntry` with attempts + elapsed, continues
5. On timeout or context cancellation: treats as halt

### Behaviour summary

| Scenario | Result |
|----------|--------|
| Gate passes on first try | DeferredEntry recorded, continues (no polling) |
| Gate passes after N polls | DeferredEntry recorded with attempts=N |
| `defer_timeout` expires | Halted, GateError returned |
| Parent context cancelled | Halted immediately |

### When to use defer vs halt

| Policy | Use when |
|--------|----------|
| `halt` (default) | Gate must pass immediately; failure is a hard error |
| `continue` | Gate failure is informational; swarm should continue |
| `warn` | Gate failure is a warning; swarm should continue with UI flag |
| `defer` | Gate depends on output that may appear later (coord-store, prior wave, external swarm) |

## Design Rules for Swarm Authors

### 1. Document Prerequisites

Every swarm with gates MUST document its dependencies:

```yaml
# dev-feature.yml
# Prerequisites:
#   - Run @planning-loop first to produce plan-review output
#   - Gate pre-senior-engineer-plan-approved blocks without it
#   - Uses failurePolicy: defer so the gate waits for the output
```

### 2. Know Your Gate Types

| Gate | Fires | Blocks |
|------|-------|--------|
| `pre` | Before any member runs | All members |
| `pre-member` | Before specific member | That member only |
| `post-member` | After specific member | Next member |
| `post` | After lead stream ends | Swarm completion |

### 3. Avoid Premature post Gates

The `post` gate fires when the **lead's stream completes** — NOT when all members complete. If members are still running (or were never reached), the gate looks for output that doesn't exist.

### 4. Test Standalone First

Before publishing a swarm, test it with a simple task that exercises all waves:

```bash
@my-swarm "do something simple"
```

If it fails, check for missing dependencies or gate issues.

### 5. Use defer for Cross-Swarm Dependencies

When a gate depends on output from another swarm or a prior wave, use `failurePolicy: defer` with a reasonable timeout. This lets the swarm wait for the prerequisite instead of failing immediately.

### 6. Set Reasonable Timeouts

- `defer_interval`: 1-5 seconds (faster feedback vs lower API load)
- `defer_timeout`: Match the expected upstream runtime (e.g. 5m for planning-loop)

## Related

- `planning-loop` — produces plan + verdict for dev-feature
- `a-team` — no gates, works standalone
- `internal/swarm/gate_policy.go` — Dispatch + defer polling implementation
- `internal/swarm/manifest.go` — GateSpec fields + validation