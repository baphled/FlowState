# Swarm Documentation

FlowState's swarm system coordinates teams of specialist agents that work
together to solve complex tasks. A swarm is defined by a YAML manifest that
specifies a lead agent, member agents, dependency ordering, retry policies,
and optional quality gates.

## Reading Order

New to swarms? Start here and work down:

| # | Document | Covers |
|---|----------|--------|
| 1 | [Getting Started](getting-started.md) | Prerequisites, installing your first swarm, triggering from CLI and TUI |
| 2 | [Overview](overview.md) | Core concepts (lead, members, delegation, gates, coordination store), architecture, registry flow |
| 3 | [Manifest Reference](manifest-reference.md) | Complete YAML schema, validation rules, retry/circuit-breaker config, gate spec, examples |
| 4 | [Gates](gates.md) | Lifecycle points, built-in and external gates, request/response formats, authoring custom gates |
| 5 | [Testing](testing.md) | Validation commands, gate isolation testing, debugging, failure modes, coordination store inspection |

## Quick Reference

### Triggering a swarm

- **CLI:** `flowstate run --agent <swarm-id>`
- **TUI chat:** `@<swarm-id> your task description`
- **Agent picker:** `Ctrl+A` in the TUI

### CLI commands

```bash
flowstate swarm list                # List all registered swarms
flowstate swarm validate [<id>]     # Validate manifest(s), ID is optional
```

### Directory layout

```
~/.config/flowstate/
├── swarms/          # Swarm manifests (*.yml / *.yaml)
├── agents/          # Agent manifests (*.md / *.json)
├── gates/           # External gate directories
├── schemas/         # JSON schemas for result-schema gates
└── config.yaml      # Provider credentials and global config
```

### Key constraints

- The lead agent must have `can_delegate: true` in its manifest
- Swarm IDs must not collide with agent IDs
- Only `*.yml` files are embedded in the binary (not `*.yaml`)
- `flowstate swarm run` is a stub — use `flowstate run --agent` instead
- Agent and swarm discovery happens at startup — no refresh needed after
  copying manifests to disk
