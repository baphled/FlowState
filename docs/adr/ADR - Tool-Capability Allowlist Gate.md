---
title: ADR - Tool-Capability Allowlist Gate
created: 2026-04-27
modified: 2026-04-27
tags:
  - project/flowstate
  - topic/architecture
  - topic/adr
  - topic/agents
  - topic/delegation
  - topic/providers
type: adr
status: accepted
adr_group: Agent Platform
---

# ADR: Tool-Capability Allowlist Gate

## Status

**Accepted** — 27 April 2026

## Date

27 April 2026

## Context

FlowState delegates work to sub-agents by streaming tool-augmented requests through the configured (provider, model) pair on the delegate's manifest. Several models on the wire — particularly local models served through Ollama, LM Studio and OpenAI-compatible shims — silently fail to emit tool-call deltas. The provider returns prose, the engine sees no `tool_use` blocks, and the delegated agent never executes the tools its manifest declares. The user observes "agent ran but did nothing"; the operator sees a successful HTTP exchange.

This is the failure profile documented in [[GLM Delegation Failure After Rebuild (April 2026)]] and surveyed across the local fleet in [[Local Model Matrix (April 2026)]]. The matrix is empirical: each row is a model that has been hand-tested against FlowState's tool-call shape and either confirmed working or confirmed broken. Models outside the matrix are unknown — they may work, they may not, and a silent failure is far more expensive to diagnose than a loud refusal at delegation time.

## Problem

1. **Silent zero-tool-call delegation** — A delegate engine streamed against an incapable model returns assistant prose. The orchestrator sees a "successful" delegation and proceeds. No error path catches this.
2. **No prevention layer** — Tool-capability was a property carried only in operator memory. Adding a new local model meant trying a delegation, watching it fail silently, and reading the KB to learn the model is broken.
3. **Fleet drift** — As local model lineups shift (qwen, glm, deepseek, gpt-oss, etc.) the empirical "which models actually emit tool-calls" answer drifts faster than the operator can re-test.
4. **Override pressure** — Operators legitimately need to test new models without editing source. The matrix must be data, not code, with an override path.

## Decision

The engine MUST refuse to construct a delegated stream when the resolved (provider, model) for the delegate is not on a known-good tool-call list. The check runs before any provider request is issued — fail-closed at delegation time, not after the fact.

### Resolution rules

The gate has two lists, both pattern-matched against the model identifier:

| List | Source | Effect |
|---|---|---|
| `cfg.ToolCapableModels` | Programmatic defaults seeded from the [[Local Model Matrix (April 2026)]], merged with operator overrides from `flowstate.yaml`. | A match marks the model as a candidate for delegation. |
| `cfg.ToolIncapableModels` | Programmatic defaults from confirmed-broken cases, merged with operator overrides. | A match REJECTS the model regardless of allow-list state. |

**Precedence: deny > allow.** A model listed in both lists is rejected. The KB evidence of broken tool-calls is a fail-closed signal; no allow-list entry can override a positive deny.

**Unknown is rejected.** A model that matches neither list is NOT capable. The operator opts in by adding a pattern to `tool_capable_models` after testing.

### Pattern shape (`matchesPattern`)

A single `*` wildcard is supported anywhere in the pattern:

- `prefix*` — match models whose name starts with `prefix`.
- `*suffix` — match models whose name ends with `suffix`.
- `prefix*suffix` — match models matching both ends.
- no `*` — literal exact match.

Multiple `*` characters are interpreted as prefix-up-to-first-star plus suffix-after-last-star; middle `*` are not independent wildcards. The empty pattern never matches; the empty model never matches.

Examples:

- `claude-*` matches `claude-sonnet-4`, `claude-3-5-haiku`.
- `qwen3:*` matches `qwen3:8b`, `qwen3:14b`, `qwen3:30b-a3b`.
- `gpt-*-mini` matches `gpt-4o-mini`, `gpt-5-mini`.
- `glm-4.7` matches only `glm-4.7`.

### Override knobs

Operators can extend or override defaults without rebuilding:

```yaml
# flowstate.yaml
tool_capable_models:
  - "qwen3:*"
  - "claude-*"
  - "gpt-*"
  - "glm-*"
tool_incapable_models:
  - "qwen2.5-coder:14b"
  - "llama3.2:*"
```

The merge is union-based: programmatic defaults are present unless the operator explicitly redefines the field.

## Implementation

```go
// internal/engine/tool_capability.go
func IsToolCapableModel(_ string, model string, allow, deny []string) bool {
    if model == "" {
        return false
    }
    if matchesAnyPattern(model, deny) {
        return false
    }
    return matchesAnyPattern(model, allow)
}
```

The `provider` argument is reserved in the signature for future scoping (e.g. "anthropic claude-* is fine but ollama claude-3-haiku-clone isn't") without callsite churn.

The gate fires from `internal/engine/delegation.go` before the delegate engine streams. A rejection produces a structured error message naming the model so the operator can either fix the manifest or extend the allowlist.

## Consequences

### Positive

- **Loud failure** — Silent zero-tool-call delegations become impossible. The operator sees an immediate, named refusal instead of an apparently-successful but empty response.
- **Matrix-as-data** — The KB-verified [[Local Model Matrix (April 2026)]] is the source of truth; the code embeds a snapshot but the operator can override.
- **Fail-closed defaults** — Unknown models are rejected. New entries require an explicit decision.
- **Audit clarity** — `grep tool_capable_models` shows the operator policy.

### Negative

- **Bootstrap friction** — Operators trying a new model must extend the allowlist before delegation works. This is the intended trade.
- **Drift between code defaults and KB** — When the matrix changes, both `internal/config/config.go` and the matrix note must be updated. Mitigated by keeping the embedded list small and pointing at the KB.
- **Pattern surface area** — Single-star globs cover the empirical shapes; more complex matchers (regex, semver) are deferred until needed.

## References

- Commit `0ec34ed` — initial allowlist gate before sub-agent stream.
- Commit `5799e21` — defaults updated from research-backed matrix.
- Commit `847bdd8` — tested-good allowlist + middle-glob matcher upgrade.
- Commit `a7fbc3f` — `glm-*` regression fix.
- [[GLM Delegation Failure After Rebuild (April 2026)]] — investigation that motivated the gate.
- [[Local Model Matrix (April 2026)]] — empirical capability matrix.
- [[Tool-Capable Allowlist glm-Star Regression (April 2026)]] — bug session pinning the matcher's middle-glob shape.

## Related

- [[ADR - MCP Tool Gating by Agent Manifest]] — manifest-side complement: this ADR gates the model, that ADR gates which tools the manifest exposes to that model.
- [[ADR - LLM Provider Abstraction]] — provider layer that the gate guards.
- [[ADR - Multi-Agent Orchestration]] — delegation contract whose silent-failure mode this ADR closes.
