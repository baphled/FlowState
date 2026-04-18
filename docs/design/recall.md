# RecallBroker Opt-In Model (P13)

## Overview

The RecallBroker feeds distilled observations from prior sessions and the
vault knowledge base into an agent's context window during assembly. Before
P13 it fired on every turn for every agent, regardless of whether the agent
benefited from recalled context. Tool-focused executors, routers, and review
agents paid the per-turn query cost and saw their context window polluted
with unrelated observations.

P13 makes recall **opt-in per agent** via the `uses_recall` manifest flag.
The broker is still a first-class engine dependency; the context-assembly
hook registered with the broker now short-circuits when the current agent
has not declared `uses_recall: true`.

## Manifest Flag

```yaml
---
id: agent-name
uses_recall: true   # opt in — hook queries the broker on every turn
# or
uses_recall: false  # opt out — hook returns early, broker is never called
---
```

`uses_recall` lives on `agent.Manifest` (see
`internal/agent/manifest.go`). The field is a plain `bool` — missing
values default to `false`. This default is deliberate: recall is a
specialised capability, not a reflex.

### JSON / YAML contract

| Format | Key | Absent → |
|--------|-----|----------|
| YAML frontmatter | `uses_recall` | `false` |
| JSON manifest    | `uses_recall` | `false` |

## Hook Gate

`buildContextAssemblyHooks` in `internal/engine/engine.go` builds the
context-assembly hook chain at engine construction time. When
`cfg.RecallBroker` is non-nil, it registers a hook that captures the
manifest's `UsesRecall` flag by value:

```go
usesRecall := cfg.Manifest.UsesRecall
hooks = append(hooks, func(ctx context.Context, payload *plugin.ContextAssemblyPayload) error {
    if !usesRecall {
        return nil // P13 opt-in gate
    }
    observations, err := broker.Query(ctx, payload.UserMessage, 5)
    if err != nil {
        return err
    }
    payload.SearchResults = append(payload.SearchResults, obsToSearchResults(observations)...)
    return nil
})
```

Each `engine.Engine` instance is bound to a single manifest, so the
captured flag is effectively constant for the engine's lifetime. The hook
closure does not share mutable state with the `Config` struct after
construction.

### Why gate inside the hook rather than skipping registration?

Registering a no-op hook (instead of omitting it outright) keeps the hook
registry symmetric across agents and preserves downstream ordering
guarantees for any future hooks that depend on position. The cost of the
closure entry is trivial compared to an MCP-backed broker query.

## Agent Perimeter

As of P13, the following agents opt into recall:

| Agent | `uses_recall` | Rationale |
|-------|---------------|-----------|
| `analyst` | `true` | Evidence synthesis benefits from recalled observations of prior research turns. |
| `executor` | `false` | Tool-focused, plan-driven execution. Recall would pollute the context with unrelated prior context. |
| `explorer` | `false` | Read-only, evidence-first codebase searches grounded in file-system queries. |
| `librarian` | `false` | External references per task — recall would dilute externally-sourced evidence. |
| `plan-reviewer` | `false` | Reviews a plan delivered via the coordination store. No recall dependency. |
| `plan-writer` | `false` | Plans come from explicit coordination-store evidence; recall would blur evidence boundaries. |
| `planner` | `false` | Router/coordinator. Decisions come from delegated specialist outputs, not self-memory. |

The list is intentionally small. Adding a new agent with `uses_recall:
true` should be a deliberate, justified decision documented in the
agent's manifest frontmatter alongside the flag.

## Backwards Compatibility

- **Old manifests without `uses_recall`**: parse as `false`. Any agent
  that benefited from recall under the pre-P13 always-on policy will
  stop receiving observations until its manifest is updated to
  `uses_recall: true`.
- **Tests constructing `agent.Manifest{}` inline**: previously got
  recall by default; now must set `UsesRecall: true` explicitly to
  exercise the broker path. The P13 change updated both
  `internal/engine/context_assembly_hook_integration_test.go` and
  `internal/engine/recall_pipeline_integration_test.go` accordingly.
- **The broker itself is unchanged.** The `recall.Broker` API, its
  internal query fan-out, and the P7 `vaultSource` empty-vault gate
  all remain in place.

## Non-Goals

- P13 does **not** add a global "disable recall" toggle. Per-agent
  control is the primary contract.
- P13 does **not** remove the broker. It remains a first-class engine
  dependency for opted-in agents.
- P13 does **not** change what recall returns, only who receives it.

## Inventory Guard

`internal/app/agents_uses_recall_inventory_test.go` walks the embedded
agent filesystem (`internal/app/agents/*.md`) and asserts every manifest
declares `uses_recall` explicitly. Missing entries fail the test so
nobody inherits the default silently — P13 is a deliberate choice, not a
fall-through.

## Related Work

- **P7** — gated the vault MCP source inside the broker when the vault
  string is empty, stopping the 185-line log storm.
- **P12** — introduced `suggest_delegate` for non-delegating agents; P13
  follows the same pattern of per-agent capability gates.
