# Backend architecture overview

FlowState's current milestone is a native Go CLI/headless engine. The accepted
product boundaries and milestone scope live in Dropbox `/FlowState/Dev`; read the
entry point and current packet before work. This guide describes source ownership,
not completed capabilities or runtime qualification.

## Dependency direction

CLI adapters call application use cases. Explicit composition wires consumer-owned
ports to persistence, provider and tool adapters. Domain code owns invariants and
lifecycle; transports and concrete storage remain outside that boundary.

| Area | Source responsibility |
| --- | --- |
| `cmd/flowstate` | Parse input and render stable output/exits |
| `internal/app` | Compose collaborators and application use cases |
| `internal/runtime`, `internal/session` | Lifecycle, identity and ordering policy |
| `internal/provider`, `internal/tool` | Replaceable ports and domain-facing values |
| Provider/auth/storage adapters | Implement external effects through those ports |

See [the enforced code structure contract](../../rules/ARCHITECTURE.md) for exact
ownership and import rules. Existing concrete-store coupling is refactoring debt;
its presence is not permission to add another dependency or a new global.

## Changes and proof

A bounded refactor preserves the public contract, failure behaviour and tests.
Keep named types with their owning package, inject collaborators, document the
public surface and update the relevant guide with the change. Read [documentation
standards](../../rules/DOCUMENTATION.md) and [the test contract](../../rules/BDD_WORKFLOW.md).

Source checks and unit/integration tests do not prove live provider qualification,
T019 acceptance, product readiness, actual AI identity or independent review.
Resolve candidate-bound evidence only from the assigned Dropbox Metadata packet.
